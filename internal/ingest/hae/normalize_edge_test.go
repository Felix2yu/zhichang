package hae

import (
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// 归一化是「脏 payload 进来、可用样本出去」的唯一一道闸门，
// 因此这里的用例集中在三件事：取值字段选择、时间解析降级、以及被丢弃记录必须计入 Skipped
// （Skipped 是用户排查「为什么数据少了」的唯一线索）。

func mustNormalize(t *testing.T, reg *metric.Registry, home *time.Location, raw string) Result {
	t.Helper()
	p, err := ParsePayload(newReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return Normalize(p, reg, home)
}

// 一条覆盖全部取值字段的时间点（qty=1 Min=2 Avg=3 Max=4 totalSleep=5 systolic=6 diastolic=7）。
const fullPoint = `{"date":"2026-10-13 08:00:00 +0800","qty":1,"Min":2,"Avg":3,"Max":4,` +
	`"totalSleep":5,"systolic":6,"diastolic":7,"source":"iPhone"}`

func oneMetric(name, point string) string {
	return `{"data":{"metrics":[{"name":"` + name + `","units":"u","data":[` + point + `]}]}}`
}

func oneSleepMetric(point string) string {
	return oneMetric("sleep_analysis", point)
}

// regOf 构造最小字典：字典里 ValueField 决定从哪个字段取值，测试用它来驱动 pickValue 的各个分支。
func regOf(specs ...[2]string) *metric.Registry {
	defs := make([]metric.Def, 0, len(specs))
	for _, s := range specs {
		defs = append(defs, metric.Def{Metric: s[0], ValueField: s[1], Unit: "u", Enabled: true})
	}
	return metric.New(defs)
}

func TestNormalizeValueFieldSelection(t *testing.T) {
	noMinMax := `{"date":"2026-10-13 08:00:00 +0800","qty":9,"source":"iPhone"}`
	onlyQty := `{"date":"2026-10-13 08:00:00 +0800","qty":1,"source":"iPhone"}`
	tests := []struct {
		name         string
		metricName   string
		field        string
		point        string
		wantValue    float64
		wantSamples  int
		wantSkipped  int
		unregistered bool // true 表示字典里根本没有这个指标
	}{
		{"qty", "t_qty", "qty", fullPoint, 1, 1, 0, false},
		{"Avg 命中 Avg", "t_avg", "Avg", fullPoint, 3, 1, 0, false},
		{"Avg 缺失回落 qty", "t_avg2", "Avg", noMinMax, 9, 1, 0, false},
		{"Min", "t_min", "Min", fullPoint, 2, 1, 0, false},
		{"Max", "t_max", "Max", fullPoint, 4, 1, 0, false},
		{"totalSleep", "t_sleep_field", "totalSleep", fullPoint, 5, 1, 0, false},
		{"systolic", "t_sys", "systolic", fullPoint, 6, 1, 0, false},
		{"diastolic", "t_dia", "diastolic", fullPoint, 7, 1, 0, false},
		{"字典写了未知字段按 qty", "t_odd", "unexpected", fullPoint, 1, 1, 0, false},
		{"未登记指标按 qty 并计入 unknown", "t_unknown", "", onlyQty, 1, 1, 0, true},
		{"声明字段缺失则该点丢弃", "t_min_only", "Min", onlyQty, 0, 0, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := regOf()
			if !tt.unregistered {
				reg = regOf([2]string{tt.metricName, tt.field})
			}
			res := mustNormalize(t, reg, time.UTC, oneMetric(tt.metricName, tt.point))
			if len(res.Samples) != tt.wantSamples {
				t.Fatalf("样本数应为 %d，实际 %d: %+v", tt.wantSamples, len(res.Samples), res.Samples)
			}
			if res.Skipped != tt.wantSkipped {
				t.Fatalf("Skipped 应为 %d，实际 %d", tt.wantSkipped, res.Skipped)
			}
			if tt.wantSamples == 1 && res.Samples[0].Value != tt.wantValue {
				t.Fatalf("取值应为 %v（字典 ValueField=%q），实际 %v", tt.wantValue, tt.field, res.Samples[0].Value)
			}
			if tt.unregistered && (len(res.Unknown) != 1 || res.Unknown[0] != tt.metricName) {
				t.Fatalf("未登记指标必须出现在 unknown 里，实际 %v", res.Unknown)
			}
		})
	}
}

func TestNormalizeUsesDateOverStartDate(t *testing.T) {
	// HAE 聚合导出同时给 date 与 startDate 时以 date 为准；只有 startDate 时降级使用它。
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, oneMetric("step_count",
		`{"date":"2026-10-13 08:00:00 +0800","startDate":"2026-10-13 02:00:00 +0800","endDate":"2026-10-13 09:00:00 +0800","qty":10,"source":"iPhone"}`))
	if len(res.Samples) != 1 {
		t.Fatal("应有 1 条样本")
	}
	s := res.Samples[0]
	if want := "2026-10-13T00:00:00Z"; s.StartTime.UTC().Format(time.RFC3339) != want {
		t.Fatalf("start 应取 date，实际 %s", s.StartTime)
	}
	if want := "2026-10-13T01:00:00Z"; s.EndTime.UTC().Format(time.RFC3339) != want {
		t.Fatalf("end 应取 endDate，实际 %s", s.EndTime)
	}

	res = mustNormalize(t, metric.New(metric.Defaults()), time.UTC, oneMetric("step_count",
		`{"startDate":"2026-10-13 08:00:00 +0800","qty":10,"source":"iPhone"}`))
	if len(res.Samples) != 1 {
		t.Fatalf("缺 date 时应回落到 startDate，实际样本 %d", len(res.Samples))
	}
	if res.Samples[0].LocalDate != "2026-10-13" || !res.Samples[0].EndTime.Equal(res.Samples[0].StartTime) {
		t.Fatalf("无 endDate 时 end 必须等于 start（否则区间聚合会算出负时长）: %+v", res.Samples[0])
	}
}

