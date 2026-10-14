package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// DailyPoint 是天级聚合的一行。
type DailyPoint struct {
	Date        string   `json:"date"`
	Metric      string   `json:"metric"`
	Value       float64  `json:"value"`
	SampleCount int      `json:"sample_count"`
	SourceCount int      `json:"source_count"`
	Sources     []string `json:"sources"`
}

// DailySeries 读取指定指标在日期区间内的天级序列。
func (db *DB) DailySeries(ctx context.Context, metrics []string, from, to string) ([]DailyPoint, error) {
	if len(metrics) == 0 {
		return []DailyPoint{}, nil
	}
	fd, err := parseDate(from)
	if err != nil {
		return nil, err
	}
	td, err := parseDate(to)
	if err != nil {
		return nil, err
	}
	rows, err := db.Pool.Query(ctx, `SELECT local_date, metric, value, sample_count, source_count, sources
		FROM daily_metrics_day
		WHERE metric = ANY($1) AND local_date BETWEEN $2 AND $3
		ORDER BY metric, local_date`, metrics, fd, td)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDaily(rows)
}

// SummaryItem 是概览卡片的一项（含前一日对比）。
type SummaryItem struct {
	Metric      string   `json:"metric"`
	DisplayName string   `json:"display_name"`
	Unit        string   `json:"unit"`
	Value       *float64 `json:"value"`
	PrevValue   *float64 `json:"prev_value"`
	Delta       *float64 `json:"delta"`
	DeltaPct    *float64 `json:"delta_pct"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

// Summary 返回指定日期（及前一日）的概览。
func (db *DB) Summary(ctx context.Context, date string, defs map[string]struct{}) ([]SummaryItem, error) {
	d, err := parseDate(date)
	if err != nil {
		return nil, err
	}
	prev := pgtype.Date{Time: d.Time.AddDate(0, 0, -1), Valid: true}
	rows, err := db.Pool.Query(ctx, `SELECT local_date, metric, value, updated_at
		FROM daily_metrics_day
		WHERE local_date IN ($1, $2)
		ORDER BY local_date`, d, prev)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type cell struct {
		value float64
		at    time.Time
	}
	cur := map[string]cell{}
	before := map[string]cell{}
	for rows.Next() {
		var ld pgtype.Date
		var m string
		var v float64
		var at time.Time
		if err := rows.Scan(&ld, &m, &v, &at); err != nil {
			return nil, err
		}
		if len(defs) > 0 {
			if _, ok := defs[m]; !ok {
				continue
			}
		}
		if ld.Time.Equal(d.Time) {
			cur[m] = cell{value: v, at: at}
		} else {
			before[m] = cell{value: v, at: at}
		}
	}
	if rows.Err() != nil {
		return nil, err
	}

	out := make([]SummaryItem, 0, len(cur))
	for m, c := range cur {
		item := SummaryItem{Metric: m}
		v := c.value
		item.Value = &v
		item.UpdatedAt = c.at.UTC().Format(time.RFC3339)
		if p, ok := before[m]; ok {
			pv := p.value
			item.PrevValue = &pv
			delta := v - pv
			item.Delta = &delta
			if pv != 0 {
				pct := delta / pv * 100
				item.DeltaPct = &pct
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// SampleRow 是明细行。
type SampleRow struct {
	StartTime string   `json:"start_time"`
	EndTime   string   `json:"end_time"`
	LocalDate string   `json:"local_date"`
	Metric    string   `json:"metric"`
	Source    string   `json:"source"`
	Value     float64  `json:"value"`
	Unit      string   `json:"unit"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
}

// Samples 读取明细（排障用）。
func (db *DB) Samples(ctx context.Context, metric, from, to string, limit int) ([]SampleRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	fd, err := parseDate(from)
	if err != nil {
		return nil, err
	}
	td, err := parseDate(to)
	if err != nil {
		return nil, err
	}
	rows, err := db.Pool.Query(ctx, `SELECT start_time, end_time, local_date, metric, source, value, unit, value_min, value_max
		FROM samples
		WHERE metric = $1 AND local_date BETWEEN $2 AND $3
		ORDER BY start_time DESC LIMIT $4`, metric, fd, td, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SampleRow, 0, 64)
	for rows.Next() {
		var r SampleRow
		var st, et time.Time
		var ld pgtype.Date
		if err := rows.Scan(&st, &et, &ld, &r.Metric, &r.Source, &r.Value, &r.Unit, &r.Min, &r.Max); err != nil {
			return nil, err
		}
		r.StartTime = st.UTC().Format(time.RFC3339)
		r.EndTime = et.UTC().Format(time.RFC3339)
		r.LocalDate = ld.Time.Format("2006-01-02")
		out = append(out, r)
	}
	return out, rows.Err()
}

