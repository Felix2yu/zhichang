package hae

import (
	"math"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// 归一化侧的三处 v3 差异：单位换成 kJ/lb、duration 按秒给、距离换了字段名。
// 这些错误的共同点是「界面数值看着正常、其实错了 4~60 倍」，编译期与类型检查都拦不住，
// 只能靠这组数值断言钉死。

func almost(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s 应为 %v（容差 %v），实际 %v", name, want, tol, got)
	}
}

func TestNormalizeV3ValuesAndUnits(t *testing.T) {
	res := Normalize(loadV3(t), metric.New(metric.Defaults()), time.UTC)

	if len(res.Samples) != 5 {
		t.Fatalf("样本数应为 5（步数/活动能量/基础能量/心率/体重），实际 %d", len(res.Samples))
	}
	if len(res.Workouts) != 2 || len(res.Sleep) != 1 {
		t.Fatalf("运动/睡眠数量错误: workouts=%d sleep=%d", len(res.Workouts), len(res.Sleep))
	}
	if res.Skipped != 0 || len(res.Unknown) != 0 {
		t.Fatalf("v3 样例不该有丢弃或未登记指标: skipped=%d unknown=%v", res.Skipped, res.Unknown)
	}

	value := map[string]float64{}
	unit := map[string]string{}
	var hr *struct{ min, max *float64 }
	for i := range res.Samples {
		s := res.Samples[i]
		value[s.Metric], unit[s.Metric] = s.Value, s.Unit
		if s.Metric == "heart_rate" {
			hr = &struct{ min, max *float64 }{s.ValueMin, s.ValueMax}
		}
	}

	// 能量：payload 给 kJ，字典声明 kcal，入库必须换算成 kcal（否则虚高 4.184 倍）
	almost(t, "活动能量", value["active_energy"], 300.0, 1e-6)
	if unit["active_energy"] != "kcal" {
		t.Fatalf("活动能量单位应为 kcal，实际 %q", unit["active_energy"])
	}
	almost(t, "基础能量", value["basal_energy_burned"], 2100.0, 1e-6)
	if unit["basal_energy_burned"] != "kcal" {
		t.Fatalf("基础能量单位应为 kcal，实际 %q", unit["basal_energy_burned"])
	}
	// 体重：payload 给磅，字典声明 kg
	almost(t, "体重", value["body_mass"], 68.0388555, 1e-6)
	if unit["body_mass"] != "kg" {
		t.Fatalf("体重单位应为 kg，实际 %q", unit["body_mass"])
	}
	// 心率：count/min 与 bpm 只是标签差异，数值不动、原文单位保留
	almost(t, "心率", value["heart_rate"], 72.41860465116279, 1e-9)
	if unit["heart_rate"] != "count/min" {
		t.Fatalf("心率单位不该被改写（不是换算关系），实际 %q", unit["heart_rate"])
	}
	if hr == nil || hr.min == nil || *hr.min != 55 || hr.max == nil || *hr.max != 128 {
		t.Fatalf("心率极值未随主值一起落库: %+v", hr)
	}
	// 计数量：单位一致，原样
	almost(t, "步数", value["step_count"], 4321, 1e-9)
	if unit["step_count"] != "count" {
		t.Fatalf("步数单位应为 count，实际 %q", unit["step_count"])
	}

	// 睡眠：聚合形态进 sleep_sessions，字段逐个对齐
	sl := res.Sleep[0]
	if sl.LocalDate != "2026-10-12" {
		t.Fatalf("睡眠应归属入睡日 2026-10-12，实际 %s", sl.LocalDate)
	}
	if sl.TotalSleepHr == nil || *sl.TotalSleepHr != 7.5 || sl.InBedHr == nil || *sl.InBedHr != 8.0 {
		t.Fatalf("睡眠时长字段错误: %+v", sl)
	}
	if sl.SleepStart == nil || sl.SleepStart.UTC().Format(time.RFC3339) != "2026-10-12T15:30:00Z" {
		t.Fatalf("入睡时刻错误: %v", sl.SleepStart)
	}
}

