// Command zhichangd 是知常的唯一二进制：主服务、运维子命令与 MCP stdio 通道。
//
//	zhichangd                # 等同 serve
//	zhichangd serve          # 启动 HTTP 服务（接入端点 / 查询 API / MCP over HTTP / 前端）
//	zhichangd migrate        # 执行数据库迁移
//	zhichangd seed-metrics   # 补齐内置指标字典
//	zhichangd doctor         # 检查数据库与数据状态
//	zhichangd mcp            # 以 stdio 方式提供 MCP 服务（本地 AI 客户端用）
//	zhichangd version        # 打印版本
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Felix2yu/zhichang/internal/api"
	"github.com/Felix2yu/zhichang/internal/cli"
	"github.com/Felix2yu/zhichang/internal/config"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
	ingest "github.com/Felix2yu/zhichang/internal/ingest/httpping"
	"github.com/Felix2yu/zhichang/internal/ingest/pipeline"
	mcpsrv "github.com/Felix2yu/zhichang/internal/mcp"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
	spa "github.com/Felix2yu/zhichang/internal/web"
	"github.com/Felix2yu/zhichang/web"
)

const usage = `知常 —— 自托管健康数据平台

用法:
  zhichangd [serve]          启动服务（默认）
  zhichangd migrate          执行数据库迁移
  zhichangd seed-metrics     补齐内置指标字典
  zhichangd doctor           检查数据库与数据状态
  zhichangd mcp              以 stdio 方式提供 MCP 服务
  zhichangd version          打印版本

运维子命令支持 -dsn 覆盖 ZHICHANG_DB_DSN；其余配置全部来自环境变量。
`

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	switch cmd {
	case "serve":
		if err := serve(); err != nil {
			slog.Error("服务退出异常", "error", err)
			os.Exit(1)
		}
	case "version", "--version":
		fmt.Println(version)
	case "migrate":
		exitOn(cli.Migrate(args))
	case "doctor":
		exitOn(cli.Doctor(args))
	case "seed-metrics":
		exitOn(cli.SeedMetrics(args))
	case "mcp":
		exitOn(cli.MCP(args))
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		fmt.Print(usage)
		os.Exit(2)
	}
}

func exitOn(err error) {
	if err != nil {
		slog.Error("命令失败", "error", err)
		os.Exit(1)
	}
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.SetDefault(newLogger(cfg.LogLevel))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		return fmt.Errorf("数据库连接失败: %w", err)
	}
	defer db.Close()

	if cfg.MigrateOnBoot {
		applied, err := db.Migrate(ctx)
		if err != nil {
			return fmt.Errorf("迁移失败: %w", err)
		}
		if len(applied) > 0 {
			slog.Info("已应用迁移", "files", applied)
		}
	}
	if n, err := db.SeedMetrics(ctx, metric.Defaults()); err != nil {
		return fmt.Errorf("指标字典写入失败: %w", err)
	} else if n > 0 {
		slog.Info("指标字典已补齐", "新增", n)
	}
	registry, fromDB, err := db.LoadMetrics(ctx)
	if err != nil {
		return fmt.Errorf("指标字典加载失败: %w", err)
	}
	if !fromDB {
		slog.Warn("metric_defs 为空，使用内置字典", "指标数", len(registry.All()))
	}

	home, err := time.LoadLocation(cfg.HomeTZ)
	if err != nil {
		return fmt.Errorf("时区加载失败: %w", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer, middleware.RequestID, middleware.RealIP)

	svc := pipeline.New(db, registry, home, cfg.DedupMode)
	ingest.New(svc, cfg.IngestToken, cfg.MaxBodyBytes, cfg.RawArchiveDir).Register(r)
	api.NewDashboard(db, registry, home, cfg.HomeTZ, cfg.SettingsToken).Register(r)

	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"version":  version,
			"time":     time.Now().UTC().Format(time.RFC3339),
			"metrics":  len(registry.Enabled()),
			"timezone": cfg.HomeTZ,
		})
	})

	if cfg.MCP.Enabled {
		mcpServer := mcpsrv.New(mcpsrv.DepsFrom(db, registry, home, cfg.HomeTZ))
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
		r.Handle(cfg.MCP.Path, requireToken(cfg.MCP.Token, handler))
		slog.Info("MCP 已启用", "path", cfg.MCP.Path)
	}

	if assets, err := web.FS(); err == nil {
		r.Handle("/*", spa.Handler(assets))
	} else {
		slog.Warn("前端资源未内嵌，跳过静态服务", "error", err)
	}

	rollup := postgres.NewRollup(db, registry)
	go rollup.Run(ctx, cfg.RollupWorkers, cfg.RollupIdle)

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		slog.Info("知常已启动", "addr", cfg.HTTPAddr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务异常", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("HTTP 关闭异常", "error", err)
	}
	slog.Info("知常已停止")
	return nil
}

func requireToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(given), []byte(token)) != 1 {
			api.WriteError(w, http.StatusUnauthorized, "token 无效")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
