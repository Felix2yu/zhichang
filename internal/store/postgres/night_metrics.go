package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// NightPhysiologyMetrics 是「按入睡时间窗关联」的生理指标白名单。
//
// 这些指标的样本全天都在产生，而一页睡眠只有一个晚上：只有按入睡窗口取，
// 才拿得到「睡着之后」的那一段（也就是 Apple 用来判断身体状态的夜间值）。
// 新增指标只要进这个表，睡眠页与查询都不用改。
var NightPhysiologyMetrics = []string{
	"heart_rate",
	"heart_rate_variability",
	"oxygen_saturation",
	"respiratory_rate",
}

// NightMetricRow 是一晚内某个生理指标的聚合。
// 一行只在「窗口内确有样本」时出现，没数据不回 NULL 行（前端据此整行不渲染）。
type NightMetricRow struct {
	// LocalDate 是睡眠日（入睡时刻的本地日期），与 SleepRow.LocalDate 同口径，可直接对齐。
	LocalDate string `json:"local_date"`
	Metric    string `json:"metric"`
	// Source 是该晚窗口内择定的唯一来源（会话来源优先，否则样本最多）。
	Source      string  `json:"source"`
	Avg         float64 `json:"avg"`
	Min         float64 `json:"min"`
	Max         float64 `json:"max"`
	SampleCount int     `json:"sample_count"`
	MinAt       string  `json:"min_at,omitempty"` // 最低值出现时刻（UTC RFC3339）
	// MinAtTZOffsetMin 是「打出最低值的那条样本」自带的偏移（分钟）。
	// 还原本地钟点必须用它，而不是服务端或会话的偏移：缺省时前端宁可不显示时刻，
	// 也不能拿 0 当 UTC 凑，那会把钟点整体偏好几个小时。
	MinAtTZOffsetMin *int    `json:"min_at_tz_offset_min,omitempty"`
	WindowStart      string  `json:"window_start"`
	WindowEnd        string  `json:"window_end"`
	WindowLength     float64 `json:"window_min"` // 窗口时长（分钟），用于判断覆盖了多少
	Unit             string  `json:"unit"`
}

// nightSessionCTE 为区间内每个睡眠日挑出「这一晚的窗口」。
//
// 两条规则：
//   - 没有 sleep_start 时退到 in_bed_start/end（导出常只给上床离床）；
//   - 同一晚多条会话（午睡 + 夜觉）时，先取入睡时刻落在本地 18:00–次日 06:00 的那条，
//     否则午睡会被当成夜觉；再取时长最长的。
//
// 入睡时刻要还原成本地墙钟再判时段：库内 timestamptz 存 UTC，偏移在 tz_offset_min 里，
// 服务端时区不参与口径，所以先加偏移再以 UTC 读墙钟。
const nightSessionCTE = `
	WITH night AS (
		SELECT DISTINCT ON (s.local_date)
			s.local_date, s.source, s.tz_offset_min,
			coalesce(s.sleep_start, s.in_bed_start) AS win_start,
			coalesce(s.sleep_end,   s.in_bed_end)   AS win_end
		FROM sleep_sessions s
		WHERE s.local_date BETWEEN $1 AND $2
		  AND coalesce(s.sleep_start, s.in_bed_start) IS NOT NULL
		  AND coalesce(s.sleep_end,   s.in_bed_end)   IS NOT NULL
		  AND coalesce(s.sleep_end, s.in_bed_end) > coalesce(s.sleep_start, s.in_bed_start)
		ORDER BY s.local_date,
			( ((coalesce(s.sleep_start, s.in_bed_start) + make_interval(mins => s.tz_offset_min))
				AT TIME ZONE 'UTC')::time >= time '18:00'
			  OR ((coalesce(s.sleep_start, s.in_bed_start) + make_interval(mins => s.tz_offset_min))
				AT TIME ZONE 'UTC')::time < time '06:00' ) DESC,
			(coalesce(s.sleep_end, s.in_bed_end) - coalesce(s.sleep_start, s.in_bed_start)) DESC,
			s.source
	)`

