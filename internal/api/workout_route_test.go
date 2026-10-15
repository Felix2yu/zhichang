package api

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/gpx"
)

func TestWorkoutRouteContract(t *testing.T) {
	h, db := newRouter(t, "")
	runID := newRun(t, db)

	start := time.Date(2026, 10, 14, 1, 12, 30, 0, time.UTC)
	total, active, avg, mx := 120.0, 80.0, 118.5, 138.0
	w := sample.Workout{
		Name: "户外 步行", Source: "Apple Watch", StartTime: start, EndTime: start.Add(20 * time.Minute),
		DurationMin: 20.5, TotalEnergy: &total, EnergyUnit: "kcal", ActiveEnergy: &active, ActiveUnit: "kcal",
		AvgHR: &avg, MaxHR: &mx, LocalDate: "2026-10-14", TZOffsetMin: 480,
		HR:   []sample.WorkoutPoint{{AtSec: -30, Value: 96}, {AtSec: 30, Value: 104}, {AtSec: 90, Value: 112}},
		Meta: map[string]any{"location": "户外", "indoor": false},
	}
	if _, err := db.WriteWorkouts(context.Background(), runID, []sample.Workout{w}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT id FROM workouts WHERE name='户外 步行'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	route := &gpx.Route{
		FileName: "户外 步行-Route-20261014_091230.gpx", WorkoutName: "户外 步行", Wall: "20261014091230",
		StartTime: start.Add(9 * time.Second), EndTime: start.Add(9*time.Second + 2*time.Minute),
		Lats: []float64{30.2541, 30.2549}, Lons: []float64{120.1532, 120.1540},
		Eles: []float64{0, 8.48}, AtSec: []int32{0, 120},
	}
	if _, err := db.WriteRoute(context.Background(), id, route, "sha-test"); err != nil {
		t.Fatal(err)
	}

	// 列表要带上 id 与新字段：前端点开详情靠 id 取曲线与轨迹
	rows := get(t, h, "/api/workouts?from=2026-10-14&to=2026-10-14").arr(t, "rows")
	if len(rows) != 1 {
		t.Fatalf("列表应有 1 条，实际 %d", len(rows))
	}
	first := rows[0].(map[string]any)
	for _, k := range []string{"id", "active_energy", "avg_hr", "max_hr"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("列表缺少字段 %q（前端要靠它点开详情/显示心率）: %v", k, first)
		}
	}
	if first["active_energy"].(float64) != 80 {
		t.Fatalf("主动能量应单独返回（不与总能量混为一谈）: %v", first["active_energy"])
	}

	// 曲线端点：相对秒数原样出来，含早于起点的负值
	s := get(t, h, "/api/workouts/series?id="+strconv.FormatInt(id, 10))
	if s.status != 200 {
		t.Fatalf("series 状态码 = %d body=%s", s.status, s.body)
	}
	hr := s.arr(t, "hr_bpm")
	secs := s.arr(t, "hr_secs")
	if len(hr) != 3 || len(secs) != 3 {
		t.Fatalf("曲线点数错误: %v / %v", hr, secs)
	}
	if secs[0].(float64) != -30 {
		t.Fatalf("早于运动起点的点必须保留（丢了就等于白丢数据），实际 %v", secs[0])
	}
	if s.str(t, "start") != start.Format(time.RFC3339) {
		t.Fatalf("时轴原点应是运动起点，实际 %s", s.str(t, "start"))
	}
	if !s.fields["has_route"].(bool) || s.num(t, "route_points") != 2 {
		t.Fatalf("series 要顺带回报轨迹摘要，让列表知道要不要显示地图: %v %v",
			s.fields["has_route"], s.fields["route_points"])
	}

	// 轨迹端点：折线本体单独取，列表页不为它付开销
	r := get(t, h, "/api/workouts/route?id="+strconv.FormatInt(id, 10))
	if r.status != 200 {
		t.Fatalf("route 状态码 = %d body=%s", r.status, r.body)
	}
	if r.num(t, "points") != 2 || len(r.arr(t, "lats")) != 2 || len(r.arr(t, "at_secs")) != 2 {
		t.Fatalf("轨迹内容错误: %s", r.body)
	}
	if r.str(t, "file_name") != route.FileName {
		t.Fatalf("应回报来源文件名（用户要能核对是哪份 GPX）: %s", r.str(t, "file_name"))
	}
	if r.num(t, "elev_max") != 8.48 {
		t.Fatalf("高程摘要错误: %v", r.fields["elev_max"])
	}
}

// 没有轨迹、id 非法、记录不存在，都必须给出可区分的答复：
// 「这次运动没导过路线」是常态，界面要显示一句说明而不是报错。
func TestWorkoutRouteAndSeriesErrors(t *testing.T) {
	h, db := newRouter(t, "")
	runID := newRun(t, db)
	start := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	if _, err := db.WriteWorkouts(context.Background(), runID, []sample.Workout{
		{Name: "整理", Source: "Apple Watch", StartTime: start, EndTime: start.Add(time.Minute),
			DurationMin: 1, LocalDate: "2026-10-13", TZOffsetMin: 480},
	}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT id FROM workouts WHERE name='整理'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	q := strconv.FormatInt(id, 10)

	get(t, h, "/api/workouts/route?id="+q).wantErr(t, 404)
	for _, bad := range []string{"", "abc", "0", "-1"} {
		get(t, h, "/api/workouts/route?id="+bad).wantErr(t, 400)
	}
	// 曲线端点没有轨迹时也要正常返回（has_route=false），否则地图区会一直转圈
	s := get(t, h, "/api/workouts/series?id="+q)
	if s.status != 200 {
		t.Fatalf("无轨迹的 series 应正常返回: %d %s", s.status, s.body)
	}
	if s.fields["has_route"].(bool) {
		t.Fatal("没写过轨迹却回报 has_route=true")
	}
	get(t, h, "/api/workouts/series?id=999999999").wantErr(t, 404)
}
