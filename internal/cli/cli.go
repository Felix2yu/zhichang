// Package cli 承载知常单二进制的子命令实现（运维与 MCP stdio）。
//
// 知常只有一个二进制 zhichangd：serve 是默认子命令，其余为运维与 MCP 通道。
// 这样镜像只需装配一个文件，Dockerfile 也就不必再内置 Go / Node 工具链。
package cli

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Felix2yu/zhichang/internal/config"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
	mcpsrv "github.com/Felix2yu/zhichang/internal/mcp"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// Migrate 执行数据库迁移。
func Migrate(args []string) error {
	return withDB(args, func(ctx context.Context, db *postgres.DB) error {
		applied, err := db.Migrate(ctx)
		if err != nil {
			return err
		}
		if len(applied) == 0 {
			fmt.Println("迁移已是最新")
			return nil
		}
		for _, a := range applied {
			fmt.Println("已应用:", a)
		}
		return nil
	})
}

// Doctor 检查数据库与数据状态。
func Doctor(args []string) error {
	return withDB(args, func(ctx context.Context, db *postgres.DB) error {
		info, err := db.Info(ctx)
		if err != nil {
			return err
		}
		fmt.Println("PostgreSQL:", info.PGVersion)
		fmt.Println("TimescaleDB:", info.TSDBVersion)
		fmt.Println("Hypertables:", info.Hypertables)

		var tables, metricCount, sampleCount int64
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM metric_defs`).Scan(&metricCount); err != nil {
			return err
		}
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(&tables); err != nil {
			return err
		}
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM samples`).Scan(&sampleCount); err != nil {
			return err
		}
		fmt.Printf("表数量: %d  指标定义: %d  样本数: %d\n", tables, metricCount, sampleCount)

		var last sql.NullTime
		if err := db.Pool.QueryRow(ctx, `SELECT max(received_at) FROM ingest_runs`).Scan(&last); err != nil {
			return err
		}
		if last.Valid {
			fmt.Println("最近同步:", last.Time.Local().Format(time.RFC3339))
		} else {
			fmt.Println("最近同步: 尚无记录")
		}
		return nil
	})
}

// SeedMetrics 补齐内置指标字典。
func SeedMetrics(args []string) error {
	return withDB(args, func(ctx context.Context, db *postgres.DB) error {
		n, err := db.SeedMetrics(ctx, metric.Defaults())
		if err != nil {
			return err
		}
		fmt.Printf("新增指标定义: %d（已存在的不改动）\n", n)
		return nil
	})
}

// MCP 以 stdio 方式提供 MCP 服务，供本地 AI 客户端（Claude Desktop 等）接入。
func MCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "覆盖 ZHICHANG_DB_DSN")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dsn != "" {
		_ = os.Setenv("ZHICHANG_DB_DSN", *dsn)
	}
	cfg, err := config.LoadForCLI()
	if err != nil {
		return err
	}
	// stdio 通道：stdout 只写 JSON-RPC，日志一律走 stderr
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	ctx, stop := signalContext()
	defer stop()

	return withDBAndMetrics(ctx, cfg, func(db *postgres.DB, registry *metric.Registry) error {
		loc, err := time.LoadLocation(cfg.HomeTZ)
		if err != nil {
			loc = time.UTC
		}
		server := mcpsrv.New(mcpsrv.DepsFrom(db, registry, loc, cfg.HomeTZ))
		if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
			return err
		}
		return nil
	})
}

func withDB(args []string, fn func(context.Context, *postgres.DB) error) error {
	fs := flag.NewFlagSet("zhichangd", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "覆盖 ZHICHANG_DB_DSN")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dsn != "" {
		_ = os.Setenv("ZHICHANG_DB_DSN", *dsn)
	}
	cfg, err := config.LoadForCLI()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	return fn(ctx, db)
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func withDBAndMetrics(ctx context.Context, cfg config.Config, fn func(*postgres.DB, *metric.Registry) error) error {
	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	registry, _, err := db.LoadMetrics(ctx)
	if err != nil {
		return err
	}
	return fn(db, registry)
}