// SleepRow 是睡眠记录。
// 除原始入睡/分期数据外，另带 tz_offset_min（本地入睡时刻要靠它还原）与
// awake_count（夜间清醒段数，仅当导出含睡眠分段数据时才有值，否则为 nil）。
type SleepRow struct {
	LocalDate  string  `json:"local_date"`
	Source     string  `json:"source"`
	SleepStart *string `json:"sleep_start,omitempty"`
	SleepEnd   *string `json:"sleep_end,omitempty"`
	// 上床/离床时刻：与 sleep_start 相减即「入睡潜伏期」（躺下到真正睡着）。
	InBedStart   *string  `json:"in_bed_start,omitempty"`
	InBedEnd     *string  `json:"in_bed_end,omitempty"`
	TotalSleepHr *float64 `json:"total_sleep_hr,omitempty"`
	AsleepHr     *float64 `json:"asleep_hr,omitempty"`
	CoreHr       *float64 `json:"core_hr,omitempty"`
	DeepHr       *float64 `json:"deep_hr,omitempty"`
	RemHr        *float64 `json:"rem_hr,omitempty"`
	AwakeHr      *float64 `json:"awake_hr,omitempty"`
	InBedHr      *float64 `json:"in_bed_hr,omitempty"`
	TZOffsetMin  int      `json:"tz_offset_min"`
	AwakeCount   *int     `json:"awake_count,omitempty"`
}