func TestNormalizeV3Workout(t *testing.T) {
	res := Normalize(loadV3(t), metric.New(metric.Defaults()), time.UTC)

	// 新版形态：对象来源 + 秒级 duration + distance 字段 + kJ 能量
	nw := res.Workouts[0]
	if nw.Name != "户外 步行" || nw.Source != "小明的Apple\u00a0Watch" {
		t.Fatalf("运动名/来源解析错误: name=%q source=%q", nw.Name, nw.Source)
	}
	// duration 890.47 秒 → 14.84 分钟；按分钟存的旧代码会算出 1234 分钟
	almost(t, "新版运动时长(分钟)", nw.DurationMin, 20.575, 1e-6)
	if nw.TotalDistance == nil {
		t.Fatal("distance 字段必须被识别，否则运动页距离列全是「—」")
	}
	almost(t, "新版运动距离", *nw.TotalDistance, 1.8525, 1e-9)
	if nw.DistanceUnit != "km" {
		t.Fatalf("距离单位应为 km，实际 %q", nw.DistanceUnit)
	}
	if nw.TotalEnergy == nil {
		t.Fatal("总能耗缺失")
	}
	almost(t, "新版运动能耗", *nw.TotalEnergy, 120.0, 1e-6)
	if nw.EnergyUnit != "kcal" {
		t.Fatalf("运动能耗单位应为 kcal，实际 %q", nw.EnergyUnit)
	}
	if nw.LocalDate != "2026-10-14" || nw.TZOffsetMin != 480 {
		t.Fatalf("运动归属必须用记录自带偏移: date=%s offset=%d", nw.LocalDate, nw.TZOffsetMin)
	}

	// 旧版形态同文件共存：分钟级 duration + totalDistance，仍按原口径入库
	old := res.Workouts[1]
	almost(t, "旧版运动时长(分钟)", old.DurationMin, 30, 1e-9)
	if old.TotalDistance == nil {
		t.Fatal("旧版 totalDistance 缺失")
	}
	almost(t, "旧版运动距离", *old.TotalDistance, 2.4, 1e-9)
	if old.TotalEnergy == nil || *old.TotalEnergy != 120 || old.EnergyUnit != "kcal" {
		t.Fatalf("旧版运动能耗错误: %v %q", old.TotalEnergy, old.EnergyUnit)
	}
}

// workoutDurationMin 是纯函数，直接把两版的判据全锁在表里。
// 判错的代价：秒当分钟 → 15 分钟步行显示成 1234 分钟；分钟当秒 → 30 分钟显示成 0.5 分钟。
func TestWorkoutDurationMinUnitDetection(t *testing.T) {
	tests := []struct {
		name   string
		raw    float64
		span   time.Duration
		want   float64
		reason string
	}{
		{"新版按秒给（890.47 秒对 891 秒跨度）", 1234.5, 891 * time.Second, 20.575,
			"隐含秒数几乎等于跨度，判为秒"},
		{"旧版按分钟给（30 对 30 分钟跨度）", 30, 30 * time.Minute, 30,
			"隐含秒数（1800）等于跨度，判为分钟"},
		{"旧版按分钟且中途有停顿", 30, 90 * time.Minute, 30,
			"跨度 90 分钟仍更接近「30 分钟」而不是「30 秒」"},
		{"新版按秒且中途有停顿", 1800, 90 * time.Minute, 30,
			"跨度 90 分钟更接近「1800 秒」而不是「1800 分钟」"},
		{"一分钟的新版写法", 60, time.Minute, 1, "秒解释与跨度完全吻合"},
		{"一分钟的旧版写法", 1, time.Minute, 1, "分钟解释与跨度完全吻合"},
		{"缺 end（跨度为 0）回落分钟", 10, 0, 10,
			"没有起止差值可对照时保持老行为，不能凭空按秒处理"},
		{"跨度为负回落分钟", 10, -time.Minute, 10, "脏数据不参与判定"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workoutDurationMin(tt.raw, tt.span)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("应为 %v 分钟，实际 %v（%s）", tt.want, got, tt.reason)
			}
		})
	}
}

