package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

func embeddedWorkout(name string, start time.Time) sample.Workout {
	w := workout(name, start)
	w.Route = &sample.WorkoutRoute{
		StartTime: start, EndTime: start.Add(2 * time.Minute),
		Points: []sample.WorkoutRoutePoint{
			{AtSec: 0, Lat: 30.25, Lon: 120.15, Ele: 4.5},
			{AtSec: 60, Lat: 30.26, Lon: 120.16, Ele: 6.25},
			{AtSec: 120, Lat: 30.27, Lon: 120.17},
		},
	}
	return w
}

func routeID(t *testing.T, db *DB, ctx context.Context, name string) int64 {
	t.Helper()
	var id int64
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM workouts WHERE name=$1`, name).Scan(&id); err != nil {
		t.Fatalf("查 %s 失败: %v", name, err)
	}
	return id
}

// 导出内嵌的轨迹与 GPX 导入共用一张表：同一批运动只该有一条折线，而不是「看谁最后跑」。
// 因此自动写入要遵守两条硬规则——内容没变不重写、手工导入的那份永远不被顶掉。
func TestWriteEmbeddedRouteFromIngest(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()
	start := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)

	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{embeddedWorkout("户外 步行", start)}); err != nil {
		t.Fatal(err)
	}
	id := routeID(t, db, ctx, "户外 步行")

	var (
		file                 string
		count                int
		latMin, latMax       float64
		lons                 []float64
		atSecs               []int32
		routeStart, routeEnd time.Time
	)
	err := db.Pool.QueryRow(ctx,
		`SELECT file_name, point_count, lat_min, lat_max, lons, at_secs, start_time, end_time
		   FROM workout_routes WHERE workout_id=$1`, id).
		Scan(&file, &count, &latMin, &latMax, &lons, &atSecs, &routeStart, &routeEnd)
	if err != nil {
		t.Fatalf("内嵌轨迹没有落库: %v", err)
	}
	if file != "health-auto-export.json" {
		t.Fatalf("来源标记应区分内嵌轨迹，实际 %q", file)
	}
	if count != 3 || len(lons) != 3 || atSecs[2] != 120 {
		t.Fatalf("折线落库不完整: count=%d lons=%v at=%v", count, lons, atSecs)
	}
	if latMin > 30.25 || latMax < 30.27 {
		t.Fatalf("bbox 应覆盖首末点，实际 %v~%v", latMin, latMax)
	}
	if !routeEnd.After(routeStart) {
		t.Fatalf("轨迹起止应有序: %s ~ %s", routeStart, routeEnd)
	}

	// 重放同一份内容：指纹相同，不该产生第二次写入。
	// 按「负载 sha256」判重在这里不成立——同一批轨迹换个日期窗口再导一次，负载哈希必变，
	// 于是每条运动都要重写一遍几千个点。
	var before time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT ingested_at FROM workout_routes WHERE workout_id=$1`, id).
		Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{embeddedWorkout("户外 步行", start)}); err != nil {
		t.Fatal(err)
	}
	var after time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT ingested_at FROM workout_routes WHERE workout_id=$1`, id).
		Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Fatalf("同内容重放不该重写轨迹行: %s → %s", before, after)
	}

	// 内容变了就要重写（否则界面永远看着旧折线）。
	changed := embeddedWorkout("户外 步行", start)
	changed.Route.Points[2].Lon = 120.99
	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{changed}); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT ingested_at FROM workout_routes WHERE workout_id=$1`, id).
		Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after.Equal(before) {
		t.Fatalf("内容变了应当重写，ingested_at 却还是 %s", after)
	}

	// 没有轨迹的运动不能留下一行空数组：那会被前端读成「采到 0 个点的轨迹」。
	mustExec(t, db, `INSERT INTO workouts (name, start_time, end_time, local_date)
		VALUES ('室内 骑行', $1, $2, '2026-05-04')`, start.Add(24*time.Hour), start.Add(25*time.Hour))
	var rows int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM workout_routes`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("无轨迹的运动不该写 workout_routes，实际 %d 行", rows)
	}
}

// 两条来源的覆盖方向是单向的：自动写入只覆盖自动写入。
// 用户手工挑过的那份 GPX（可能是补采的、点位更密的）被一次后台同步静默顶掉，
// 是那种没人会发现的损失。反向（手工导入覆盖自动写入）照旧成立。
func TestEmbeddedRouteDoesNotOverwriteGPXRoute(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()
	start := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)

	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{embeddedWorkout("户外 骑行", start)}); err != nil {
		t.Fatal(err)
	}
	id := routeID(t, db, ctx, "户外 骑行")

	g := route("户外 骑行", "20260503080000")
	g.StartTime = start
	g.EndTime = start.Add(8 * time.Second)
	if _, err := db.WriteRoute(ctx, id, g, "gpx-sha"); err != nil {
		t.Fatal(err)
	}

	// 再投递一次同一份导出：轨迹内容变了（内嵌的那组点），也不许动手工导入的行。
	w := embeddedWorkout("户外 骑行", start)
	w.Route.Points[1].Lat = 31.99
	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{w}); err != nil {
		t.Fatal(err)
	}
	var (
		file string
		sha  string
		lats []float64
	)
	if err := db.Pool.QueryRow(ctx, `SELECT file_name, sha256, lats FROM workout_routes WHERE workout_id=$1`, id).
		Scan(&file, &sha, &lats); err != nil {
		t.Fatal(err)
	}
	if file != g.FileName || sha != "gpx-sha" {
		t.Fatalf("手工导入的轨迹被自动写入顶掉了: file=%q sha=%q", file, sha)
	}
	for _, lat := range lats {
		if lat == 31.99 {
			t.Fatalf("折线被内嵌轨迹改写了: %v", lats)
		}
	}

	// 反向照常：GPX 重导（换指纹）要能覆盖内嵌的那份。
	mustExec(t, db, `DELETE FROM workout_routes WHERE workout_id=$1`, id)
	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{embeddedWorkout("户外 骑行", start)}); err != nil {
		t.Fatal(err)
	}
	if wrote, err := db.WriteRoute(ctx, id, g, "gpx-sha-2"); err != nil || !wrote {
		t.Fatalf("GPX 应能覆盖内嵌轨迹: %v %v", wrote, err)
	}
}
