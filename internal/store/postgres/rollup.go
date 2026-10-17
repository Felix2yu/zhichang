package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// Rollup 负责天级聚合的增量重算。
// 口径：累计量日内 sum、跨来源取 max；瞬时量日内 mean、跨来源按样本数加权 mean。
// 分桶一律按 local_date（记录自带本地日期），不依赖数据库会话时区。
type Rollup struct {
	db  *DB
	reg *metric.Registry
}

// NewRollup 构造聚合服务。
func NewRollup(db *DB, reg *metric.Registry) *Rollup {
	return &Rollup{db: db, reg: reg}
}

// Run 后台消费重算队列，直到 ctx 取消。
func (r *Rollup) Run(ctx context.Context, workers int, idle time.Duration) {
	if workers < 1 {
		workers = 1
	}
	if idle <= 0 {
		idle = 2 * time.Second
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				n, err := r.RecomputePending(ctx, 500)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					slog.Warn("聚合重算失败", "error", err)
				}
				if n > 0 {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(idle):
				}
			}
		}()
	}
	wg.Wait()
}

// RecomputePending 取出一批待重算项并重算，返回处理条数。
func (r *Rollup) RecomputePending(ctx context.Context, limit int) (int, error) {
	rows, err := r.db.Pool.Query(ctx, `WITH picked AS (
			SELECT local_date, metric FROM rollup_queue ORDER BY requested_at LIMIT $1 FOR UPDATE SKIP LOCKED
		)
		DELETE FROM rollup_queue q USING picked p
		WHERE q.local_date = p.local_date AND q.metric = p.metric
		RETURNING q.local_date, q.metric`, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	byMetric := map[string][]string{}
	n := 0
	for rows.Next() {
		var d pgtype.Date
		var m string
		if err := rows.Scan(&d, &m); err != nil {
			return 0, err
		}
		if !d.Valid {
			continue
		}
		byMetric[m] = append(byMetric[m], d.Time.Format("2006-01-02"))
		n++
	}
	if rows.Err() != nil {
		return 0, rows.Err()
	}
	for m, dates := range byMetric {
		if err := r.Recompute(ctx, dates, []string{m}); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Recompute 重算指定 (日期 × 指标) 的天级聚合。
func (r *Rollup) Recompute(ctx context.Context, dates []string, metrics []string) error {
	if len(dates) == 0 || len(metrics) == 0 {
		return nil
	}
	d := toDateArray(dates)
	known := make([]string, 0, len(metrics))
	known = append(known, metrics...)

	// 睡眠不产生 samples（会话形态只进 sleep_sessions），它的中间量单独从会话表取。
	// 少了这一条，字典里的「睡眠时长」卡片会永远是空的，而睡眠页明明有几百晚数据——
	// 两边看着都像坏了，但谁都没报错。
	sampleMetrics := make([]string, 0, len(known))
	sleepInBatch := false
	for _, m := range known {
		if m == SleepMetric {
			sleepInBatch = true
			continue
		}
		sampleMetrics = append(sampleMetrics, m)
	}

	// 1) 每 (日期 × 指标 × 来源) 的中间量
	if len(sampleMetrics) > 0 {
		if _, err := r.db.Pool.Exec(ctx, `DELETE FROM daily_metrics WHERE local_date = ANY($1) AND metric = ANY($2)`, d, sampleMetrics); err != nil {
			return err
		}
		if _, err := r.db.Pool.Exec(ctx, `INSERT INTO daily_metrics
			(local_date, metric, source, sample_count, sum_value, mean_value, min_value, max_value, first_at, last_at)
			SELECT local_date, metric, source, count(*), sum(value), avg(value), min(value), max(value), min(start_time), max(start_time)
			FROM samples
			WHERE local_date = ANY($1) AND metric = ANY($2)
			GROUP BY local_date, metric, source`, d, sampleMetrics); err != nil {
			return err
		}
	}
	if sleepInBatch {
		if err := r.recomputeSleepSources(ctx, d); err != nil {
			return err
		}
	}

	// 2) 按日内口径分组，收敛到每 (日期 × 指标)
	groups := map[metric.DayAgg][]string{}
	for _, m := range known {
		def, ok := r.reg.Get(m)
		if !ok {
			def = metric.Def{Metric: m, DayAgg: metric.DayAggSum, CrossSourceAgg: metric.CrossAggMax}
		}
		groups[def.DayAgg] = append(groups[def.DayAgg], m)
	}

	for agg, ms := range groups {
		if agg == metric.DayAggLast {
			if err := r.recomputeLast(ctx, d, ms); err != nil {
				return err
			}
			continue
		}
		expr := crossExprFor(agg, ms, r.reg)
		if _, err := r.db.Pool.Exec(ctx, `DELETE FROM daily_metrics_day WHERE local_date = ANY($1) AND metric = ANY($2)`, d, ms); err != nil {
			return err
		}
		q := fmt.Sprintf(`INSERT INTO daily_metrics_day (local_date, metric, value, sample_count, source_count, sources)
			SELECT d.local_date, d.metric, %s, sum(d.sample_count), count(*), array_agg(DISTINCT d.source)
			FROM daily_metrics d
			WHERE d.local_date = ANY($1) AND d.metric = ANY($2)
			GROUP BY d.local_date, d.metric`, expr)
		if _, err := r.db.Pool.Exec(ctx, q, d, ms); err != nil {
			return err
		}
	}
	return nil
}

// SleepMetric 是唯一一个「不产生 samples 却有天级值」的指标：
// 睡眠以会话形态入库，只落在 sleep_sessions。
const SleepMetric = "sleep_analysis"

// recomputeSleepSources 从睡眠会话表算每 (日期 × 来源) 的睡眠总量。
//
// 一晚归属到哪个日期由入库侧按「本夜」（18:00 分界）决定，所以一天里通常正好一晚；
// 真出现分段睡眠或午睡 + 夜觉时，按累计量口径在同来源内求和。
// 跨来源的收敛留给第二步（取各来源总量的 max），与步数/能量走同一套规则——
// 手表报 7.6 小时、第三方 App 报 4.5 小时时，求和会得到一个谁都没睡出来的数。
func (r *Rollup) recomputeSleepSources(ctx context.Context, d []pgtype.Date) error {
	if _, err := r.db.Pool.Exec(ctx,
		`DELETE FROM daily_metrics WHERE local_date = ANY($1) AND metric = $2`, d, SleepMetric); err != nil {
		return err
	}
	_, err := r.db.Pool.Exec(ctx, `INSERT INTO daily_metrics
		(local_date, metric, source, sample_count, sum_value, mean_value, min_value, max_value, first_at, last_at)
		SELECT local_date, $2, source, count(*), sum(total_sleep_hr), avg(total_sleep_hr),
			min(total_sleep_hr), max(total_sleep_hr), min(sleep_start), max(sleep_end)
		FROM sleep_sessions
		WHERE local_date = ANY($1) AND total_sleep_hr IS NOT NULL
		GROUP BY local_date, source`, d, SleepMetric)
	return err
}

// recomputeLast 处理“取当日最后一次测量”的指标（身高、VO2max 等）。
//
// 来源清单与来源数必须去重，但不能用 array_agg(DISTINCT ...) OVER (...)：
// PostgreSQL 明确不支持窗口函数里的 DISTINCT（SQLSTATE 0A000），
// 那样写会让整条语句在任何库上都直接失败。改成先按 (日期 × 指标) 聚合出
// 去重来源，再与「最后一次」那一行拼起来，口径与逐样本算完全一致。
func (r *Rollup) recomputeLast(ctx context.Context, d []pgtype.Date, ms []string) error {
	if _, err := r.db.Pool.Exec(ctx, `DELETE FROM daily_metrics_day WHERE local_date = ANY($1) AND metric = ANY($2)`, d, ms); err != nil {
		return err
	}
	const q = `INSERT INTO daily_metrics_day (local_date, metric, value, sample_count, source_count, sources)
		SELECT t.local_date, t.metric, t.value, t.cnt, sc.src_cnt, sc.sources FROM (
			SELECT s.local_date, s.metric, s.value,
				count(*) OVER (PARTITION BY s.local_date, s.metric) AS cnt,
				row_number() OVER (PARTITION BY s.local_date, s.metric ORDER BY s.start_time DESC, s.source) AS rn
			FROM samples s
			WHERE s.local_date = ANY($1) AND s.metric = ANY($2)
		) t
		JOIN (
			SELECT s.local_date, s.metric,
				count(DISTINCT s.source) AS src_cnt,
				array_agg(DISTINCT s.source ORDER BY s.source) AS sources
			FROM samples s
			WHERE s.local_date = ANY($1) AND s.metric = ANY($2)
			GROUP BY s.local_date, s.metric
		) sc ON sc.local_date = t.local_date AND sc.metric = t.metric
		WHERE t.rn = 1`
	_, err := r.db.Pool.Exec(ctx, q, d, ms)
	return err
}

// RecomputeRange 重建指定日期区间内全部指标的聚合（供运维命令使用）。
func (r *Rollup) RecomputeRange(ctx context.Context, from, to string) (int, error) {
	dates, err := dateRange(from, to)
	if err != nil {
		return 0, err
	}
	reg := r.reg.All()
	metrics := make([]string, 0, len(reg))
	for _, d := range reg {
		metrics = append(metrics, d.Metric)
	}
	return len(dates), r.Recompute(ctx, dates, metrics)
}

// crossExprFor 按日内口径与跨来源口径生成聚合表达式（白名单，不含用户输入）。
func crossExprFor(agg metric.DayAgg, ms []string, reg *metric.Registry) string {
	// 同一分组内的跨来源口径取首个指标的声明（同口径分组内一致）。
	cross := metric.CrossAggMax
	if len(ms) > 0 {
		if def, ok := reg.Get(ms[0]); ok {
			cross = def.CrossSourceAgg
		}
	}
	switch agg {
	case metric.DayAggSum:
		switch cross {
		case metric.CrossAggSum:
			return "sum(d.sum_value)"
		case metric.CrossAggMean:
			return "sum(d.sum_value) / nullif(sum(d.sample_count), 0)"
		default: // max：消除 iPhone / Apple Watch 双计
			return "max(d.sum_value)"
		}
	case metric.DayAggMean:
		switch cross {
		case metric.CrossAggMax:
			return "max(d.mean_value)"
		default: // 按样本数加权，等价于全样本均值
			return "sum(d.sum_value) / nullif(sum(d.sample_count), 0)"
		}
	case metric.DayAggMax:
		switch cross {
		case metric.CrossAggMean:
			return "avg(d.max_value)"
		default:
			return "max(d.max_value)"
		}
	case metric.DayAggMin:
		switch cross {
		case metric.CrossAggMean:
			return "avg(d.min_value)"
		default:
			return "min(d.min_value)"
		}
	default: // count
		switch cross {
		case metric.CrossAggSum:
			return "sum(d.sample_count)::float8"
		case metric.CrossAggMean:
			return "avg(d.sample_count)::float8"
		default:
			return "max(d.sample_count)::float8"
		}
	}
}

func toDateArray(dates []string) []pgtype.Date {
	out := make([]pgtype.Date, 0, len(dates))
	for _, s := range dates {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			continue
		}
		out = append(out, pgtype.Date{Time: t, Valid: true})
	}
	return out
}

func dateRange(from, to string) ([]string, error) {
	f, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, fmt.Errorf("起始日期非法: %w", err)
	}
	t, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, fmt.Errorf("结束日期非法: %w", err)
	}
	if t.Before(f) {
		return nil, fmt.Errorf("结束日期早于起始日期")
	}
	var out []string
	for c := f; !c.After(t); c = c.AddDate(0, 0, 1) {
		out = append(out, c.Format("2006-01-02"))
	}
	return out, nil
}