// SleepNightMetrics 统计区间内每一晚（按入睡时间窗）的生理指标。
//
// 关联口径与 awake_count、SleepStages 一致：样本按 start_time 落进 [sleep_start, sleep_end)
// 才算这一晚。样本自身的 local_date 是各自起始日，跨零点后与会话的睡眠日差一天，
// 所以取 {睡眠日, 次日} 两天做索引/分区裁剪，真正的取舍由时间窗决定。
//
// metrics 为空时用整个白名单；白名单外的指标一律忽略。
func (db *DB) SleepNightMetrics(ctx context.Context, from, to string, metrics []string) ([]NightMetricRow, error) {
	fd, err := parseDate(from)
	if err != nil {
		return nil, err
	}
	td, err := parseDate(to)
	if err != nil {
		return nil, err
	}
	want := filterNightMetrics(metrics)
	if len(want) == 0 {
		return make([]NightMetricRow, 0, 1), nil
	}
	// 常量时间边界：让 hypertable 按 start_time 做 chunk 裁剪，
	// 否则关联条件里的窗口是变量，每晚会全表扫一遍。
	lo := fd.Time.AddDate(0, 0, -1)
	hi := td.Time.AddDate(0, 0, 2)

	rows, err := db.Pool.Query(ctx, nightSessionCTE+`,
		pts AS (
			SELECT n.local_date, n.source AS session_source, n.win_start, n.win_end,
				p.metric, p.source, p.start_time, p.value, p.value_min, p.value_max, p.unit, p.tz_offset_min
			FROM night n
			JOIN samples p
			  ON p.metric = ANY($3)
			 AND p.start_time >= n.win_start
			 AND p.start_time <  n.win_end
			 AND p.local_date = ANY (ARRAY[n.local_date, n.local_date + 1])
			 AND p.start_time >= $4 AND p.start_time < $5
		),
		pick AS (
			-- 一晚一指标只认一个来源：会话来源优先，否则取窗口内样本数最多的那个。
			-- 不跨来源平均：手表 5 分钟一条、手机低频，平均会被采样密度带偏。
			SELECT DISTINCT ON (local_date, metric) local_date, metric, source
			FROM pts
			GROUP BY local_date, metric, source, session_source
			ORDER BY local_date, metric, (source = session_source) DESC, count(*) DESC, source
		),
		agg AS (
			SELECT local_date, metric, source,
				count(*)::int AS n,
				avg(value) AS avg_v,
				min(coalesce(value_min, value)) AS min_v,
				max(coalesce(value_max, value)) AS max_v,
				(array_agg(p.start_time ORDER BY coalesce(p.value_min, p.value), p.start_time))[1] AS min_at,
				(array_agg(p.tz_offset_min ORDER BY coalesce(p.value_min, p.value), p.start_time))[1] AS min_tz,
				max(p.unit) AS unit
			FROM pts p
			GROUP BY local_date, metric, source
		)
		SELECT a.local_date, a.metric, a.source, a.avg_v, a.min_v, a.max_v, a.n, a.min_at, a.min_tz, a.unit,
			n.win_start, n.win_end,
			EXTRACT(EPOCH FROM (n.win_end - n.win_start)) / 60
		FROM agg a
		JOIN pick k USING (local_date, metric, source)
		JOIN night n USING (local_date)
		ORDER BY a.local_date, a.metric`,
		fd, td, want, lo, hi)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]NightMetricRow, 0, 64)
	for rows.Next() {
		var r NightMetricRow
		var ld pgtype.Date
		var avg, minV, maxV, winLen *float64
		var minAt pgtype.Timestamptz
		var minTz *int32
		var ws, we time.Time
		if err := rows.Scan(&ld, &r.Metric, &r.Source, &avg, &minV, &maxV, &r.SampleCount, &minAt, &minTz, &r.Unit,
			&ws, &we, &winLen); err != nil {
			return nil, err
		}
		r.LocalDate = ld.Time.Format("2006-01-02")
		r.Avg = deref(avg)
		r.Min = deref(minV)
		r.Max = deref(maxV)
		r.WindowLength = deref(winLen)
		if minTz != nil {
			off := int(*minTz)
			r.MinAtTZOffsetMin = &off
		}
		if minAt.Valid {
			r.MinAt = minAt.Time.UTC().Format(time.RFC3339)
		}
		r.WindowStart = ws.UTC().Format(time.RFC3339)
		r.WindowEnd = we.UTC().Format(time.RFC3339)
		out = append(out, r)
	}
	return out, rows.Err()
}

