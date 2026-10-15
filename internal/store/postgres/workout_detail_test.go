package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// TestWriteWorkoutsBackfillsDetail 锁两件事：
//  1. 新增字段（主动能量、心率、effortScore、meta、逐点曲线）真的落得下去；
//  2. 同一 (name, source, start_time) 再次投递时**补齐**而不是丢弃——
//     上游字段是分批出现的，冲突即丢弃会让老记录永远补不上后来的字段。
func TestWriteWorkoutsBackfillsDetail(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 14, 1, 12, 30, 0, time.UTC)

	bare := sample.Workout{
		Name: "户外 步行", Source: "Apple Watch", StartTime: start, EndTime: start.Add(20 * time.Minute),
		DurationMin: 20.575, LocalDate: "2026-10-14", TZOffsetMin: 480,
	}
	if n, err := db.WriteWorkouts(ctx, 1, []sample.Workout{bare}); err != nil || n != 1 {
		t.Fatalf("首次写入应 1 条: %d %v", n, err)
	}

	rich := bare
	total, active, avg, mx, effort := 120.0, 80.0, 118.5, 138.0, 3.0
	rich.TotalEnergy, rich.EnergyUnit = &total, "kcal"
	rich.ActiveEnergy, rich.ActiveUnit = &active, "kcal"
	rich.AvgHR, rich.MaxHR, rich.EffortScore = &avg, &mx, &effort
	rich.Meta = map[string]any{"location": "户外", "indoor": false, "avg_speed": 4.05, "avg_speed_unit": "km"}
	rich.HR = []sample.WorkoutPoint{{AtSec: 30, Value: 104}, {AtSec: 90, Value: 112}}
	rich.Recovery = []sample.WorkoutPoint{{AtSec: 1235, Value: 110}}
	rich.Splits = []byte(`[{"index":1}]`)
	if n, err := db.WriteWorkouts(ctx, 2, []sample.Workout{rich}); err != nil || n != 0 {
		t.Fatalf("重放不该算新增: %d %v", n, err)
	}

	var (
		id                                  int64
		gotTotal, gotActive, gotAvg, gotMax float64
		gotEffort                           float64
		gotMeta                             []byte
	)
	if err := db.Pool.QueryRow(ctx,
		`SELECT id, total_energy, active_energy, avg_hr, max_hr, effort_score, meta::text
		   FROM workouts WHERE name='户外 步行'`).
		Scan(&id, &gotTotal, &gotActive, &gotAvg, &gotMax, &gotEffort, &gotMeta); err != nil {
		t.Fatal(err)
	}
	if gotActive != 80 || gotTotal != 120 {
		t.Fatalf("主动/总能量未落库: active=%v total=%v", gotActive, gotTotal)
	}
	if gotAvg != 118.5 || gotMax != 138 || gotEffort != 3 {
		t.Fatalf("心率/effort 未落库: %v %v %v", gotAvg, gotMax, gotEffort)
	}
	if gotMeta == nil || len(gotMeta) < 10 {
		t.Fatalf("meta 仍为空（此前该列自建表起恒 NULL）: %s", gotMeta)
	}

	// 溯源指针留在第一次把它带进来的那次投递上，后续补字段不改写它
	var runID int64
	if err := db.Pool.QueryRow(ctx, `SELECT ingest_run_id FROM workouts WHERE id=$1`, id).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if runID != 1 {
		t.Fatalf("补字段不该把 ingest_run_id 挪到后来的投递上，实际 %d", runID)
	}

	// 曲线与分段单独成表，列表查询不该为它们付出代价
	var (
		secs        []int32
		vals        []float64
		recSecs     []int32
		recVals     []float64
		splitsCount int
	)
	if err := db.Pool.QueryRow(ctx,
		`SELECT hr_secs, hr_bpm, rec_secs, rec_bpm, jsonb_array_length(splits) FROM workout_series WHERE workout_id=$1`, id).
		Scan(&secs, &vals, &recSecs, &recVals, &splitsCount); err != nil {
		t.Fatalf("曲线未入库: %v", err)
	}
	if len(secs) != 2 || secs[0] != 30 || vals[1] != 112 || splitsCount != 1 {
		t.Fatalf("曲线内容错误: %v %v splits=%d", secs, vals, splitsCount)
	}
	if len(recSecs) != 1 || recSecs[0] != 1235 || recVals[0] != 110 {
		t.Fatalf("恢复心率错误: %v %v", recSecs, recVals)
	}

	// 第三次投递带回一个更大的 meta：按键合并，旧键不被空值冲掉，新键补进去
	rich2 := rich
	rich2.Meta = map[string]any{"temperature": 21.5}
	rich2.HR = nil
	rich2.Recovery = nil
	rich2.Splits = nil
	if _, err := db.WriteWorkouts(ctx, 3, []sample.Workout{rich2}); err != nil {
		t.Fatal(err)
	}
	var merged map[string]any
	if err := db.Pool.QueryRow(ctx, `SELECT meta FROM workouts WHERE id=$1`, id).Scan(&merged); err != nil {
		t.Fatal(err)
	}
	if merged["location"] != "户外" || merged["temperature"] != 21.5 {
		t.Fatalf("meta 应按键合并（旧键保住、新键补进），实际 %v", merged)
	}
	// 曲线字段传空不能把已有曲线抹掉：COALESCE 的语义是「没有新数据就留着旧的」
	var stillThere int
	if err := db.Pool.QueryRow(ctx, `SELECT array_length(hr_bpm,1) FROM workout_series WHERE workout_id=$1`, id).
		Scan(&stillThere); err != nil {
		t.Fatal(err)
	}
	if stillThere != 2 {
		t.Fatalf("后续投递不该清空曲线，实际长度 %d", stillThere)
	}
}