func TestNormalizeTimeFormatVariants(t *testing.T) {
	home, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("缺少时区数据")
	}
	tests := []struct {
		name        string
		date        string // 含点号的写法会直接作为 JSON 片段插入
		wantStart   string // UTC RFC3339
		wantLocal   string
		wantOffset  int
		wantSkipped bool
	}{
		{"带偏移", `"2026-10-13 08:00:00 +0800"`, "2026-10-13T00:00:00Z", "2026-10-13", 480, false},
		{"负偏移", `"2026-10-13 08:00:00 -0500"`, "2026-10-13T13:00:00Z", "2026-10-13", -300, false},
		{"无偏移按 home", `"2026-10-13 08:00:00"`, "2026-10-13T00:00:00Z", "2026-10-13", 480, false},
		{"纯日期按 home 午夜", `"2026-10-13"`, "2026-10-12T16:00:00Z", "2026-10-13", 480, false},
		{"值里再套引号（HAE 老版本）", `"\"2026-10-13 08:00:00 +0800\""`, "2026-10-13T00:00:00Z", "2026-10-13", 480, false},
		{"RFC3339 不在接受格式内", `"2026-10-13T08:00:00+08:00"`, "", "", 0, true},
		{"乱码", `"not-a-time"`, "", "", 0, true},
		{"完全没有时间字段", "", "", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			point := `{"qty":10,"source":"iPhone"}`
			if tt.date != "" {
				point = `{"date":` + tt.date + `,"qty":10,"source":"iPhone"}`
			}
			res := mustNormalize(t, metric.New(metric.Defaults()), home, oneMetric("step_count", point))
			if tt.wantSkipped {
				if len(res.Samples) != 0 || res.Skipped != 1 {
					t.Fatalf("不可解析的时间必须被丢弃并计数，实际 samples=%d skipped=%d", len(res.Samples), res.Skipped)
				}
				return
			}
			if len(res.Samples) != 1 {
				t.Fatalf("应有 1 条样本: %+v", res.Samples)
			}
			s := res.Samples[0]
			if got := s.StartTime.UTC().Format(time.RFC3339); got != tt.wantStart {
				t.Fatalf("UTC 起点应为 %s，实际 %s", tt.wantStart, got)
			}
			if s.LocalDate != tt.wantLocal || s.TZOffsetMin != tt.wantOffset {
				t.Fatalf("本地日期/偏移应为 %s/%d，实际 %s/%d", tt.wantLocal, tt.wantOffset, s.LocalDate, s.TZOffsetMin)
			}
		})
	}
}

