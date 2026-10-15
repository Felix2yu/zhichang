package hae

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// 运动详情字段：此前 normalizeWorkout 只留名称/起止/时长/总能量/距离，
// 一年导出里的 110 次逐点心率、95 个 effortScore、最大平均心率、
// 室内外与地点全部丢弃。这里钉住「取到了、换算对了、形态变了也不炸」。
func TestNormalizeWorkoutDetailFields(t *testing.T) {
	raw := `{"data":{"workouts":[{
		"name":"户外 步行","start":"2026-10-14 09:12:30 +0800","end":"2026-10-14 09:33:05 +0800",
		"duration":1234.5,"source":"Apple Watch",
		"totalEnergy":{"qty":502.08,"units":"kJ"},
		"activeEnergyBurned":{"qty":334.72,"units":"kJ"},
		"distance":{"qty":1.8525,"units":"km"},
		"avgHeartRate":{"qty":118.5,"units":"bpm"},"maxHeartRate":{"qty":138,"units":"bpm"},
		"effortScore":{"qty":3,"units":"appleEffortScore"},
		"flightsClimbed":{"qty":2,"units":"count"},
		"temperature":{"qty":21.5,"units":"degC"},"humidity":{"qty":0.62,"units":"count"},
		"avgSpeed":{"qty":4.05,"units":"km"},"maxSpeed":{"qty":9.2,"units":"km"},
		"isIndoor":false,"location":"户外",
		"heartRateData":[
			{"date":"2026-10-14 09:12:00 +0800","Avg":96,"Min":96,"Max":96,"units":"bpm"},
			{"date":"2026-10-14 09:13:00 +0800","Avg":104,"Min":104,"Max":104,"units":"bpm"},
			{"date":"2026-10-14 09:14:00 +0800","Avg":112,"Min":112,"Max":112,"units":"bpm"}],
		"heartRateRecovery":[{"date":"2026-10-14 09:34:00 +0800","Avg":110,"units":"bpm"}],
		"splits":[{"index":1,"metrics":{"paceKilometre":{"qty":924,"units":"s/km"}}}]
	}]}}`

	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	if len(res.Workouts) != 1 {
		t.Fatalf("应有 1 条运动: %d skipped=%d", len(res.Workouts), res.Skipped)
	}
	w := res.Workouts[0]

	// 主动能量与总能量同口径（kJ → kcal），两者都要留：只留总能量会让运动页
	// 的消耗比健康 App 高出约三成。
	almost(t, "总能量", *w.TotalEnergy, 120.0, 1e-6)
	almost(t, "主动能量", *w.ActiveEnergy, 80.0, 1e-6)
	if w.ActiveUnit != "kcal" {
		t.Fatalf("主动能量单位应为 kcal，实际 %q", w.ActiveUnit)
	}
	if w.AvgHR == nil || *w.AvgHR != 118.5 || w.MaxHR == nil || *w.MaxHR != 138 {
		t.Fatalf("心率字段未取到: %v %v", w.AvgHR, w.MaxHR)
	}
	if w.EffortScore == nil || *w.EffortScore != 3 {
		t.Fatalf("effortScore 未取到: %v", w.EffortScore)
	}

	// 逐点心率存相对运动起点的秒数。第一点在 09:12:00，比运动起点早 30 秒：
	// 必须保留成 -30，而不是因为「不能为负」被丢掉（实测上游常早几十秒，一年丢 69 个点）。
	if len(w.HR) != 3 || w.HR[0].AtSec != -30 || w.HR[1].AtSec != 30 || w.HR[2].AtSec != 90 {
		t.Fatalf("心率曲线相对时轴错误: %+v", w.HR)
	}
	if w.HR[0].Value != 96 || w.HR[1].Value != 104 || w.HR[2].Value != 112 {
		t.Fatalf("心率取值错误: %+v", w.HR)
	}
	if len(w.Recovery) != 1 || w.Recovery[0].Value != 110 {
		t.Fatalf("恢复心率未取到: %+v", w.Recovery)
	}
	if len(w.Splits) == 0 || !json.Valid(w.Splits) {
		t.Fatalf("分段原文应原样保留且是合法 JSON: %s", w.Splits)
	}

	// meta：不参与聚合的上下文，速度连上游单位一起存（HAE 把 km/hr 写成 "km"，
	// 猜一个换算比留原文更危险）。
	if w.Meta["location"] != "户外" || w.Meta["indoor"] != false {
		t.Fatalf("地点/室内外未落进 meta: %v", w.Meta)
	}
	if w.Meta["avg_speed"] != 4.05 || w.Meta["avg_speed_unit"] != "km" {
		t.Fatalf("速度应带上游单位原样保留: %v", w.Meta)
	}
	if w.Meta["temperature"] != 21.5 || w.Meta["flights"] != 2.0 {
		t.Fatalf("温湿度/爬楼未落进 meta: %v", w.Meta)
	}
}

