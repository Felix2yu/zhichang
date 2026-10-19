package hae

import (
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// 运动内部按分钟摊开的数组。此前结构体按聚合对象解析，而 Quantity 的容错逻辑对数组
// 返回「不认识」——一年 5202 个主动能量点、5174 个步数点、4924 个基础能量点、
// 4151 个步行+跑步距离点、159 个骑行距离点，一个也没进过库。
func TestNormalizeWorkoutMetricSeries(t *testing.T) {
	raw := `{"data":{"workouts":[{
		"name":"户外 步行","start":"2026-05-03 08:00:00 +0800","end":"2026-05-03 08:03:00 +0800",
		"duration":180,"source":"示例的Apple Watch",
		"stepCount":[{"date":"2026-05-03 08:00:00 +0800","qty":40,"units":"步"},
		             {"date":"2026-05-03 08:01:00 +0800","qty":60,"units":"步"}],
		"activeEnergy":[{"date":"2026-05-03 08:00:00 +0800","qty":4.184,"units":"kJ"}],
		"basalEnergy":[{"date":"2026-05-03 08:02:00 +0800","qty":8.368,"units":"kJ"}],
		"walkingAndRunningDistance":[{"date":"2026-05-03 08:01:00 +0800","qty":0.1,"units":"km"}]
	}]}}`

	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	w := res.Workouts[0]
	byMetric := map[string]sample.WorkoutSeries{}
	for _, s := range w.Series {
		byMetric[s.Metric] = s
	}

	// 名字必须是字典里的规范名（step_count 而不是上游的 stepCount），
	// 否则这张表与字典对不上，界面无从按同一口径渲染。
	if _, ok := metric.New(metric.Defaults()).Get("step_count"); !ok {
		t.Fatal("字典里没有 step_count")
	}
	if len(w.Series) != 4 {
		t.Fatalf("应有 4 条序列，实际 %d：%+v", len(w.Series), w.Series)
	}

	// 能量按字典口径换成 kcal：值与单位必须一起改，只改值会让展示层把 kJ 标成 kcal。
	ae := byMetric["active_energy"]
	if ae.Unit != "kcal" {
		t.Fatalf("主动能量应换算到字典单位 kcal，实际 %q", ae.Unit)
	}
	almost(t, "主动能量逐点", ae.Points[0].Value, 1.0, 1e-9)
	be := byMetric["basal_energy_burned"]
	if be.Unit != "kcal" || be.Points[0].Value != 2.0 {
		t.Fatalf("基础能量换算错误: %+v", be)
	}

	// 步数没有换算表可命中（上游单位是「步」这个写法），值原样、单位保留上游原文：
	// 未知单位绝不猜系数。
	sc := byMetric["step_count"]
	if sc.Points[0].Value != 40 || sc.Unit != "步" {
		t.Fatalf("步数应原样存并保留上游单位: %+v", sc)
	}

	// 时轴相对运动起点：08:02:00 是第 120 秒。
	if byMetric["basal_energy_burned"].Points[0].AtSec != 120 {
		t.Fatalf("相对秒数错误: %+v", byMetric["basal_energy_burned"].Points[0])
	}
}

// 逐点里少一个值或时间只丢那一个点——这里存的是显式秒数（不像心电按索引推时轴），
// 少一个点后面不会错位，因此没必要为它丢掉整段。
func TestNormalizeWorkoutMetricSeriesSkipsUnusablePoints(t *testing.T) {
	raw := `{"data":{"workouts":[{
		"name":"户外 步行","start":"2026-05-03 08:00:00 +0800","end":"2026-05-03 08:04:00 +0800",
		"duration":240,"source":"示例的Apple Watch",
		"cyclingDistance":[
			{"date":"2026-05-03 08:01:00 +0800","qty":0.2,"units":"km"},
			{"date":"2026-05-03 08:02:00 +0800","units":"km"},
			{"date":"完全不是时间","qty":0.4,"units":"km"},
			{"date":"2026-05-03 08:03:00 +0800","qty":0.5,"units":"km"}
		]}]}}`
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	var s sample.WorkoutSeries
	for _, x := range res.Workouts[0].Series {
		if x.Metric == "cycling_distance" {
			s = x
		}
	}
	if len(s.Points) != 2 {
		t.Fatalf("应只留下两个可用点，实际 %+v", s.Points)
	}
	if s.Points[0].AtSec != 60 || s.Points[1].AtSec != 180 {
		t.Fatalf("剩下的点相对秒数不该被压实: %+v", s.Points)
	}
}

// 数组换成对象、null、字符串——都只该少一条序列，不许让整份导出 400。
// 求和仍然要成立：旧版给的就是 {qty,units} 聚合对象。
func TestNormalizeWorkoutMetricSeriesToleratesOtherShapes(t *testing.T) {
	for _, form := range []string{`null`, `"abc"`, `[]`, `[null]`, `{"qty":5,"units":"kJ"}`} {
		raw := `{"data":{"workouts":[{
			"name":"户外 步行","start":"2026-05-03 08:00:00 +0800","end":"2026-05-03 08:10:00 +0800",
			"duration":600,"source":"示例的Apple Watch","stepCount":` + form + `}]}}`
		res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
		if len(res.Workouts) != 1 {
			t.Fatalf("stepCount=%s 不该让运动丢失（skipped=%d）", form, res.Skipped)
		}
		if form == `{"qty":5,"units":"kJ"}` {
			if got := res.Workouts[0].Meta["step_count"]; got != 5.0 {
				t.Fatalf("旧版聚合对象形态的总量应照收，实际 %v（meta=%v）", got, res.Workouts[0].Meta)
			}
		}
	}
}

// 上游并行给出的那些标量：最低心率、speed、步频、强度、预估 effortScore、运动编号。
// 它们不进聚合，但界面上「这次运动最快心率是多少」只能从这里回答。
func TestNormalizeWorkoutDetailScalarsLandInMeta(t *testing.T) {
	raw := `{"data":{"workouts":[{
		"id":"1847","name":"户外 步行","start":"2026-05-03 08:00:00 +0800","end":"2026-05-03 08:10:00 +0800",
		"duration":600,"source":"示例的Apple Watch",
		"avgHeartRate":{"qty":100,"units":"bpm"},"maxHeartRate":{"qty":130,"units":"bpm"},
		"heartRate":{"min":{"qty":72,"units":"bpm"},"avg":{"qty":100,"units":"bpm"},"max":{"qty":130,"units":"bpm"}},
		"avgSpeed":{"qty":4.2,"units":"km"},"speed":{"qty":3.8,"units":"km/hr"},
		"stepCadence":{"qty":96,"units":"count/min"},"intensity":{"qty":0.6,"units":"kcal/hr·kg"},
		"estimatedEffortScore":{"qty":2.5,"units":"appleEffortScore"}
	}]}}`
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	m := res.Workouts[0].Meta
	for key, want := range map[string]float64{
		"min_heart_rate": 72, "speed": 3.8, "step_cadence": 96, "intensity": 0.6,
		"estimated_effort_score": 2.5,
	} {
		if got := m[key]; got != want {
			t.Fatalf("%s 应为 %v，实际 %v（meta=%v）", key, want, got, m)
		}
	}
	// 单位随值一起存原文：上游把 km/hr 写成 "km" 这类事实在这里留着，换算交给核对。
	if m["min_heart_rate_unit"] != "bpm" || m["speed_unit"] != "km/hr" {
		t.Fatalf("标量的上游单位没跟着存: %v", m)
	}
	if m["upstream_id"] != "1847" {
		t.Fatalf("上游运动编号应留在 meta，实际 %v", m["upstream_id"])
	}
	// heartRate.avg 与 avgHeartRate 是同一量（实测 110 条全等），不该在 meta 里再存一份
	if _, dup := m["avg_heart_rate"]; dup {
		t.Fatalf("平均心率已有列，meta 里不该再存一份")
	}
}
