// Command zhichang-mcp 以 stdio 方式提供 MCP 服务，供本地 AI 客户端（Claude Desktop 等）接入。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Felix2yu/zhichang/internal/config"
	mcpsrv "github.com/Felix2yu/zhichang/internal/mcp"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

func main() {
	cfg, err := config.LoadForCLI()
	if err != nil {
		slog.Error("配置加载失败", "error", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		slog.Error("数据库连接失败", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	registry, _, err := db.LoadMetrics(ctx)
	if err != nil {
		slog.Error("指标字典加载失败", "error", err)
		os.Exit(1)
	}
	loc, err := time.LoadLocation(cfg.HomeTZ)
	if err != nil {
		loc = time.UTC
	}

	server := mcpsrv.New(mcpsrv.DepsFrom(db, registry, loc, cfg.HomeTZ))
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		slog.Error("MCP 服务异常", "error", err)
		os.Exit(1)
	}
}
