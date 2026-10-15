package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/gpx"
)

func route(name, wall string) *gpx.Route {
	st := time.Date(2026, 10, 14, 1, 12, 39, 0, time.UTC)
	return &gpx.Route{
		FileName: name + "-Route-" + wall + ".gpx", WorkoutName: name, Wall: wall,
		StartTime: st, EndTime: st.Add(8 * time.Second),
		Lats: []float64{30.2541, 30.2542}, Lons: []float64{120.1532, 120.1533},
		Eles: []float64{3.5, 4.0}, AtSec: []int32{0, 8},
	}
}

func workout(name string, start time.Time) sample.Workout {
	v := 20.0
	d := 1.8
	return sample.Workout{
		Name: name, Source: "Apple Watch", StartTime: start, EndTime: start.Add(20 * time.Minute),
		DurationMin: 20, TotalEnergy: &v, EnergyUnit: "kcal", TotalDistance: &d, DistanceUnit: "km",
		LocalDate: "2026-10-14", TZOffsetMin: 480,
	}
}

// TestMatchWorkoutByWall 锁轨迹归属的匹配口径：文件名给的是本地钟点，
// 库里是 UTC + 记录自带偏移，两边必须用同一个表达式拉到一把尺子上。
func TestMatchWorkoutByWall(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()
	// 09:12:30 (+0800) = 01:12:30 UTC
	start := time.Date(2026, 10, 14, 1, 12, 30, 0, time.UTC)
	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{workout("户外 步行", start)}); err != nil {
		t.Fatal(err)
	}

	ids, err := db.MatchWorkoutByWall(ctx, "户外 步行", "20261014091230")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("本地钟点应匹配到 1 条运动，实际 %v", ids)
	}
	// 差一秒都不能配：挂错运动等于把这段行踪记到另一天头上
	for _, wall := range []string{"20261014091229", "20261014091231", "20261014011230"} {
		ids, err := db.MatchWorkoutByWall(ctx, "户外 步行", wall)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 0 {
			t.Fatalf("%s 不该匹配到运动（按 UTC 或差一秒都不行），实际 %v", wall, ids)
		}
	}
	if ids, err := db.MatchWorkoutByWall(ctx, "户外 骑行", "20261014091230"); err != nil || len(ids) != 0 {
		t.Fatalf("运动名不同不该匹配，实际 %v %v", ids, err)
	}

	// 同名同刻但来源不同（唯一键是 name+source+start_time）→ 必须报歧义，不能挑一条
	second := workout("户外 步行", start)
	second.Source = "Keep"
	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{second}); err != nil {
		t.Fatal(err)
	}
	if ids, err := db.MatchWorkoutByWall(ctx, "户外 步行", "20261014091230"); err != nil || len(ids) != 2 {
		t.Fatalf("应报出 2 条候选让调用方跳过，实际 %v %v", ids, err)
	}
}

// TestWriteRouteIdempotent 锁重放语义：同指纹不产生第二次写入，换指纹才覆盖。
func TestWriteRouteIdempotent(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 14, 1, 12, 30, 0, time.UTC)
	if _, err := db.WriteWorkouts(ctx, 0, []sample.Workout{workout("户外 步行", start)}); err != nil {
		t.Fatal(err)
	}
	var wid int64
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM workouts WHERE name='户外 步行'`).Scan(&wid); err != nil {
		t.Fatal(err)
	}

	// 第一次导入时谁都还没有：查不到指纹必须是 ("", nil)。
	// 把 pgx 的哨兵当成 database/sql 的那个，命令会在第一个文件上整体失败。
	if fp, err := db.RouteFingerprint(ctx, wid); err != nil || fp != "" {
		t.Fatalf("无轨迹时应返回空指纹而不是报错，实际 %q %v", fp, err)
	}

	r := route("户外 步行", "20261014091230")
	wrote, err := db.WriteRoute(ctx, wid, r, "sha-1")
	if err != nil || !wrote {
		t.Fatalf("首次写入应生效: %v %v", wrote, err)
	}
	if wrote, err := db.WriteRoute(ctx, wid, r, "sha-1"); err != nil || wrote {
		t.Fatalf("同指纹重放不该再写: %v %v", wrote, err)
	}
	if fp, err := db.RouteFingerprint(ctx, wid); err != nil || fp != "sha-1" {
		t.Fatalf("指纹应能读回，实际 %q %v", fp, err)
	}
	if wrote, err := db.WriteRoute(ctx, wid, route("户外 步行", "20261014091230"), "sha-2"); err != nil || !wrote {
		t.Fatalf("内容变了就该覆盖: %v %v", wrote, err)
	}

	// 摘要列与数组都要落得住：列表页只读摘要，折线只在点开时读
	var (
		count                          int
		latMin, latMax, lonMin, lonMax float64
		lats                           []float64
		atSec                          []int32
		routeStart, routeEnd           time.Time
	)
	if err := db.Pool.QueryRow(ctx,
		`SELECT point_count, lat_min, lat_max, lon_min, lon_max, start_time, end_time, lats, at_secs
		   FROM workout_routes WHERE workout_id=$1`, wid).
		Scan(&count, &latMin, &latMax, &lonMin, &lonMax, &routeStart, &routeEnd, &lats, &atSec); err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(lats) != 2 || len(atSec) != 2 || atSec[1] != 8 {
		t.Fatalf("折线落库不完整: count=%d lats=%v at=%v", count, lats, atSec)
	}
	if latMin > latMax || lonMin > lonMax {
		t.Fatalf("bbox 反了: lat %v~%v lon %v~%v", latMin, latMax, lonMin, lonMax)
	}
	if !routeStart.Equal(start.Add(9 * time.Second)) {
		t.Fatalf("轨迹起点应取第一个点的时间，实际 %s", routeStart)
	}

	// 运动记录删掉时轨迹跟着走（FK 是 ON DELETE CASCADE），否则库里会留下无主的坐标
	mustExec(t, db, `DELETE FROM workouts WHERE id=$1`, wid)
	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM workout_routes`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("删除运动后不该留下孤儿轨迹，实际 %d 条", left)
	}
}
