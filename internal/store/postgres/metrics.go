package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// SeedMetrics 写入缺失的指标定义，已存在的（用户自定义）保持不动。
func (db *DB) SeedMetrics(ctx context.Context, defs []metric.Def) (int, error) {
	const q = `INSERT INTO metric_defs
		(metric, display_name_zh, kind, day_agg, cross_source_agg, unit, preferred_unit, value_field, chart, enabled, sort_order)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (metric) DO NOTHING`
	n := 0
	for _, d := range defs {
		tag, err := db.Pool.Exec(ctx, q, d.Metric, d.DisplayNameZH, string(d.Kind), string(d.DayAgg),
			string(d.CrossSourceAgg), d.Unit, d.PreferredUnit, d.ValueField, d.Chart, d.Enabled, d.SortOrder)
		if err != nil {
			return n, fmt.Errorf("写入指标 %s 失败: %w", d.Metric, err)
		}
		if tag.RowsAffected() > 0 {
			n++
		}
	}
	return n, nil
}

// LoadMetrics 读取指标字典；表为空时回落到内置默认值。
func (db *DB) LoadMetrics(ctx context.Context) (*metric.Registry, bool, error) {
	const q = `SELECT metric, display_name_zh, kind, day_agg, cross_source_agg,
		COALESCE(unit,''), COALESCE(preferred_unit,''), value_field, chart, enabled, sort_order, updated_at
		FROM metric_defs`
	rows, err := db.Pool.Query(ctx, q)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var defs []metric.Def
	for rows.Next() {
		var (
			d                                        metric.Def
			kind, dayAgg, crossAgg, chart, unit, punit string
			updated                                  time.Time
		)
		if err := rows.Scan(&d.Metric, &d.DisplayNameZH, &kind, &dayAgg, &crossAgg,
			&unit, &punit, &d.ValueField, &chart, &d.Enabled, &d.SortOrder, &updated); err != nil {
			return nil, false, err
		}
		d.Kind, d.DayAgg, d.CrossSourceAgg = metric.Kind(kind), metric.DayAgg(dayAgg), metric.CrossAgg(crossAgg)
		d.Chart, d.Unit, d.PreferredUnit, d.UpdatedAt = chart, unit, punit, updated
		defs = append(defs, d)
	}
	if rows.Err() != nil {
		return nil, false, rows.Err()
	}
	if len(defs) == 0 {
		return metric.New(metric.Defaults()), false, nil
	}
	return metric.New(defs), true, nil
}