func TestNormalizeSkipsEmptyMetricName(t *testing.T) {
	// 无名指标既不出样本也不进 unknown：否则每次导出都会刷出一串无意义告警。
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC,
		`{"data":{"metrics":[{"name":"","units":"x","data":[{"date":"2026-10-13 08:00:00 +0800","qty":1}]}]}}`)
	if len(res.Samples) != 0 || res.Skipped != 0 || len(res.Unknown) != 0 {
		t.Fatalf("无名指标应被整体忽略，实际 samples=%d skipped=%d unknown=%v", len(res.Samples), res.Skipped, res.Unknown)
	}
}

func TestNormalizeUnknownMetricsDeduped(t *testing.T) {
	// unknown 列表要去重：一份导出里同名指标出现两次只报一次，否则端点响应会越推越长。
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC,
		`{"data":{"metrics":[
			{"name":"weird_a","units":"x","data":[{"date":"2026-10-13 08:00:00 +0800","qty":1}]},
			{"name":"weird_a","units":"x","data":[{"date":"2026-10-13 09:00:00 +0800","qty":2}]},
			{"name":"weird_b","units":"x","data":[{"date":"2026-10-13 10:00:00 +0800","qty":3}]}
		]}}`)
	if len(res.Unknown) != 2 {
		t.Fatalf("未知指标应去重为 2 条，实际 %v", res.Unknown)
	}
	got := map[string]bool{}
	for _, u := range res.Unknown {
		got[u] = true
	}
	if !got["weird_a"] || !got["weird_b"] {
		t.Fatalf("未知指标内容错误: %v", res.Unknown)
	}
}

func TestNormalizeMinMaxEnvelope(t *testing.T) {
	// 心率这类区间指标：Min/Max 单独存在时只填对应的一侧，缺的一侧必须留在 DB 为 NULL
	// （写 0 会让日内 min/max 聚合得出假的极值）。
	tests := []struct {
		name    string
		point   string
		wantMin *float64
		wantMax *float64
		wantVal float64
	}{
		{"只有 Min", `{"date":"2026-10-13 08:00:00 +0800","Min":55,"Avg":70,"source":"iPhone"}`, ptr(55), nil, 70},
		{"只有 Max", `{"date":"2026-10-13 08:00:00 +0800","Max":95,"Avg":70,"source":"iPhone"}`, nil, ptr(95), 70},
		{"只有 Avg 时两侧都为空", `{"date":"2026-10-13 08:00:00 +0800","Avg":70,"source":"iPhone"}`, nil, nil, 70},
		{"全缺时回落 qty", `{"date":"2026-10-13 08:00:00 +0800","qty":66,"source":"iPhone"}`, nil, nil, 66},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, oneMetric("heart_rate", tt.point))
			if len(res.Samples) != 1 {
				t.Fatalf("应有 1 条样本，实际 %+v", res.Samples)
			}
			s := res.Samples[0]
			if s.Value != tt.wantVal {
				t.Fatalf("value 应取 %v，实际 %v", tt.wantVal, s.Value)
			}
			if !samePtr(s.ValueMin, tt.wantMin) {
				t.Fatalf("value_min 期望 %v，实际 %v", deref(tt.wantMin), deref(s.ValueMin))
			}
			if !samePtr(s.ValueMax, tt.wantMax) {
				t.Fatalf("value_max 期望 %v，实际 %v", deref(tt.wantMax), deref(s.ValueMax))
			}
		})
	}
}

func TestNormalizeBuildMeta(t *testing.T) {
	// mealTime / reason / value 是血糖、正念、类别指标的解释信息；
	// 一条都没有时 meta 必须是 nil，空 JSON 会让前端渲染出 "{}" 噪声。
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, oneMetric("blood_glucose",
		`{"date":"2026-10-13 08:00:00 +0800","qty":102,"mealTime":"breakfast","reason":"演练","value":"偏高","source":"iPhone"}`))
	if len(res.Samples) != 1 {
		t.Fatal("应有 1 条样本")
	}
	m := res.Samples[0].Meta
	if m == nil || m["meal_time"] != "breakfast" || m["reason"] != "演练" || m["value"] != "偏高" {
		t.Fatalf("附加字段未落进 meta: %v", m)
	}

	res = mustNormalize(t, metric.New(metric.Defaults()), time.UTC, oneMetric("blood_glucose",
		`{"date":"2026-10-13 08:00:00 +0800","qty":102,"source":"iPhone"}`))
	if res.Samples[0].Meta != nil {
		t.Fatalf("无附加字段时 meta 必须为 nil，实际 %v", res.Samples[0].Meta)
	}
	// unit 原样取自 payload 的 units 字段，不被字典口径改写。
	if res.Samples[0].Unit != "u" {
		t.Fatalf("unit 应保留 payload 原文，实际 %q", res.Samples[0].Unit)
	}
}