// TestWorkoutRangeAndSeriesReadback 锁读出侧：列表带上新字段与 id，
// 曲线端点只在被单独请求时才把数组取出来。
func TestWorkoutRangeAndSeriesReadback(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 14, 1, 12, 30, 0, time.UTC)
	total, active, avg := 120.0, 80.0, 118.5
	w := sample.Workout{
		Name: "户外 骑行", Source: "Apple Watch", StartTime: start, EndTime: start.Add(7 * time.Minute),
		DurationMin: 6.9, TotalEnergy: &total, EnergyUnit: "kcal", ActiveEnergy: &active, ActiveUnit: "kcal",
		AvgHR: &avg, LocalDate: "2026-10-14", TZOffsetMin: 480,
		HR: []sample.WorkoutPoint{{AtSec: 5, Value: 130}, {AtSec: 10, Value: 141}},
	}
	if _, err := db.WriteWorkouts(ctx, 1, []sample.Workout{w}); err != nil {
		t.Fatal(err)
	}

	rows, err := db.WorkoutRange(ctx, "2026-10-01", "2026-10-31")
	if err != nil || len(rows) != 1 {
		t.Fatalf("列表读取失败: rows=%d err=%v", len(rows), err)
	}
	r := rows[0]
	if r.ID == 0 || r.ActiveEnergy == nil || *r.ActiveEnergy != 80 || r.AvgHR == nil || *r.AvgHR != 118.5 {
		t.Fatalf("列表未带回新字段: %+v", r)
	}

	s, err := db.WorkoutSeriesFor(ctx, r.ID)
	if err != nil || s == nil {
		t.Fatalf("曲线读取失败: %v %v", s, err)
	}
	if len(s.HRBPM) != 2 || s.HRSecs[1] != 10 || s.HasRoute {
		t.Fatalf("曲线内容错误: %+v", s)
	}
	if s.Start != start.UTC().Format(time.RFC3339) {
		t.Fatalf("曲线时轴原点应是运动起点，实际 %s", s.Start)
	}
	// 不存在的 id 回 (nil, nil)：端点据此给 404，而不是把内部错误抛给界面
	none, err := db.WorkoutSeriesFor(ctx, 999999)
	if err != nil || none != nil {
		t.Fatalf("查不到时应回 (nil,nil)，实际 %v %v", none, err)
	}
}
