// Package postgres 封装 pgx 连接池、迁移与仓储。
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Felix2yu/zhichang/internal/config"
)

// DB 持有连接池与配置。
type DB struct {
	Pool *pgxpool.Pool
	Cfg  config.Config
}

// Open 建立连接池并验证连通性。
func Open(ctx context.Context, cfg config.Config) (*DB, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DBDSN)
	if err != nil {
		return nil, fmt.Errorf("解析 DB_DSN 失败: %w", err)
	}
	pcfg.MaxConns = 8
	pcfg.MinConns = 1
	pcfg.MaxConnLifetime = 30 * time.Minute
	pcfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("创建连接池失败: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	return &DB{Pool: pool, Cfg: cfg}, nil
}

// Close 关闭连接池。
func (db *DB) Close() {
	if db != nil && db.Pool != nil {
		db.Pool.Close()
	}
}

// ServerInfo 返回数据库与 TimescaleDB 版本，用于 doctor 自检。
type ServerInfo struct {
	PGVersion     string
	TSDBVersion   string
	Hypertables   []string
	ChunkInterval string
}

// Info 采集服务端信息。
func (db *DB) Info(ctx context.Context) (ServerInfo, error) {
	var info ServerInfo
	if err := db.Pool.QueryRow(ctx, `SELECT version()`).Scan(&info.PGVersion); err != nil {
		return info, err
	}
	err := db.Pool.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'`).Scan(&info.TSDBVersion)
	if err != nil {
		info.TSDBVersion = "(未安装 timescaledb 扩展)"
	}
	rows, err := db.Pool.Query(ctx, `SELECT hypertable_name FROM timescaledb_information.hypertables ORDER BY hypertable_name`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err == nil {
				info.Hypertables = append(info.Hypertables, name)
			}
		}
	}
	return info, nil
}
