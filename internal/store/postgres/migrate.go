package postgres

import (
	"context"
	"fmt"

	"github.com/Felix2yu/zhichang/migrations"
)

// Migrate 按文件名顺序执行未应用的迁移，每个文件在单事务内执行。
func (db *DB) Migrate(ctx context.Context) ([]string, error) {
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return nil, fmt.Errorf("创建 schema_migrations 失败: %w", err)
	}

	files, err := migrations.Files()
	if err != nil {
		return nil, err
	}
	var applied []string
	for _, f := range files {
		var exists bool
		if err := db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, f.Version).Scan(&exists); err != nil {
			return applied, err
		}
		if exists {
			continue
		}
		if err := db.execScript(ctx, f.SQL); err != nil {
			return applied, fmt.Errorf("执行迁移 %s 失败: %w", f.Version, err)
		}
		if _, err := db.Pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, f.Version); err != nil {
			return applied, err
		}
		applied = append(applied, f.Version)
	}
	return applied, nil
}

// execScript 用简单协议执行整份脚本（支持多语句），包在单事务内。
func (db *DB) execScript(ctx context.Context, script string) error {
	c, err := db.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer c.Release()

	pc := c.Conn().PgConn()
	if _, err := pc.Exec(ctx, "BEGIN").ReadAll(); err != nil {
		return err
	}
	if _, err := pc.Exec(ctx, script).ReadAll(); err != nil {
		_, _ = pc.Exec(ctx, "ROLLBACK").ReadAll()
		return err
	}
	if _, err := pc.Exec(ctx, "COMMIT").ReadAll(); err != nil {
		return err
	}
	return nil
}