// 形态容错：这些字段任何一个换成别的形状，都不许让整份导出解析失败。
// 一次 UnmarshalTypeError 的代价是「34 个指标一条都入不了库」。
func TestNormalizeWorkoutDetailToleratesShapeDrift(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"isIndoor 是字符串", `"isIndoor":"0"`},
		{"isIndoor 是数字", `"isIndoor":1`},
		{"isIndoor 是对象", `"isIndoor":{"qty":1}`},
		{"location 是对象", `"location":{"name":"户外"}`},
		{"location 是数组", `"location":["户外"]`},
		{"心率是裸数值", `"avgHeartRate":118.5`},
		{"心率是字符串", `"avgHeartRate":"118.5"`},
		{"心率数组是逐点数组", `"avgHeartRate":[{"qty":1},{"qty":2}]`},
		{"splits 是 null", `"splits":null`},
		{"splits 是对象不是数组", `"splits":{"a":1}`},
		{"heartRateData 是对象", `"heartRateData":{"Avg":90}`},
		{"heartRateData 点缺时间", `"heartRateData":[{"Avg":90}]`},
		{"heartRateData 点缺数值", `"heartRateData":[{"date":"2026-10-14 09:13:00 +0800"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"data":{"workouts":[{"name":"步行","start":"2026-10-14 09:12:30 +0800",` +
				`"end":"2026-10-14 09:33:05 +0800","duration":1234.5,"source":"Apple Watch",` +
				tc.body + `}],"metrics":[{"name":"step_count","units":"count","data":[` +
				`{"date":"2026-10-14 09:00:00 +0800","qty":100,"source":"Apple Watch"}]}]}}`
			res, err := parseAndNormalize(raw)
			if err != nil {
				t.Fatalf("字段形态变化不该让整包失败: %v", err)
			}
			if len(res.Workouts) != 1 {
				t.Fatalf("运动仍应入库 1 条，实际 %d（skipped=%d）", len(res.Workouts), res.Skipped)
			}
			if len(res.Samples) != 1 {
				t.Fatalf("同一包里的指标必须照常入库，实际 %d 条", len(res.Samples))
			}
		})
	}
}

func parseAndNormalize(raw string) (*Result, error) {
	p, err := ParsePayload(newReader(raw))
	if err != nil {
		return nil, err
	}
	res := Normalize(p, metric.New(metric.Defaults()), time.UTC)
	return &res, nil
}

// LaxBool / Text 的取值口径：认得出真假的才算有值。
func TestLaxBoolAndText(t *testing.T) {
	for _, tc := range []struct {
		raw string
		set bool
		val bool
	}{
		{`true`, true, true},
		{`false`, true, false},
		{`"true"`, true, true},
		{`1`, true, true},
		{`0`, true, false},
		{`"YES"`, true, true},
		{`null`, false, false},
		{`"也许"`, false, false},
		{`{"qty":1}`, false, false},
	} {
		var b struct {
			V LaxBool `json:"v"`
		}
		if err := json.Unmarshal([]byte(`{"v":`+tc.raw+`}`), &b); err != nil {
			t.Fatalf("%s 不该报错: %v", tc.raw, err)
		}
		if b.V.Set != tc.set || b.V.Value != tc.val {
			t.Fatalf("%s → (%v,%v)，期望 (%v,%v)", tc.raw, b.V.Set, b.V.Value, tc.set, tc.val)
		}
	}
	for _, tc := range []struct{ raw, want string }{
		{`"户外"`, "户外"},
		{`12`, "12"},
		{`null`, ""},
		{`{"a":1}`, ""},
		{`["户外"]`, ""},
		{`true`, ""},
	} {
		var s struct {
			T Text `json:"t"`
		}
		if err := json.Unmarshal([]byte(`{"t":`+tc.raw+`}`), &s); err != nil {
			t.Fatalf("%s 不该报错: %v", tc.raw, err)
		}
		if string(s.T) != tc.want {
			t.Fatalf("%s → %q，期望 %q", tc.raw, string(s.T), tc.want)
		}
	}
}
