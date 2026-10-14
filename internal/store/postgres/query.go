package postgres

import (
	"context"
	"fmt"
	"time"

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
type SleepRow struct {
	LocalDate    string   `json:"local_date"`
	Source       string   `json:"source"`
	SleepStart   *string  `json:"sleep_start,omitempty"`
	SleepEnd     *string  `json:"sleep_end,omitempty"`
	TotalSleepHr *float64 `json:"total_sleep_hr,omitempty"`
	AsleepHr     *float64 `json:"asleep_hr,omitempty"`
	CoreHr       *float64 `json:"core_hr,omitempty"`
	DeepHr       *float64 `json:"deep_hr,omitempty"`
	RemHr        *float64 `json:"rem_hr,omitempty"`
	InBedHr      *float64 `json:"in_bed_hr,omitempty"`
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
	rows, err := db.Pool.Query(ctx, `SELECT local_date, source, sleep_start, sleep_end, total_sleep_hr, asleep_hr, core_hr, deep_hr, rem_hr, in_bed_hr
		FROM sleep_sessions WHERE local_date BETWEEN $1 AND $2 ORDER BY local_date`, fd, td)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SleepRow, 0, 64)
	for rows.Next() {
		var r SleepRow
		var ld pgtype.Date
		var ss, se *time.Time
		if err := rows.Scan(&ld, &r.Source, &ss, &se, &r.TotalSleepHr, &r.AsleepHr, &r.CoreHr, &r.DeepHr, &r.RemHr, &r.InBedHr); err != nil {
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
		out = append(out, r)
	}
	return out, rows.Err()
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