// SleepRange 读取区间内的睡眠记录。
func (db *DB) SleepRange(ctx context.Context, from, to string) ([]SleepRow, error) {
	fd, err := parseDate(from)
	if err != nil {
		return nil, err
	}
	td, err := parseDate(to)
	if err != nil {
		return nil, err
	}
	// awake_count 用「入床时间窗内的睡眠分段样本」统计：
	// 分段样本的 local_date 落在各段自身的起始日，跨零点后会与睡眠会话的 local_date
	// （以 sleepStart 为准）错开一天，因此按时间窗而不是按 local_date 关联。
	rows, err := db.Pool.Query(ctx, `SELECT local_date, source, sleep_start, sleep_end, in_bed_start, in_bed_end,
			s.total_sleep_hr, s.asleep_hr, s.core_hr, s.deep_hr, s.rem_hr, s.awake_hr, s.in_bed_hr, s.tz_offset_min,
			(SELECT CASE WHEN count(*) = 0 THEN NULL
			             ELSE count(*) FILTER (WHERE lower(coalesce(p.meta->>'stage', '')) LIKE '%awake%'
			                                      OR coalesce(p.meta->>'stage', '') LIKE '%醒%') END
			   FROM samples p
			  WHERE p.metric = 'sleep_analysis'
			    AND s.sleep_start IS NOT NULL AND s.sleep_end IS NOT NULL
			    AND p.start_time >= s.sleep_start AND p.start_time < s.sleep_end)
		FROM sleep_sessions s WHERE s.local_date BETWEEN $1 AND $2 ORDER BY s.local_date`, fd, td)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SleepRow, 0, 64)
	for rows.Next() {
		var r SleepRow
		var ld pgtype.Date
		var ss, se, bs, be *time.Time
		if err := rows.Scan(&ld, &r.Source, &ss, &se, &bs, &be, &r.TotalSleepHr, &r.AsleepHr, &r.CoreHr, &r.DeepHr, &r.RemHr, &r.AwakeHr, &r.InBedHr, &r.TZOffsetMin, &r.AwakeCount); err != nil {
			return nil, err
		}
		r.LocalDate = ld.Time.Format("2006-01-02")
		if ss != nil {
			s := ss.UTC().Format(time.RFC3339)
			r.SleepStart = &s
		}
		if se != nil {
			s := se.UTC().Format(time.RFC3339)
			r.SleepEnd = &s
		}
		if bs != nil {
			s := bs.UTC().Format(time.RFC3339)
			r.InBedStart = &s
		}
		if be != nil {
			s := be.UTC().Format(time.RFC3339)
			r.InBedEnd = &s
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SleepStageSeg 是一段睡眠分期（hypnogram 的一段）。
type SleepStageSeg struct {
	Start string `json:"start"` // UTC RFC3339
	End   string `json:"end"`   // UTC RFC3339
	Stage string `json:"stage"` // 归一化后的阶段键：awake/rem/core/deep/asleep/inbed/other
	Raw   string `json:"raw"`   // 原始 stage 文本，便于排查上游写法
	// Source 是该段所属来源（择优后通常与 SleepStages.Source 一致）。
	Source string `json:"source"`
	// LocalMin 是该段起点在「本夜时间轴」上的位置（分钟）：
	// 以睡眠日 local_date 的本地 00:00 为 0，跨零点后顺延，凌晨 03:00 即 180。
	LocalMin int `json:"local_min"`
	// DurationMin 为该段时长（分钟）。
	DurationMin float64 `json:"duration_min"`
}

// SleepStages 是一晚的睡眠分期时间线。
type SleepStages struct {
	LocalDate   string          `json:"local_date"`
	Source      string          `json:"source"`
	TZOffsetMin int             `json:"tz_offset_min"`
	HasSession  bool            `json:"has_session"`
	SleepStart  *string         `json:"sleep_start,omitempty"`
	SleepEnd    *string         `json:"sleep_end,omitempty"`
	Segments    []SleepStageSeg `json:"segments"`
	// Totals 是各阶段的合计小时数，键与 Segments[].Stage 一致。
	Totals map[string]float64 `json:"totals"`
}

// normalizeSleepStage 把上游各版本的 stage 写法归一化。
// 顺序有讲究：新版 HealthKit 的 AsleepCore / AsleepDeep / AsleepREM 同时含
// "asleep" 与具体阶段，必须先判具体阶段，否则会被归成笼统的 asleep。
func normalizeSleepStage(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case s == "":
		return "other"
	case strings.Contains(s, "inbed"), strings.Contains(s, "in bed"), strings.Contains(s, "在床"):
		return "inbed"
	case strings.Contains(s, "awake"), strings.Contains(s, "醒"):
		return "awake"
	case strings.Contains(s, "rem"):
		return "rem"
	case strings.Contains(s, "deep"), strings.Contains(s, "深"):
		return "deep"
	case strings.Contains(s, "core"), strings.Contains(s, "核心"):
		return "core"
	case strings.Contains(s, "asleep"), strings.Contains(s, "睡眠"):
		return "asleep"
	default:
		return "other"
	}
}

// SleepStages 读取某一晚的分期分段序列（时间线）。
//
// 分期数据落在 samples（metric='sleep_analysis'，阶段在 meta.stage），
// 而睡眠会话在 sleep_sessions，两者用「入睡时间窗」关联：分期样本的 local_date
// 是各段自身的起始日，跨零点后与会话的 local_date 并不相同。
//
// 夜间窗口的取法：
//   - 有会话时用 [sleep_start, sleep_end]，精确到这一觉；
//   - 没有会话（导出只给了分期）时退化为「入睡日 18:00 → 次日 18:00」。
//
// 两者都用样本自带的 tz_offset_min 把 UTC 换算成本地墙钟后再定位，
// 因此不依赖服务端时区。
func (db *DB) SleepStages(ctx context.Context, date string) (*SleepStages, error) {
	d, err := parseDate(date)
	if err != nil {
		return nil, err
	}
	out := &SleepStages{
		LocalDate:   date,
		Segments:    make([]SleepStageSeg, 0, 64),
		Totals:      make(map[string]float64, 6),
		TZOffsetMin: 0,
	}

	// 会话窗口：取时长最长的那条（信息最完整）。
	var sessStart, sessEnd *time.Time
	sessRows, err := db.Pool.Query(ctx, `SELECT sleep_start, sleep_end, source, tz_offset_min
		FROM sleep_sessions
		WHERE local_date = $1 AND sleep_start IS NOT NULL AND sleep_end IS NOT NULL
		ORDER BY (sleep_end - sleep_start) DESC LIMIT 1`, d)
	if err != nil {
		return nil, err
	}
	for sessRows.Next() {
		var st, en time.Time
		var src string
		if err := sessRows.Scan(&st, &en, &src, &out.TZOffsetMin); err != nil {
			sessRows.Close()
			return nil, err
		}
		stv, env := st.UTC(), en.UTC()
		sessStart, sessEnd = &stv, &env
		out.Source = src
		out.HasSession = true
	}
	sessRows.Close()
	if err := sessRows.Err(); err != nil {
		return nil, err
	}
	if sessStart != nil {
		s := sessStart.Format(time.RFC3339)
		e := sessEnd.Format(time.RFC3339)
		out.SleepStart, out.SleepEnd = &s, &e
	}

	// 夜间窗口（分钟）：以睡眠日本地 00:00 为 0，横轴一路顺延到次日中午过后。
	// 有会话时按入睡时刻的本地墙钟定位；无会话时用 18:00 → 次日 18:00。
	const eveningAnchor = 18 * 60
	winStart, winEnd := eveningAnchor, eveningAnchor+24*60
	if sessStart != nil {
		if m := localMinute(*sessStart, out.TZOffsetMin); m >= 0 && m < winStart {
			winStart = m // 午睡等异常：如实以入睡时刻为起点
		}
		if m := localMinute(*sessEnd, out.TZOffsetMin); m >= 0 {
			if m <= winStart {
				m += 24 * 60 // 跨零点
			}
			if m > winStart {
				winEnd = m
			}
		}
	}

	// 候选分段：睡眠日与次日都可能落样本。
	nd := pgtype.Date{Time: d.Time.AddDate(0, 0, 1), Valid: true}
	segRows, err := db.Pool.Query(ctx, `SELECT start_time, end_time, source, tz_offset_min,
			COALESCE(meta->>'stage', '')
		FROM samples
		WHERE metric = 'sleep_analysis'
		  AND local_date = ANY($1)
		  AND COALESCE(meta->>'stage', '') <> ''
		ORDER BY start_time`, []pgtype.Date{d, nd})
	if err != nil {
		return nil, err
	}
	defer segRows.Close()

	type cand struct {
		SleepStageSeg
		source string
	}
	cands := make([]cand, 0, 256)
	coverBySource := map[string]float64{}
	for segRows.Next() {
		var st, en time.Time
		var src, raw string
		var tz int
		if err := segRows.Scan(&st, &en, &src, &tz, &raw); err != nil {
			return nil, err
		}
		// 墙钟分钟要落到夜间窗口内：凌晨的 03:00 在本夜轴上是 180+1440，
		// 因此两个候选值（当日 / 次日）里取落在窗口内的那个。
		wall := localMinute(st, tz)
		startMin := -1
		for _, m := range [2]int{wall, wall + 24*60} {
			if m >= winStart && m <= winEnd {
				startMin = m
				break
			}
		}
		if startMin < 0 {
			continue
		}
		durMin := en.Sub(st).Minutes()
		if durMin <= 0 {
			continue
		}
		// 超出窗口的部分裁掉（无会话时窗口是粗粒度的，可能切到相邻的觉）
		if tail := float64(winEnd-startMin) - durMin; tail < 0 {
			durMin += tail
		}
		if durMin <= 0 {
			continue
		}
		seg := SleepStageSeg{
			Start:       st.UTC().Format(time.RFC3339),
			End:         en.UTC().Format(time.RFC3339),
			Stage:       normalizeSleepStage(raw),
			Raw:         raw,
			Source:      src,
			LocalMin:    startMin,
			DurationMin: durMin,
		}
		coverBySource[src] += durMin
		cands = append(cands, cand{SleepStageSeg: seg, source: src})
	}
	if err := segRows.Err(); err != nil {
		return nil, err
	}

	// 多来源择优：会话自身的来源优先（手表分期比 iPhone 细，且与会话口径一致），
	// 否则取覆盖率最高的来源。
	pick := out.Source
	if _, ok := coverBySource[pick]; !ok {
		pick = ""
		best := -1.0
		for src, cov := range coverBySource {
			if cov > best {
				pick, best = src, cov
			}
		}
	}
	if pick != "" {
		out.Source = pick
	}

	for _, c := range cands {
		if c.source != pick {
			continue
		}
		// 在床不是分期阶段，Apple 的时间线也不画它：只统计不渲染。
		out.Segments = append(out.Segments, c.SleepStageSeg)
		out.Totals[c.Stage] += c.DurationMin / 60
	}
	return out, nil
}

// LatestSleepStageDate 返回最近一晚有分期的睡眠日（以入睡时刻的本地日期为准）。
// 没有分期数据时返回空串。
func (db *DB) LatestSleepStageDate(ctx context.Context) (string, error) {
	var d pgtype.Date
	err := db.Pool.QueryRow(ctx, `SELECT local_date FROM samples
		WHERE metric = 'sleep_analysis' AND COALESCE(meta->>'stage', '') <> ''
		ORDER BY local_date DESC LIMIT 1`).Scan(&d)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !d.Valid {
		return "", nil
	}
	return d.Time.Format("2006-01-02"), nil
}

// localMinute 把 UTC 时刻换算成「本地日期的 00:00 起算的分钟数」。
func localMinute(t time.Time, tzOffsetMin int) int {
	lt := t.UTC().Add(time.Duration(tzOffsetMin) * time.Minute)
	return lt.Hour()*60 + lt.Minute()
}

// WorkoutRow 是运动记录。
type WorkoutRow struct {
	LocalDate     string   `json:"local_date"`
	Name          string   `json:"name"`
	Source        string   `json:"source"`
	Start         string   `json:"start"`
	End           string   `json:"end"`
	DurationMin   float64  `json:"duration_min"`
	TotalEnergy   *float64 `json:"total_energy,omitempty"`
	EnergyUnit    string   `json:"energy_unit,omitempty"`
	TotalDistance *float64 `json:"total_distance,omitempty"`
	DistanceUnit  string   `json:"distance_unit,omitempty"`
}

// WorkoutRange 读取区间内的运动记录。
func (db *DB) WorkoutRange(ctx context.Context, from, to string) ([]WorkoutRow, error) {
	fd, err := parseDate(from)
	if err != nil {
		return nil, err
	}
	td, err := parseDate(to)
	if err != nil {
		return nil, err
	}
	rows, err := db.Pool.Query(ctx, `SELECT local_date, name, source, start_time, end_time, duration_min, total_energy, COALESCE(energy_unit,''), total_distance, COALESCE(distance_unit,'')
		FROM workouts WHERE local_date BETWEEN $1 AND $2 ORDER BY start_time DESC`, fd, td)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]WorkoutRow, 0, 64)
	for rows.Next() {
		var r WorkoutRow
		var ld pgtype.Date
		var st, et time.Time
		if err := rows.Scan(&ld, &r.Name, &r.Source, &st, &et, &r.DurationMin, &r.TotalEnergy, &r.EnergyUnit, &r.TotalDistance, &r.DistanceUnit); err != nil {
			return nil, err
		}
		r.LocalDate = ld.Time.Format("2006-01-02")
		r.Start = st.UTC().Format(time.RFC3339)
		r.End = et.UTC().Format(time.RFC3339)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SourceRow 是数据来源概览。
type SourceRow struct {
	Source     string    `json:"source"`
	DeviceType string    `json:"device_class"`
	Preferred  bool      `json:"preferred"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Samples    int64     `json:"samples"`
}

// Sources 列出已登记的数据来源。
func (db *DB) Sources(ctx context.Context) ([]SourceRow, error) {
	rows, err := db.Pool.Query(ctx, `SELECT s.source, s.device_class, s.preferred, s.first_seen_at, s.last_seen_at,
		COALESCE(c.cnt, 0)
		FROM sources s
		LEFT JOIN (SELECT source, count(*) cnt FROM samples GROUP BY source) c ON c.source = s.source
		ORDER BY c.cnt DESC NULLS LAST`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SourceRow, 0, 16)
	for rows.Next() {
		var r SourceRow
		if err := rows.Scan(&r.Source, &r.DeviceType, &r.Preferred, &r.FirstSeen, &r.LastSeen, &r.Samples); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SyncStatus 是同步健康度概览。
type SyncStatus struct {
	LastRun     *RunBrief      `json:"last_run,omitempty"`
	RecentRuns  []RunBrief     `json:"recent_runs"`
	Watermarks  []WatermarkRow `json:"watermarks"`
	MissingDays []string       `json:"missing_days"`
	HomeTZ      string         `json:"home_timezone,omitempty"`
}

// RunBrief 是一次摄入的简要信息。
type RunBrief struct {
	ID           int64   `json:"id"`
	Transport    string  `json:"transport"`
	AutomationID string  `json:"automation_id,omitempty"`
	SessionID    string  `json:"session_id,omitempty"`
	ReceivedAt   string  `json:"received_at"`
	SampleCount  int     `json:"sample_count"`
	Inserted     int     `json:"inserted"`
	Duplicates   int     `json:"duplicates"`
	Status       string  `json:"status"`
	Error        *string `json:"error,omitempty"`
}

// WatermarkRow 是同步水位。
type WatermarkRow struct {
	AutomationID  string `json:"automation_id"`
	Metric        string `json:"metric"`
	LastStartTime string `json:"last_start_time"`
	LastLocalDate string `json:"last_local_date"`
}

// SyncStatusOf 汇总同步状态与数据缺口。
func (db *DB) SyncStatusOf(ctx context.Context, days int) (SyncStatus, error) {
	// 三个 slice 字段必须初始化：nil slice 会被编码成 JSON null，
	// 前端拿到 null 后展开/取下标会直接抛错。
	st := SyncStatus{
		RecentRuns:  make([]RunBrief, 0, 10),
		Watermarks:  make([]WatermarkRow, 0, 16),
		MissingDays: make([]string, 0, days),
	}
	rows, err := db.Pool.Query(ctx, `SELECT id, transport, COALESCE(automation_id,''), COALESCE(session_id,''), received_at,
		sample_count, inserted, duplicates, status, error
		FROM ingest_runs ORDER BY received_at DESC LIMIT 10`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var r RunBrief
		var at time.Time
		if err := rows.Scan(&r.ID, &r.Transport, &r.AutomationID, &r.SessionID, &at, &r.SampleCount, &r.Inserted, &r.Duplicates, &r.Status, &r.Error); err != nil {
			return st, err
		}
		r.ReceivedAt = at.UTC().Format(time.RFC3339)
		st.RecentRuns = append(st.RecentRuns, r)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	if len(st.RecentRuns) > 0 {
		last := st.RecentRuns[0]
		st.LastRun = &last
	}

	wmRows, err := db.Pool.Query(ctx, `SELECT automation_id, metric, last_start_time, last_local_date FROM sync_watermark ORDER BY metric`)
	if err != nil {
		return st, err
	}
	defer wmRows.Close()
	for wmRows.Next() {
		var w WatermarkRow
		var at time.Time
		var ld pgtype.Date
		if err := wmRows.Scan(&w.AutomationID, &w.Metric, &at, &ld); err != nil {
			return st, err
		}
		w.LastStartTime = at.UTC().Format(time.RFC3339)
		w.LastLocalDate = ld.Time.Format("2006-01-02")
		st.Watermarks = append(st.Watermarks, w)
	}
	if err := wmRows.Err(); err != nil {
		return st, err
	}

	// 缺口：最近 N 天中缺失 step_count 的日期
	missing, err := db.Pool.Query(ctx, `SELECT d::date::text FROM generate_series(
			(SELECT COALESCE(min(local_date), CURRENT_DATE - $1::int) FROM daily_metrics_day), CURRENT_DATE, INTERVAL '1 day') d
		WHERE d::date NOT IN (SELECT local_date FROM daily_metrics_day WHERE metric = 'step_count')
		ORDER BY d`, days)
	if err != nil {
		return st, err
	}
	defer missing.Close()
	for missing.Next() {
		var s string
		if err := missing.Scan(&s); err == nil {
			st.MissingDays = append(st.MissingDays, s)
		}
	}
	return st, missing.Err()
}

func collectDaily(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]DailyPoint, error) {
	out := make([]DailyPoint, 0, 64)
	for rows.Next() {
		var p DailyPoint
		var ld pgtype.Date
		if err := rows.Scan(&ld, &p.Metric, &p.Value, &p.SampleCount, &p.SourceCount, &p.Sources); err != nil {
			return nil, err
		}
		p.Date = ld.Time.Format("2006-01-02")
		out = append(out, p)
	}
	return out, rows.Err()
}

func parseDate(s string) (pgtype.Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf("日期格式应为 YYYY-MM-DD: %w", err)
	}
	return pgtype.Date{Time: t, Valid: true}, nil
}
