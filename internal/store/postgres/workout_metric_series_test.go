package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

func seriesWorkout(name string, start time.Time, active float64) sample.Workout {
	w := workout(name, start)
	w.Series = []sample.WorkoutSeries{
		{Metric: "step_count", Unit: "步", Points: []sample.WorkoutPoint{
			{AtSec: 0, Value: 40}, {AtSec: 60, Value: 60}}},
		{Metric: "active_energy", Unit: "kcal", Points: []sample.WorkoutPoint{
			{AtSec: 60, Value: active}}},
	}
	return w
}

func seriesRow(t *testing.T, db *DB, ctx context.Context, workoutID int64, metric string) (int, []int32, []float64, *string, time.Time) {
	t.Helper()
	var (
		n    int
		secs []int32
		vals []float64
		unit *string
		at   time.Time
	)
	err := db.Pool.QueryRow(ctx,
		`SELECT cardinality(at_secs), at_secs, vals, unit, ingested_at
		   FROM workout_metric_series WHERE workout_id=$1 AND metric=$2`, workoutID, metric).
		Scan(&n, &secs, &vals, &unit, &at)
	if err != nil {
		t.Fatalf("读 %s 序列失败: %v", metric, err)
	}
	return n, secs, vals, unit, at
}

// 逐分钟数组的落库。
//
// 这一层必须单独验：归一化返回里有值 ≠ 库里有了——基础能量丢了一整年就丢在
// 「解析通过但没人写表」这一步，而界面上那栏本来就是空的，看不出矛盾。
func TestWriteWorkoutMetricSeries(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()
	start := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)

	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{seriesWorkout("户外 步行", start, 1)}); err != nil {
		t.Fatal(err)
	}
	id := routeID(t, db, ctx, "户外 步行")

	n, secs, vals, unit, _ := seriesRow(t, db, ctx, id, "step_count")
	if n != 2 || secs[1] != 60 || vals[1] != 60 {
		t.Fatalf("步数序列落库不完整: n=%d secs=%v vals=%v", n, secs, vals)
	}
	if unit == nil || *unit != "步" {
		t.Fatalf("上游单位应随值一起留存，实际 %v", unit)
	}
	if _, _, _, u2, _ := seriesRow(t, db, ctx, id, "active_energy"); u2 == nil || *u2 != "kcal" {
		t.Fatalf("主动能量应记字典单位，实际 %v", u2)
	}

	// 幂等：日期窗口重叠会把同一批逐分钟点反复送到，无条件重写等于每次同步
	// 重刷上万个点的数组。
	_, _, _, _, first := seriesRow(t, db, ctx, id, "step_count")
	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{seriesWorkout("户外 步行", start, 1)}); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, after := seriesRow(t, db, ctx, id, "step_count")
	if !after.Equal(first) {
		t.Fatalf("内容没变不该重写: %s → %s", first, after)
	}

	// 内容变了要重写，否则界面永远看着旧曲线。
	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{seriesWorkout("户外 步行", start, 9)}); err != nil {
		t.Fatal(err)
	}
	_, _, vals, _, rewritten := seriesRow(t, db, ctx, id, "active_energy")
	if vals[0] != 9 || !rewritten.After(first) {
		t.Fatalf("换了值应重写：vals=%v ingested_at=%s（首次 %s）", vals, rewritten, first)
	}

	// 空序列不写行：一行空数组会被读成「采到了但全是空的」，比没有更糟。
	empty := seriesWorkout("室内 骑行", start.Add(24*time.Hour), 1)
	empty.Series = nil
	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{empty}); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM workout_metric_series s JOIN workouts w ON w.id=s.workout_id
		  WHERE w.name='室内 骑行'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("空序列不该写行，实际 %d 行", rows)
	}

	// 运动删掉时序列跟着走，否则库里留下无主的逐分钟数据。
	mustExec(t, db, `DELETE FROM workouts WHERE id=$1`, id)
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM workout_metric_series`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("删除运动后不该留下孤儿序列，实际 %d 行", rows)
	}
}