func TestNormalizeSleepAnchorFallback(t *testing.T) {
	// 睡眠归属日依次取 sleepStart → inBedStart → date：HAE 的聚合睡眠常常只给 inBed 边界或纯日期，
	// 若一律按 date 归属，跨零点的那晚会算错一天。
	tests := []struct {
		name        string
		point       string
		wantDate    string
		wantSkipped bool
	}{
		{"只有 inBedStart", `{"inBedStart":"2026-10-12 23:10:00 +0800","inBedEnd":"2026-10-13 07:10:00 +0800","totalSleep":7,"source":"Apple Watch"}`, "2026-10-12", false},
		{"只有 date", `{"date":"2026-10-13","totalSleep":6.5,"source":"Apple Watch"}`, "2026-10-13", false},
		{"只有 asleep 也算聚合形态", `{"date":"2026-10-13","asleep":6.0,"source":"Apple Watch"}`, "2026-10-13", false},
		{"锚点不可解析", `{"sleepStart":"昨天半夜","totalSleep":6.5,"source":"Apple Watch"}`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, oneSleepMetric(tt.point))
			if tt.wantSkipped {
				if len(res.Sleep) != 0 || res.Skipped != 1 {
					t.Fatalf("不可解析的睡眠应丢弃，实际 sleep=%d skipped=%d", len(res.Sleep), res.Skipped)
				}
				return
			}
			if len(res.Sleep) != 1 {
				t.Fatalf("应有 1 条睡眠，实际 %d（samples=%d）", len(res.Sleep), len(res.Samples))
			}
			if res.Sleep[0].LocalDate != tt.wantDate {
				t.Fatalf("归属日应为 %s，实际 %s", tt.wantDate, res.Sleep[0].LocalDate)
			}
		})
	}
}

func TestNormalizeSleepPartialStages(t *testing.T) {
	// 缺失的阶段必须留 NULL 而不是 0：0 会被当成「这段睡眠确实为 0」参与求和。
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, oneSleepMetric(
		`{"sleepStart":"2026-10-12 23:30:00 +0800","sleepEnd":"2026-10-13 07:00:00 +0800","totalSleep":7.5,"core":3.5,"source":"Apple Watch"}`))
	if len(res.Sleep) != 1 {
		t.Fatal("应有 1 条睡眠")
	}
	s := res.Sleep[0]
	if s.CoreHr == nil || *s.CoreHr != 3.5 {
		t.Fatalf("core 解析错误: %v", s.CoreHr)
	}
	for name, v := range map[string]*float64{"deep": s.DeepHr, "rem": s.RemHr, "awake": s.AwakeHr, "inBed": s.InBedHr, "asleep": s.AsleepHr} {
		if v != nil {
			t.Fatalf("未提供的阶段 %s 必须为 nil，实际 %v", name, *v)
		}
	}
	if s.InBedStart != nil || s.InBedEnd != nil {
		t.Fatalf("未提供的 inBed 边界应为 nil: %v / %v", s.InBedStart, s.InBedEnd)
	}
	if s.TZOffsetMin != 480 {
		t.Fatalf("偏移应取记录自带的 +0800，实际 %d", s.TZOffsetMin)
	}
	if s.SleepStart == nil || s.SleepStart.UTC().Format(time.RFC3339) != "2026-10-12T15:30:00Z" {
		t.Fatalf("入睡时刻应转为 UTC，实际 %v", s.SleepStart)
	}
}