func TestNormalizeWorkoutDistanceFieldFallback(t *testing.T) {
	reg := metric.New(metric.Defaults())
	tests := []struct {
		name      string
		workout   string
		wantValue *float64
		wantUnit  string
	}{
		{
			name:      "只有旧版 totalDistance",
			workout:   `{"name":"Walking","start":"2026-10-13 08:00:00 +0800","totalDistance":{"qty":2.4,"units":"km"},"source":"Apple Watch"}`,
			wantValue: ptr(2.4), wantUnit: "km",
		},
		{
			name:      "只有新版 distance",
			workout:   `{"name":"Walking","start":"2026-10-13 08:00:00 +0800","distance":{"qty":3.2,"units":"km"},"source":"Apple Watch"}`,
			wantValue: ptr(3.2), wantUnit: "km",
		},
		{
			name:      "两者都在时以旧版字段为准",
			workout:   `{"name":"Walking","start":"2026-10-13 08:00:00 +0800","totalDistance":{"qty":2.4,"units":"km"},"distance":{"qty":3.2,"units":"km"},"source":"Apple Watch"}`,
			wantValue: ptr(2.4), wantUnit: "km",
		},
		{
			name:      "英制距离换算成公里",
			workout:   `{"name":"Running","start":"2026-10-13 08:00:00 +0800","distance":{"qty":2,"units":"mi"},"source":"Apple Watch"}`,
			wantValue: ptr(3.218688), wantUnit: "km",
		},
		{
			name:      "distance 是逐点数组时忽略不报错",
			workout:   `{"name":"Walking","start":"2026-10-13 08:00:00 +0800","distance":[{"qty":0.02,"units":"km"}],"source":"Apple Watch"}`,
			wantValue: nil, wantUnit: "",
		},
		{
			name:      "两个字段都没有",
			workout:   `{"name":"Yoga","start":"2026-10-13 08:00:00 +0800","source":"Apple Watch"}`,
			wantValue: nil, wantUnit: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := mustNormalize(t, reg, time.UTC, `{"data":{"workouts":[`+tt.workout+`]}}`)
			if len(res.Workouts) != 1 {
				t.Fatalf("应有 1 条运动，实际 %d（skipped=%d）", len(res.Workouts), res.Skipped)
			}
			w := res.Workouts[0]
			if !samePtr(w.TotalDistance, tt.wantValue) {
				t.Fatalf("距离期望 %v，实际 %v", deref(tt.wantValue), deref(w.TotalDistance))
			}
			if w.DistanceUnit != tt.wantUnit {
				t.Fatalf("距离单位期望 %q，实际 %q", tt.wantUnit, w.DistanceUnit)
			}
		})
	}
}

func TestNormalizeWorkoutEnergyTargetsKcal(t *testing.T) {
	// 运动能耗统一到 kcal：运动页的「总消耗」卡按 kcal 展示，混着 kJ 会让总量虚高 4.18 倍。
	reg := metric.New(metric.Defaults())
	res := mustNormalize(t, reg, time.UTC, `{"data":{"workouts":[
		{"name":"Cycling","start":"2026-10-13 08:00:00 +0800","totalEnergy":{"qty":418.4,"units":"kJ"},"source":"Apple Watch"},
		{"name":"Yoga","start":"2026-10-13 09:00:00 +0800","totalEnergy":{"qty":100,"units":"kcal"},"source":"Apple Watch"}
	]}}`)
	if len(res.Workouts) != 2 {
		t.Fatalf("应有 2 条运动，实际 %d", len(res.Workouts))
	}
	almost(t, "kJ 能耗换算", *res.Workouts[0].TotalEnergy, 100, 1e-9)
	if res.Workouts[0].EnergyUnit != "kcal" {
		t.Fatalf("单位应为 kcal，实际 %q", res.Workouts[0].EnergyUnit)
	}
	almost(t, "kcal 能耗原样", *res.Workouts[1].TotalEnergy, 100, 1e-9)
	if res.Workouts[1].EnergyUnit != "kcal" {
		t.Fatalf("单位应为 kcal，实际 %q", res.Workouts[1].EnergyUnit)
	}
}
