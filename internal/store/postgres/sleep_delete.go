package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SleepDeleteStats 是一次睡眠清理的范围与结果。
// Preview 与真正删除返回同一结构，便于调用方对照「会删什么」与「删了什么」。
type SleepDeleteStats struct {
	// Sessions 是 sleep_sessions 的条数。
	Sessions int64
	// Segments 是 metric='sleep_analysis' 的分期样本条数。
	Segments int64
	// Dates 是受影响的睡眠日（含只剩天级值的日期），删除后需要重算。
	Dates []string
}

// SleepDeletePreview 统计将被删除的睡眠记录，不修改任何数据。
//
// 范围按睡眠日（local_date）过滤。分期样本的 local_date 是各段自己的起始日，
// 跨零点后会落到睡眠日的次日，所以计数时把「入场会话时间窗内的分段」也算进来：
// 只按 local_date 数，预览会比真正删除少报一段凌晨的分期。
//
// 但反过来也不能见日期就删：凌晨那一段的 local_date 是次日，可能是前一晚
// （范围外、要保留的会话）的后半夜。所以只认两种归属——
// 落在被删会话窗口内的，或本地日期在范围内且不属于任何保留会话的孤儿段。
func (db *DB) SleepDeletePreview(ctx context.Context, from, to string) (SleepDeleteStats, error) {
	fd, td, err := sleepDeleteBounds(from, to)
	if err != nil {
		return SleepDeleteStats{}, err
	}
	var st SleepDeleteStats
	err = db.Pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM sleep_sessions WHERE local_date BETWEEN $1 AND $2),
			(SELECT count(*) FROM samples s
			  WHERE s.metric = $3
			    AND (
			      EXISTS (
			        SELECT 1 FROM sleep_sessions ds
			         WHERE ds.local_date BETWEEN $1 AND $2
			           AND ds.sleep_start IS NOT NULL AND ds.sleep_end IS NOT NULL
			           AND s.start_time >= ds.sleep_start AND s.start_time < ds.sleep_end)
			      OR (s.local_date BETWEEN $1 AND $2
			          AND NOT EXISTS (
			            SELECT 1 FROM sleep_sessions ss
			             WHERE ss.local_date NOT BETWEEN $1 AND $2
			               AND ss.sleep_start IS NOT NULL AND ss.sleep_end IS NOT NULL
			               AND s.start_time >= ss.sleep_start AND s.start_time < ss.sleep_end))))`,
		fd, td, SleepMetric).Scan(&st.Sessions, &st.Segments)
	if err != nil {
		return SleepDeleteStats{}, err
	}
	st.Dates, err = sleepAffectedDates(ctx, db.Pool, fd, td)
	return st, err
}

// DeleteSleep 删除指定睡眠日区间内的睡眠记录：会话与分期样本，连同这些日期的
// 天级睡眠值一起清掉，避免删完数据后趋势页还挂着一个旧数值。
//
// 删除范围与 SleepDeletePreview 一致；from/to 留空表示不设边界。
// 天级值只清不建：调用方按返回的 Dates 重算，这样 stdio 模式（没有后台
// rollup worker）也能立即看到一致的结果。
func (db *DB) DeleteSleep(ctx context.Context, from, to string) (SleepDeleteStats, error) {
	fd, td, err := sleepDeleteBounds(from, to)
	if err != nil {
		return SleepDeleteStats{}, err
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return SleepDeleteStats{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var st SleepDeleteStats
	// 先记日期再删：daily_metrics 里可能还有数据已不存在的旧日期。
	if st.Dates, err = sleepAffectedDates(ctx, tx, fd, td); err != nil {
		return SleepDeleteStats{}, err
	}

	// 先按入睡时间窗删分段：跨零点后的分段 local_date 是次日，按日期会漏。
	// 用 EXISTS 而不是 DELETE ... USING：语法与预览计数完全同构，
	// 在 TimescaleDB 的 hypertable 上也走同一条规划路径。
	tag, err := tx.Exec(ctx, `DELETE FROM samples s
		WHERE s.metric = $3
		  AND EXISTS (
		      SELECT 1 FROM sleep_sessions ss
		       WHERE ss.local_date BETWEEN $1 AND $2
		         AND ss.sleep_start IS NOT NULL AND ss.sleep_end IS NOT NULL
		         AND s.start_time >= ss.sleep_start AND s.start_time < ss.sleep_end)`, fd, td, SleepMetric)
	if err != nil {
		return SleepDeleteStats{}, err
	}
	st.Segments += tag.RowsAffected()

	// 再删按日期归属的孤儿段：本地日期在范围内，且不属于任何要保留的会话。
	// 少了后半句，删「今晚」会把「昨晚」跨零点后的分段一起带走。
	tag, err = tx.Exec(ctx, `DELETE FROM samples s
		WHERE s.metric = $3
		  AND s.local_date BETWEEN $1 AND $2
		  AND NOT EXISTS (
		      SELECT 1 FROM sleep_sessions ss
		       WHERE ss.local_date NOT BETWEEN $1 AND $2
		         AND ss.sleep_start IS NOT NULL AND ss.sleep_end IS NOT NULL
		         AND s.start_time >= ss.sleep_start AND s.start_time < ss.sleep_end)`, fd, td, SleepMetric)
	if err != nil {
		return SleepDeleteStats{}, err
	}
	st.Segments += tag.RowsAffected()

	tag, err = tx.Exec(ctx, `DELETE FROM sleep_sessions WHERE local_date BETWEEN $1 AND $2`, fd, td)
	if err != nil {
		return SleepDeleteStats{}, err
	}
	st.Sessions = tag.RowsAffected()

	if len(st.Dates) > 0 {
		dates := toDateArray(st.Dates)
		for _, q := range []string{
			`DELETE FROM daily_metrics WHERE metric = $2 AND local_date = ANY($1)`,
			`DELETE FROM daily_metrics_day WHERE metric = $2 AND local_date = ANY($1)`,
			`DELETE FROM rollup_queue WHERE metric = $2 AND local_date = ANY($1)`,
		} {
			if _, err := tx.Exec(ctx, q, dates, SleepMetric); err != nil {
				return SleepDeleteStats{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SleepDeleteStats{}, err
	}
	return st, nil
}

// sleepAffectedDates 汇总范围内所有需要重算的睡眠日：
// 会话、分期样本、以及可能残留的天级值（数据已删但聚合还在的日期）。
func sleepAffectedDates(ctx context.Context, q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, fd, td pgtype.Date) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT d FROM (
			SELECT local_date AS d FROM sleep_sessions WHERE local_date BETWEEN $1 AND $2
			UNION
			SELECT local_date AS d FROM samples WHERE metric = $3 AND local_date BETWEEN $1 AND $2
			UNION
			SELECT local_date AS d FROM daily_metrics WHERE metric = $3 AND local_date BETWEEN $1 AND $2
			UNION
			SELECT local_date AS d FROM daily_metrics_day WHERE metric = $3 AND local_date BETWEEN $1 AND $2
		) u ORDER BY d`, fd, td, SleepMetric)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var d pgtype.Date
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		if d.Valid {
			out = append(out, d.Time.Format("2006-01-02"))
		}
	}
	return out, rows.Err()
}

// sleepDeleteBounds 把可选区间换成 SQL 边界；留空表示不设该端边界。
func sleepDeleteBounds(from, to string) (pgtype.Date, pgtype.Date, error) {
	first := pgtype.Date{Time: time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}
	last := pgtype.Date{Time: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), Valid: true}
	var err error
	if from != "" {
		if first, err = parseDate(from); err != nil {
			return pgtype.Date{}, pgtype.Date{}, err
		}
	}
	if to != "" {
		if last, err = parseDate(to); err != nil {
			return pgtype.Date{}, pgtype.Date{}, err
		}
	}
	if last.Time.Before(first.Time) {
		return pgtype.Date{}, pgtype.Date{}, fmt.Errorf("结束日期早于起始日期")
	}
	return first, last, nil
}