func TestNormalizeSegmentedSleepSkip(t *testing.T) {
	// 分段形态（每条是一个睡眠阶段）缺 qty 或缺 startDate 都无法定位区间，必须丢弃并计数。
	tests := []struct {
		name  string
		point string
	}{
		{"缺 qty", `{"startDate":"2026-10-12 23:30:00 +0800","endDate":"2026-10-13 03:00:00 +0800","value":"Core"}`},
		{"缺 startDate", `{"endDate":"2026-10-13 03:00:00 +0800","qty":3.5,"value":"Core"}`},
		{"startDate 不可解析", `{"startDate":"昨天半夜","qty":3.5,"value":"Core"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, oneSleepMetric(tt.point))
			if len(res.Samples) != 0 || len(res.Sleep) != 0 || res.Skipped != 1 {
				t.Fatalf("非法分段睡眠应丢弃，实际 samples=%d sleep=%d skipped=%d", len(res.Samples), len(res.Sleep), res.Skipped)
			}
		})
	}
}

func TestNormalizeSegmentedSleepWithoutStage(t *testing.T) {
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, oneSleepMetric(
		`{"startDate":"2026-10-12 23:30:00 +0800","endDate":"2026-10-13 03:00:00 +0800","qty":3.5,"source":"Apple Watch"}`))
	if len(res.Samples) != 1 {
		t.Fatal("应有 1 条分段样本")
	}
	if res.Samples[0].Meta != nil {
		t.Fatalf("没有 value 时不该造 stage meta: %v", res.Samples[0].Meta)
	}
	if res.Samples[0].Metric != "sleep_analysis" || res.Samples[0].Value != 3.5 {
		t.Fatalf("分段睡眠应以小时数入 sleep_analysis: %+v", res.Samples[0])
	}
}

func TestNormalizeBloodPressureEdges(t *testing.T) {
	reg := metric.New(metric.Defaults())
	// 只有收缩压（手动录入常见）：只出一条，不能补一个 0 舒张压。
	res := mustNormalize(t, reg, time.UTC, oneMetric("blood_pressure",
		`{"date":"2026-10-13 08:00:00 +0800","systolic":120,"source":"iPhone"}`))
	if len(res.Samples) != 1 || res.Samples[0].Metric != "blood_pressure_systolic" {
		t.Fatalf("只该出一条收缩压: %+v", res.Samples)
	}
	if res.Samples[0].Unit != "u" {
		t.Fatalf("unit 应保留 payload 原文，实际 %q", res.Samples[0].Unit)
	}

	// 时间不可解析时收缩压与舒张压各计一次丢弃（两条独立记录）。
	res = mustNormalize(t, reg, time.UTC, oneMetric("blood_pressure",
		`{"date":"昨天半夜","systolic":120,"diastolic":80,"source":"iPhone"}`))
	if len(res.Samples) != 0 || res.Skipped != 2 {
		t.Fatalf("血压两条都应丢弃，实际 samples=%d skipped=%d", len(res.Samples), res.Skipped)
	}

	// blood_pressure 是复合指标名，本身不进 unknown 列表。
	res = mustNormalize(t, reg, time.UTC, oneMetric("blood_pressure",
		`{"date":"2026-10-13 08:00:00 +0800","systolic":120,"diastolic":80,"source":"iPhone"}`))
	if len(res.Unknown) != 0 {
		t.Fatalf("blood_pressure 不该被判为未知指标: %v", res.Unknown)
	}
}

func TestNormalizeWorkoutEdges(t *testing.T) {
	reg := metric.New(metric.Defaults())
	tests := []struct {
		name          string
		workout       string
		wantOK        bool
		wantDuration  float64
		wantStart     string
		wantEnd       string
		wantEnergyNil bool
	}{
		{
			name:    "带 duration 字段",
			workout: `{"name":"Walking","start":"2026-10-13 08:00:00 +0800","end":"2026-10-13 08:45:00 +0800","duration":30,"totalEnergy":{"qty":120,"units":"kcal"},"totalDistance":{"qty":2.4,"units":"km"},"source":"Apple Watch"}`,
			wantOK:  true, wantDuration: 30, wantStart: "2026-10-13T00:00:00Z", wantEnd: "2026-10-13T00:45:00Z",
		},
		{
			name:    "无 duration 时按起止差值推算",
			workout: `{"name":"Running","start":"2026-10-13 08:00:00 +0800","end":"2026-10-13 08:45:00 +0800","source":"Apple Watch"}`,
			wantOK:  true, wantDuration: 45, wantStart: "2026-10-13T00:00:00Z", wantEnd: "2026-10-13T00:45:00Z", wantEnergyNil: true,
		},
		{
			name:    "end 不可解析时退化为瞬时运动",
			workout: `{"name":"Yoga","start":"2026-10-13 08:00:00 +0800","end":"随便写的","source":"iPhone"}`,
			wantOK:  true, wantDuration: 0, wantStart: "2026-10-13T00:00:00Z", wantEnd: "2026-10-13T00:00:00Z", wantEnergyNil: true,
		},
		{
			name:    "start 不可解析整条丢弃",
			workout: `{"name":"Bad","start":"昨天半夜","end":"2026-10-13 08:45:00 +0800","source":"iPhone"}`,
			wantOK:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := mustNormalize(t, reg, time.UTC, `{"data":{"workouts":[`+tt.workout+`]}}`)
			if !tt.wantOK {
				if len(res.Workouts) != 0 || res.Skipped != 1 {
					t.Fatalf("非法运动应丢弃并计数，实际 workouts=%d skipped=%d", len(res.Workouts), res.Skipped)
				}
				return
			}
			if len(res.Workouts) != 1 {
				t.Fatalf("应有 1 条运动，实际 %d skipped=%d", len(res.Workouts), res.Skipped)
			}
			w := res.Workouts[0]
			if got := w.StartTime.UTC().Format(time.RFC3339); got != tt.wantStart {
				t.Fatalf("start 应为 %s，实际 %s", tt.wantStart, got)
			}
			if got := w.EndTime.UTC().Format(time.RFC3339); got != tt.wantEnd {
				t.Fatalf("end 应为 %s，实际 %s", tt.wantEnd, got)
			}
			if w.DurationMin != tt.wantDuration {
				t.Fatalf("时长应为 %v，实际 %v", tt.wantDuration, w.DurationMin)
			}
			if (w.TotalEnergy == nil) != tt.wantEnergyNil {
				t.Fatalf("总能耗期望 nil=%v，实际 %v", tt.wantEnergyNil, w.TotalEnergy)
			}
			if w.TZOffsetMin != 480 || w.LocalDate != "2026-10-13" {
				t.Fatalf("运动归属必须用记录自带偏移: offset=%d date=%s", w.TZOffsetMin, w.LocalDate)
			}
		})
	}
}

func TestNormalizeWorkoutNoSourceStillNormalizes(t *testing.T) {
	// source 缺失不致命（落库时写空串），但字典与日期口径不能因此改变。
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC,
		`{"data":{"workouts":[{"name":"Walking","start":"2026-10-13 08:00:00 +0800","duration":10}]}}`)
	if len(res.Workouts) != 1 || res.Workouts[0].Source != "" {
		t.Fatalf("无来源运动仍应入库: %+v", res.Workouts)
	}
	if res.Workouts[0].DurationMin != 10 {
		t.Fatalf("duration 字段应优先于起止差值: %v", res.Workouts[0].DurationMin)
	}
	if res.Workouts[0].TotalDistance != nil || res.Workouts[0].DistanceUnit != "" {
		t.Fatalf("未提供距离时不该造值: %v / %q", res.Workouts[0].TotalDistance, res.Workouts[0].DistanceUnit)
	}
}

func TestNormalizeOnlySleepProducesNoSamples(t *testing.T) {
	// 纯睡眠导出：聚合形态只进 sleep_sessions，不会污染 samples，
	// 因此 received/入库计数走的是不同口径，这条断言防止以后误把睡眠当样本双计。
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, oneSleepMetric(
		`{"date":"2026-10-13","totalSleep":7,"sleepStart":"2026-10-12 23:30:00 +0800","source":"Apple Watch"}`))
	if len(res.Samples) != 0 || len(res.Sleep) != 1 {
		t.Fatalf("sleep 不应生成样本: samples=%d sleep=%d", len(res.Samples), len(res.Sleep))
	}
}

func ptr(f float64) *float64 { return &f }

func samePtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func deref(f *float64) any {
	if f == nil {
		return "nil"
	}
	return *f
}