// NightPoint 是单夜时间轴上的一个采样点。
type NightPoint struct {
	LocalMin float64  `json:"local_min"` // 本夜轴位置（分钟）：睡眠日本地 00:00 为 0，跨零点顺延
	Value    float64  `json:"value"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
}

// NightSeries 是某一晚窗口内某个生理指标的逐点序列。
type NightSeries struct {
	Metric string  `json:"metric"`
	Unit   string  `json:"unit"`
	Source string  `json:"source"`
	Avg    float64 `json:"avg"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	// MinLocalMin 是最低值在本夜轴上的位置，用于标出「最低心率出现在几点」。
	MinLocalMin float64      `json:"min_local_min"`
	SampleCount int          `json:"sample_count"`
	Points      []NightPoint `json:"points"`
}

// nightPhysiology 取单晚窗口内的生理指标逐点序列（供分期时间线叠加曲线）。
//
// 窗口以「本夜分钟」表达（与分期分段同一把尺子），因此样本要像分段那样用自带的
// tz_offset_min 还原墙钟，再在 {当日, 次日} 两个候选里取落进窗口的那个。
// 择一来源的规则与 SleepNightMetrics 相同。
func (db *DB) nightPhysiology(ctx context.Context, cand []pgtype.Date, winStart, winEnd float64, sessionSource string) ([]NightSeries, error) {
	rows, err := db.Pool.Query(ctx, `SELECT start_time, metric, source, value, value_min, value_max, unit, tz_offset_min
		FROM samples
		WHERE metric = ANY($1)
		  AND local_date = ANY($2)
		ORDER BY start_time`, NightPhysiologyMetrics, cand)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type pt struct {
		series NightPoint
		source string
		unit   string
	}
	byMetric := map[string][]pt{}
	for rows.Next() {
		var st time.Time
		var metric, source, unit string
		var value float64
		var vmin, vmax *float64
		var tz int
		if err := rows.Scan(&st, &metric, &source, &value, &vmin, &vmax, &unit, &tz); err != nil {
			return nil, err
		}
		wall := localMinute(st, tz)
		localMin := -1.0
		for _, m := range [2]float64{float64(wall), float64(wall) + 24*60} {
			if m >= winStart && m <= winEnd {
				localMin = m
				break
			}
		}
		if localMin < 0 {
			continue
		}
		byMetric[metric] = append(byMetric[metric], pt{
			series: NightPoint{LocalMin: localMin, Value: value, Min: vmin, Max: vmax},
			source: source,
			unit:   unit,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]NightSeries, 0, len(byMetric))
	for _, metric := range NightPhysiologyMetrics {
		pts := byMetric[metric]
		if len(pts) == 0 {
			continue
		}
		// 择一来源：会话来源优先，否则样本数最多
		counts := map[string]int{}
		for _, p := range pts {
			counts[p.source]++
		}
		pick := sessionSource
		if _, ok := counts[pick]; !ok {
			best := -1
			for src, c := range counts {
				if c > best {
					pick, best = src, c
				}
			}
		}
		s := NightSeries{Metric: metric, Source: pick, Points: make([]NightPoint, 0, len(pts))}
		var sum, low, high float64
		for _, p := range pts {
			if p.source != pick {
				continue
			}
			if s.Unit == "" {
				s.Unit = p.unit
			}
			s.Points = append(s.Points, p.series)
			sum += p.series.Value
			s.SampleCount++
			lo := deref2(p.series.Min, p.series.Value)
			hi := deref2(p.series.Max, p.series.Value)
			if s.SampleCount == 1 || lo < low {
				low = lo
				s.MinLocalMin = p.series.LocalMin
			}
			if s.SampleCount == 1 || hi > high {
				high = hi
			}
		}
		if s.SampleCount == 0 {
			continue
		}
		s.Avg = sum / float64(s.SampleCount)
		s.Min = low
		s.Max = high
		out = append(out, s)
	}
	return out, nil
}

// filterNightMetrics 把请求的指标收敛到白名单内；空请求即整个白名单。
func filterNightMetrics(metrics []string) []string {
	if len(metrics) == 0 {
		out := make([]string, len(NightPhysiologyMetrics))
		copy(out, NightPhysiologyMetrics)
		return out
	}
	out := make([]string, 0, len(metrics))
	for _, m := range metrics {
		for _, ok := range NightPhysiologyMetrics {
			if m == ok {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func deref2(v *float64, fallback float64) float64 {
	if v == nil {
		return fallback
	}
	return *v
}
