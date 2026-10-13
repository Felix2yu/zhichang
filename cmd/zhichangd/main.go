// Command zhichangd 是知常的主服务：数据接入、查询 API、MCP 与前端静态资源。
package main

import (
	"context"
	"crypto/subtle"
	"errors"
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
	"github.com/Felix2yu/zhichang/internal/config"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
	ingest "github.com/Felix2yu/zhichang/internal/ingest/httpping"
	"github.com/Felix2yu/zhichang/internal/ingest/pipeline"
	mcpsrv "github.com/Felix2yu/zhichang/internal/mcp"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
	spa "github.com/Felix2yu/zhichang/internal/web"
	"github.com/Felix2yu/zhichang/web"
)

// version 由构建时注入。
var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("配置加载失败", "error", err)
		os.Exit(1)
	}
	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		slog.Error("数据库连接失败", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if cfg.MigrateOnBoot {
		applied, err := db.Migrate(ctx)
		if err != nil {
			slog.Error("迁移失败", "error", err)
			os.Exit(1)
		}
		if len(applied) > 0 {
			slog.Info("已应用迁移", "files", applied)
		}
	}
	if n, err := db.SeedMetrics(ctx, metric.Defaults()); err != nil {
		slog.Error("指标字典写入失败", "error", err)
		os.Exit(1)
	} else if n > 0 {
		slog.Info("指标字典已补齐", "新增", n)
	}
	registry, fromDB, err := db.LoadMetrics(ctx)
	if err != nil {
		slog.Error("指标字典加载失败", "error", err)
		os.Exit(1)
	}
	if !fromDB {
		slog.Warn("metric_defs 为空，使用内置字典", "指标数", len(registry.All()))
	}

	home, err := time.LoadLocation(cfg.HomeTZ)
	if err != nil {
		slog.Error("时区加载失败", "error", err)
		os.Exit(1)
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer, middleware.RequestID, middleware.RealIP)

	svc := pipeline.New(db, registry, home, cfg.DedupMode)
	ingest.New(svc, cfg.IngestToken, cfg.MaxBodyBytes, cfg.RawArchiveDir).Register(r)
	api.NewDashboard(db, registry, home, cfg.HomeTZ).Register(r)

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
