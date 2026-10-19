package hae

import (
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// HAE 的 REST 自动化只能发 JSON 负载，而这份 JSON 里本来就带着逐点经纬度
// （一年导出实测 109 条运动、30 万余个点）。不解析它并不会让位置数据少上传一分，
// 只是把它丢掉——而界面上表现为「这条运动没有轨迹」，看不出是上游没给还是应用没收。
func TestNormalizeWorkoutRouteFromEmbeddedJSON(t *testing.T) {
	raw := `{"data":{"workouts":[{
		"name":"户外 步行","start":"2026-05-03 08:00:00 +0800","end":"2026-05-03 08:10:00 +0800",
		"duration":600,"source":"示例的Apple Watch",
		"route":[
			{"timestamp":"2026-05-03 08:00:20 +0800","latitude":30.25,"longitude":120.15,"altitude":4.5,"speed":1.2},
			{"timestamp":"2026-05-03 08:01:20 +0800","latitude":30.26,"longitude":120.16,"altitude":6.25,"speed":1.4},
			{"timestamp":"2026-05-03 08:02:20 +0800","latitude":30.27,"longitude":120.17}
		]
	}]}}`

	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	w := res.Workouts[0]
	if w.Route == nil {
		t.Fatalf("内嵌轨迹没有被解析: %+v", w)
	}
	if len(w.Route.Points) != 3 {
		t.Fatalf("应有 3 个点，实际 %d", len(w.Route.Points))
	}
	// 时轴按「相对第一个点」存，与 GPX 导入侧同一个事实来源。
	// 第一个点比运动起点晚 20 秒，也必须从 0 起算（不是相对 start）。
	for i, want := range []int32{0, 60, 120} {
		if w.Route.Points[i].AtSec != want {
			t.Fatalf("第 %d 点相对秒数应为 %d，实际 %d", i, want, w.Route.Points[i].AtSec)
		}
	}
	if got := w.Route.StartTime; !got.Equal(time.Date(2026, 5, 3, 0, 0, 20, 0, time.UTC)) {
		t.Fatalf("轨迹起点应取第一个点的时刻，实际 %s", got)
	}
	if w.Route.Points[0].Lat != 30.25 || w.Route.Points[0].Ele != 4.5 {
		t.Fatalf("坐标/海拔未取到: %+v", w.Route.Points[0])
	}
	// 缺海拔按 0 存：海拔不进时轴，缺它只影响着色，不影响位置。
	if w.Route.Points[2].Ele != 0 {
		t.Fatalf("缺海拔应记 0，实际 %v", w.Route.Points[2].Ele)
	}
}

// 判据与 GPX 侧逐字对齐（gpx.readPoint）：同一批运动喂同一个渲染器，
// 两套规则不一致会画出两种折线，而两种看起来都「像是数据」。
func TestNormalizeWorkoutRouteMatchesGPXRules(t *testing.T) {
	t.Run("没定位的点丢弃，其余照收", func(t *testing.T) {
		raw := `{"data":{"workouts":[{
			"name":"户外 步行","start":"2026-05-03 08:00:00 +0800","end":"2026-05-03 08:10:00 +0800",
			"duration":600,"source":"示例的Apple Watch",
			"route":[
				{"timestamp":"2026-05-03 08:00:00 +0800","latitude":30.25,"longitude":120.15},
				{"timestamp":"2026-05-03 08:01:00 +0800","latitude":0,"longitude":0},
				{"timestamp":"2026-05-03 08:02:00 +0800","longitude":120.17},
				{"timestamp":"2026-05-03 08:03:00 +0800","latitude":30.27,"longitude":120.17}
			]}]}}`
		res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
		if res.Workouts[0].Route == nil || len(res.Workouts[0].Route.Points) != 2 {
			t.Fatalf("应只留下两个有坐标的点，实际 %+v", res.Workouts[0].Route)
		}
	})

	// 时轴按索引推：中间少一个时间，后面的点全部错位。
	// 画出一条时间轴错的折线比不画更糟，所以任一点时间认不出就整条不要。
	t.Run("任一点时间不可解析则整条不要", func(t *testing.T) {
		raw := `{"data":{"workouts":[{
			"name":"户外 步行","start":"2026-05-03 08:00:00 +0800","end":"2026-05-03 08:10:00 +0800",
			"duration":600,"source":"示例的Apple Watch",
			"route":[
				{"timestamp":"2026-05-03 08:00:00 +0800","latitude":30.25,"longitude":120.15},
				{"latitude":30.26,"longitude":120.16},
				{"timestamp":"明天下午","latitude":30.27,"longitude":120.17}
			]}]}}`
		res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
		if res.Workouts[0].Route != nil {
			t.Fatalf("有时间认不出就该整条放弃，实际 %+v", res.Workouts[0].Route)
		}
		if len(res.Workouts) != 1 {
			t.Fatalf("轨迹不可用不该让运动本身丢失: %d", len(res.Workouts))
		}
	})
}

// 上游把逐点时间换成 unix 秒是发生过的（心电逐点时间就是这种形态），
// 所以两种形态都要认。只认日期字符串会在上游改形态时把所有轨迹静默清空。
func TestNormalizeWorkoutRouteAcceptsUnixSeconds(t *testing.T) {
	raw := `{"data":{"workouts":[{
		"name":"户外 骑行","start":"2026-05-03 08:00:00 +0800","end":"2026-05-03 08:10:00 +0800",
		"duration":600,"source":"示例的Apple Watch",
		"route":[
			{"timestamp":1777940400,"latitude":30.25,"longitude":120.15},
			{"timestamp":1777940460.5,"latitude":30.26,"longitude":120.16}
		]}]}}`
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	rt := res.Workouts[0].Route
	if rt == nil || len(rt.Points) != 2 {
		t.Fatalf("unix 秒形态的轨迹应能解析，实际 %+v", rt)
	}
	if rt.Points[1].AtSec != 60 {
		t.Fatalf("相对秒数应为 60，实际 %d", rt.Points[1].AtSec)
	}
}

// route 换成对象、null、元素不是对象——都只该少一条轨迹，绝不该让整份导出 400。
func TestNormalizeWorkoutRouteToleratesOtherShapes(t *testing.T) {
	for _, form := range []string{`null`, `{}`, `"abc"`, `[1,2,3]`, `[]`} {
		raw := `{"data":{"workouts":[{
			"name":"户外 步行","start":"2026-05-03 08:00:00 +0800","end":"2026-05-03 08:10:00 +0800",
			"duration":600,"source":"示例的Apple Watch","route":` + form + `}]}}`
		res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
		if len(res.Workouts) != 1 {
			t.Fatalf("route=%s 不该让运动丢失（skipped=%d）", form, res.Skipped)
		}
		if res.Workouts[0].Route != nil {
			t.Fatalf("route=%s 不该解析出轨迹: %+v", form, res.Workouts[0].Route)
		}
	}
}
