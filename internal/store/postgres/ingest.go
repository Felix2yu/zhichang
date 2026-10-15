package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// RunMeta 描述一次摄入请求的元数据（部分来自 HAE 自动附带的 header）。
type RunMeta struct {
	Transport      string
	AutomationID   string
	AutomationName string
	SessionID      string
	Period         string
	Aggregation    string
	PayloadSHA256  string
	PayloadBytes   int64
	ArchivedPath   string
}

// IngestStats 是一次摄入的统计结果。
//
// Workouts / Sleep / ECG 只反映本次请求的实况，不像别的字段那样落进 ingest_runs：
// 事件表各有自己的去重键，重复提交时按 session 回放的是样本统计，
// 这几个计数会读回 0。这是既有口径（Workouts/Sleep 一直如此），不是心电特有的。
type IngestStats struct {
	RunID        int64
	Received     int
	Inserted     int
	Duplicates   int
	Updated      int
	Workouts     int
	Sleep        int
	ECG          int
	AffectedFrom string
	AffectedTo   string
	DuplicateRun bool
}

// BeginRun 登记一次摄入；同一 session-id 重复提交时返回既有 run（幂等）。
func (db *DB) BeginRun(ctx context.Context, meta RunMeta) (int64, bool, error) {
	const q = `INSERT INTO ingest_runs
		(transport, automation_id, automation_name, session_id, period, aggregation, payload_sha256, payload_bytes, archived_path, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'running')
		ON CONFLICT (session_id) WHERE session_id IS NOT NULL DO NOTHING
		RETURNING id`
	var id int64
	err := db.Pool.QueryRow(ctx, q,
		meta.Transport, meta.AutomationID, meta.AutomationName, nullable(meta.SessionID),
		nullable(meta.Period), nullable(meta.Aggregation),
		nullable(meta.PayloadSHA256), meta.PayloadBytes, nullable(meta.ArchivedPath),
	).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, err
	}
	if meta.SessionID == "" {
		return 0, false, errors.New("session 缺失且无法判定重复")
	}
	var existing int64
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM ingest_runs WHERE session_id = $1`, meta.SessionID).Scan(&existing); err != nil {
		return 0, false, err
	}
	return existing, true, nil
}

// FinishRun 收尾统计（成功或失败）。
func (db *DB) FinishRun(ctx context.Context, runID int64, stats IngestStats, runErr error) error {
	status := "ok"
	var msg string
	if runErr != nil {
		status = "error"
		msg = runErr.Error()
	}
	_, err := db.Pool.Exec(ctx, `UPDATE ingest_runs SET
		sample_count=$2, inserted=$3, duplicates=$4, updated=$5, status=$6, error=NULLIF($7,''),
		affected_from=NULLIF($8,'')::date, affected_to=NULLIF($9,'')::date,
		workout_count=$10, sleep_count=$11, ecg_count=$12
		WHERE id=$1`, runID, stats.Received, stats.Inserted, stats.Duplicates, stats.Updated,
		status, msg, stats.AffectedFrom, stats.AffectedTo, stats.Workouts, stats.Sleep, stats.ECG)
	return err
}

// LoadRunStats 读取某次摄入的统计（用于重复提交时回放上次结果）。
func (db *DB) LoadRunStats(ctx context.Context, runID int64) (IngestStats, error) {
	var st IngestStats
	var from, to pgtype.Date
	err := db.Pool.QueryRow(ctx, `SELECT id, sample_count, inserted, duplicates, updated,
			workout_count, sleep_count, ecg_count, affected_from, affected_to
		FROM ingest_runs WHERE id = $1`, runID).Scan(
		&st.RunID, &st.Received, &st.Inserted, &st.Duplicates, &st.Updated,
		&st.Workouts, &st.Sleep, &st.ECG, &from, &to)
	if err != nil {
		return st, err
	}
	if from.Valid {
		st.AffectedFrom = from.Time.Format("2006-01-02")
	}
	if to.Valid {
		st.AffectedTo = to.Time.Format("2006-01-02")
	}
	return st, nil
}

// WriteSamples 批量写入样本：COPY 进 staging 表后用 ON CONFLICT 合并进 hypertable。
// COPY 本身不支持 ON CONFLICT，因此必须经由 staging 合并。
func (db *DB) WriteSamples(ctx context.Context, runID int64, samples []sample.Sample, mode string) (inserted, updated int, from, to string, err error) {
	if len(samples) == 0 {
		return 0, 0, "", "", nil
	}
	staging := fmt.Sprintf("samples_staging_%d", runID)
	if _, err := db.Pool.Exec(ctx, fmt.Sprintf(
		`CREATE UNLOGGED TABLE IF NOT EXISTS %s (LIKE samples INCLUDING DEFAULTS)`, pgx.Identifier{staging}.Sanitize())); err != nil {
		return 0, 0, "", "", err
	}
	defer func() {
		_, _ = db.Pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", pgx.Identifier{staging}.Sanitize()))
	}()

	columns := []string{"start_time", "end_time", "metric", "source", "device", "value", "value_min", "value_max", "unit", "local_date", "tz_offset_min", "meta", "ingest_run_id"}
	rows := make([][]any, len(samples))
	minDate, maxDate := samples[0].LocalDate, samples[0].LocalDate
	for i, s := range samples {
		if s.LocalDate < minDate {
			minDate = s.LocalDate
		}
		if s.LocalDate > maxDate {
			maxDate = s.LocalDate
		}
		rows[i] = []any{
			s.StartTime.UTC(), s.EndTime.UTC(), s.Metric, s.Source, nullable(s.Device),
			s.Value, s.ValueMin, s.ValueMax, s.Unit, s.LocalDate, s.TZOffsetMin,
			metaJSON(s.Meta), runID,
		}
	}
	if _, err := db.Pool.CopyFrom(ctx, pgx.Identifier{staging}, columns, pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		return rows[i], nil
	})); err != nil {
		return 0, 0, "", "", fmt.Errorf("COPY 到 staging 失败: %w", err)
	}

	var merge string
	switch mode {
	case "latest":
		merge = fmt.Sprintf(`INSERT INTO samples (%s)
			SELECT %s FROM %s
			ON CONFLICT (metric, source, start_time) DO UPDATE SET
				value = EXCLUDED.value,
				value_min = EXCLUDED.value_min,
				value_max = EXCLUDED.value_max,
				end_time = EXCLUDED.end_time,
				meta = EXCLUDED.meta,
				ingest_run_id = EXCLUDED.ingest_run_id,
				ingested_at = now()
			WHERE samples.value IS DISTINCT FROM EXCLUDED.value`, colList(columns), colList(columns), pgx.Identifier{staging}.Sanitize())
	default:
		merge = fmt.Sprintf(`INSERT INTO samples (%s)
			SELECT %s FROM %s
			ON CONFLICT (start_time, metric, source, value) DO NOTHING`, colList(columns), colList(columns), pgx.Identifier{staging}.Sanitize())
	}

	tag, err := db.Pool.Exec(ctx, merge)
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("合并进 samples 失败: %w", err)
	}
	affected := int(tag.RowsAffected())
	if mode == "latest" {
		return affected, affected, minDate, maxDate, nil
	}
	return affected, 0, minDate, maxDate, nil
}

// WriteWorkouts 写入运动记录（同名同来源同起始时间视为重复）。
// WriteWorkouts 写入运动记录，返回新插入的条数。
//
// 幂等键是 (name, source, start_time)。与样本不同，这里冲突时**补齐字段而不是丢弃**：
// 上游字段是分批出现的（老版本导出不带 effortScore 与逐点心率），
// 冲突即丢弃会让后来才有的字段永远补不进去。数值用 COALESCE 保住已有值，
// meta 按键合并，两边都不被空值冲掉。
//
// ingest_run_id 不跟着改：它记的是「哪一次投递把这条运动带进来的」，
// 后续投递只补字段，不该把溯源指针挪到自己身上。
func (db *DB) WriteWorkouts(ctx context.Context, runID int64, list []sample.Workout) (int, error) {
	if len(list) == 0 {
		return 0, nil
	}
	const q = `INSERT INTO workouts
		(name, start_time, end_time, duration_min, total_energy, energy_unit,
		 active_energy, active_energy_unit, total_distance, distance_unit,
		 avg_hr, max_hr, effort_score, source, local_date, tz_offset_min, meta, ingest_run_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (name, source, start_time) DO UPDATE SET
			duration_min = EXCLUDED.duration_min,
			total_energy = COALESCE(EXCLUDED.total_energy, workouts.total_energy),
			energy_unit = COALESCE(EXCLUDED.energy_unit, workouts.energy_unit),
			active_energy = COALESCE(EXCLUDED.active_energy, workouts.active_energy),
			active_energy_unit = COALESCE(EXCLUDED.active_energy_unit, workouts.active_energy_unit),
			total_distance = COALESCE(EXCLUDED.total_distance, workouts.total_distance),
			distance_unit = COALESCE(EXCLUDED.distance_unit, workouts.distance_unit),
			avg_hr = COALESCE(EXCLUDED.avg_hr, workouts.avg_hr),
			max_hr = COALESCE(EXCLUDED.max_hr, workouts.max_hr),
			effort_score = COALESCE(EXCLUDED.effort_score, workouts.effort_score),
			meta = COALESCE(workouts.meta, '{}'::jsonb) || COALESCE(EXCLUDED.meta, '{}'::jsonb)
		RETURNING id, (xmax = 0)`
	n := 0
	for _, w := range list {
		var id int64
		var isNew bool
		if err := db.Pool.QueryRow(ctx, q, w.Name, w.StartTime.UTC(), w.EndTime.UTC(), w.DurationMin,
			w.TotalEnergy, nullable(w.EnergyUnit), w.ActiveEnergy, nullable(w.ActiveUnit),
			w.TotalDistance, nullable(w.DistanceUnit), w.AvgHR, w.MaxHR, w.EffortScore,
			w.Source, w.LocalDate, w.TZOffsetMin, metaJSON(w.Meta), runID).Scan(&id, &isNew); err != nil {
			return n, err
		}
		if isNew {
			n++
		}
		if err := db.writeWorkoutSeries(ctx, id, w); err != nil {
			return n, err
		}
	}
	return n, nil
}

// writeWorkoutSeries 落逐点心率与恢复心率。空序列不写行（宁可不写，也不留一行空数组
// 让前端把「没有曲线」读成「曲线是平的」）。
func (db *DB) writeWorkoutSeries(ctx context.Context, workoutID int64, w sample.Workout) error {
	if len(w.HR) == 0 && len(w.Recovery) == 0 && len(w.Splits) == 0 {
		return nil
	}
	const q = `INSERT INTO workout_series (workout_id, hr_secs, hr_bpm, rec_secs, rec_bpm, splits)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (workout_id) DO UPDATE SET
			hr_secs = COALESCE(EXCLUDED.hr_secs, workout_series.hr_secs),
			hr_bpm = COALESCE(EXCLUDED.hr_bpm, workout_series.hr_bpm),
			rec_secs = COALESCE(EXCLUDED.rec_secs, workout_series.rec_secs),
			rec_bpm = COALESCE(EXCLUDED.rec_bpm, workout_series.rec_bpm),
			splits = COALESCE(EXCLUDED.splits, workout_series.splits),
			ingested_at = now()`
	secs, vals := splitPoints(w.HR)
	rsecs, rvals := splitPoints(w.Recovery)
	var splits any
	if len(w.Splits) > 0 {
		splits = w.Splits
	}
	_, err := db.Pool.Exec(ctx, q, workoutID, secs, vals, rsecs, rvals, splits)
	return err
}

// splitPoints 把 [{at,value}] 拆成两条平行数组（库里存数组，读一次即得整条曲线）。
// 空序列返回两个 nil，落库为 NULL 而不是空数组：区分「没采到」与「采到 0 个点」。
func splitPoints(pts []sample.WorkoutPoint) ([]int32, []float64) {
	if len(pts) == 0 {
		return nil, nil
	}
	secs := make([]int32, len(pts))
	vals := make([]float64, len(pts))
	for i, p := range pts {
		secs[i], vals[i] = p.AtSec, p.Value
	}
	return secs, vals
}

// WriteSleep 写入睡眠记录。
func (db *DB) WriteSleep(ctx context.Context, runID int64, list []sample.SleepSession) (int, error) {
	if len(list) == 0 {
		return 0, nil
	}
	const q = `INSERT INTO sleep_sessions
		(local_date, sleep_start, sleep_end, in_bed_start, in_bed_end, total_sleep_hr, asleep_hr, core_hr, deep_hr, rem_hr, awake_hr, in_bed_hr, source, tz_offset_min, ingest_run_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (local_date, source, sleep_start) DO NOTHING`
	n := 0
	for _, s := range list {
		tag, err := db.Pool.Exec(ctx, q, s.LocalDate, s.SleepStart, s.SleepEnd, s.InBedStart, s.InBedEnd,
			s.TotalSleepHr, s.AsleepHr, s.CoreHr, s.DeepHr, s.RemHr, s.AwakeHr, s.InBedHr, s.Source, s.TZOffsetMin, runID)
		if err != nil {
			return n, err
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

// UpsertSources 登记出现过的来源（iPhone / Watch / App）。
func (db *DB) UpsertSources(ctx context.Context, sources []string) error {
	if len(sources) == 0 {
		return nil
	}
	const q = `INSERT INTO sources (source, device_class) VALUES ($1, $2)
		ON CONFLICT (source) DO UPDATE SET last_seen_at = now()`
	for _, s := range sources {
		if s == "" {
			continue
		}
		if _, err := db.Pool.Exec(ctx, q, s, ClassifySource(s)); err != nil {
			return err
		}
	}
	return nil
}

// Watermark 是某个指标的最新同步水位。
type Watermark struct {
	Time      time.Time
	LocalDate string
}

// UpdateWatermark 更新每个 automation × metric 的同步水位。
func (db *DB) UpdateWatermark(ctx context.Context, automationID string, latest map[string]Watermark) error {
	const q = `INSERT INTO sync_watermark (automation_id, metric, last_start_time, last_local_date)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (automation_id, metric) DO UPDATE SET
			last_start_time = GREATEST(sync_watermark.last_start_time, EXCLUDED.last_start_time),
			last_local_date = GREATEST(sync_watermark.last_local_date, EXCLUDED.last_local_date),
			updated_at = now()`
	for metric, w := range latest {
		if _, err := db.Pool.Exec(ctx, q, automationID, metric, w.Time.UTC(), w.LocalDate); err != nil {
			return err
		}
	}
	return nil
}

// EnqueueRollup 把受影响的 (日期 × 指标) 放入重算队列（幂等）。
func (db *DB) EnqueueRollup(ctx context.Context, dates map[string]map[string]struct{}) error {
	const q = `INSERT INTO rollup_queue (local_date, metric) VALUES ($1,$2) ON CONFLICT DO NOTHING`
	for date, metrics := range dates {
		for m := range metrics {
			if _, err := db.Pool.Exec(ctx, q, date, m); err != nil {
				return err
			}
		}
	}
	return nil
}

// SeriesMeta 供外部收集本次写入涉及的来源与日期。
type SeriesMeta struct {
	Sources map[string]struct{}
	Dates   map[string]map[string]struct{}
}

func colList(cols []string) string {
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ", "
		}
		out += c
	}
	return out
}

func metaJSON(m map[string]any) []byte {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

func nullable(s string) any {
	if s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}

// ClassifySource 粗判来源设备类别。
func ClassifySource(s string) string {
	lower := strings.ToLower(s)
	switch {
	case strings.Contains(lower, "watch"):
		return "watch"
	case strings.Contains(lower, "iphone"), strings.Contains(lower, "ipad"):
		return "phone"
	case strings.Contains(lower, "health"):
		return "app"
	default:
		return "unknown"
	}
}
