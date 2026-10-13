// Command zhichangctl 是知常的运维 CLI：迁移、自检、指标字典维护。
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Felix2yu/zhichang/internal/config"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "migrate":
		run(args, func(ctx context.Context, db *postgres.DB) error {
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
	case "doctor":
		run(args, func(ctx context.Context, db *postgres.DB) error {
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
	case "seed-metrics":
		run(args, func(ctx context.Context, db *postgres.DB) error {
			n, err := db.SeedMetrics(ctx, metric.Defaults())
			if err != nil {
				return err
			}
			fmt.Printf("新增指标定义: %d（已存在的不改动）\n", n)
			return nil
		})
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func run(args []string, fn func(context.Context, *postgres.DB) error) {
	fs := flag.NewFlagSet("zhichangctl", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "覆盖 ZHICHANG_DB_DSN")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *dsn != "" {
		_ = os.Setenv("ZHICHANG_DB_DSN", *dsn)
	}
	cfg, err := config.LoadForCLI()
	if err != nil {
		slog.Error("配置加载失败", "error", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		slog.Error("数据库连接失败", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := fn(ctx, db); err != nil {
		slog.Error("命令失败", "error", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`知常运维 CLI

用法:
  zhichangctl migrate        执行数据库迁移
  zhichangctl doctor         检查数据库与数据状态
  zhichangctl seed-metrics   补齐内置指标字典

全局参数:
  -dsn string   覆盖 ZHICHANG_DB_DSN
`)
}
