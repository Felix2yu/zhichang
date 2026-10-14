package hae

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/domain/tz"
)

func newReader(s string) *strings.Reader { return strings.NewReader(s) }

func loadSample(t *testing.T) *Payload {
	t.Helper()
	f, err := os.Open("../../../testdata/hae_export_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := ParsePayload(f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNormalizeCountsAndFields(t *testing.T) {
	p := loadSample(t)
	reg := metric.New(metric.Defaults())
	home, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("缺少时区数据")
	}
	res := Normalize(p, reg, home)

	// step_count 4 + heart_rate 2 + body_mass 1 + environmental_audio_exposure 1 = 8
	if len(res.Samples) != 8 {
		t.Fatalf("样本数应为 8，实际 %d", len(res.Samples))
	}
	if len(res.Sleep) != 1 {
		t.Fatalf("睡眠记录应为 1，实际 %d", len(res.Sleep))
	}
	if len(res.Workouts) != 1 {
		t.Fatalf("运动记录应为 1，实际 %d", len(res.Workouts))
	}
	// 样例里已没有字典外的指标：环境音量原先未登记，现已进字典。
	// 「未知指标被收集」这条口径由 normalize_edge_test.go 用构造出来的
	// 假名字覆盖——用「当下恰好未登记」的真实指标来测，它被登记后就会静默失效。
	if len(res.Unknown) != 0 {
		t.Fatalf("样例里不该还有未登记指标: %v", res.Unknown)
	}

	byMetric := map[string][]float64{}
	dates := map[string]string{}
	offsets := map[string]int{}
	for _, s := range res.Samples {
		byMetric[s.Metric] = append(byMetric[s.Metric], s.Value)
		dates[s.Metric] = s.LocalDate
		offsets[s.Metric] = s.TZOffsetMin
	}
	if len(byMetric["step_count"]) != 4 {
		t.Fatalf("step_count 应有 4 条，实际 %d", len(byMetric["step_count"]))
	}
	if got := byMetric["heart_rate"]; len(got) != 2 || got[0] != 72 || got[1] != 76 {
		t.Fatalf("心率应取 Avg，实际 %v", got)
	}
	if got := byMetric["body_mass"]; got[0] != 68.5 {
		t.Fatalf("体重错误: %v", got)
	}
	if dates["step_count"] != "2026-10-13" {
		t.Fatalf("步数本地日期错误: %s", dates["step_count"])
	}
	if offsets["step_count"] != 480 {
		t.Fatalf("偏移应为 480，实际 %d", offsets["step_count"])
	}
}

func TestHeartRateMinMax(t *testing.T) {
	p := loadSample(t)
	reg := metric.New(metric.Defaults())
	home := time.UTC
	res := Normalize(p, reg, home)
	for _, s := range res.Samples {
		if s.Metric != "heart_rate" {
			continue
		}
		if s.ValueMin == nil || *s.ValueMin != 60 {
			t.Fatalf("Min 未落到 value_min: %v", s.ValueMin)
		}
		if s.ValueMax == nil || *s.ValueMax != 88 {
			t.Fatalf("Max 未落到 value_max: %v", s.ValueMax)
		}
		break
	}
}

func TestSleepLocalDateFallsToPreviousNight(t *testing.T) {
	p := loadSample(t)
	reg := metric.New(metric.Defaults())
	home := time.UTC // 服务端 UTC，记录自带 +0800
	res := Normalize(p, reg, home)
	if len(res.Sleep) != 1 {
		t.Fatal("应有 1 条睡眠")
	}
	s := res.Sleep[0]
	if s.LocalDate != "2026-10-12" {
		t.Fatalf("睡眠应归属入睡日 2026-10-12，实际 %s", s.LocalDate)
	}
	if s.TotalSleepHr == nil || *s.TotalSleepHr != 7.5 {
		t.Fatalf("总睡眠时长错误: %v", s.TotalSleepHr)
	}
	if s.SleepStart == nil || s.SleepStart.UTC().Format(time.RFC3339) != "2026-10-12T15:30:00Z" {
		t.Fatalf("入睡时间错误: %v", s.SleepStart)
	}
}

func TestSegmentedSleepBecomesSamples(t *testing.T) {
	raw := `{"data":{"metrics":[{"name":"sleep_analysis","units":"hr","data":[
		{"startDate":"2026-10-12 23:30:00 +0800","endDate":"2026-10-13 03:00:00 +0800","qty":3.5,"value":"Core","source":"Apple Watch"},
		{"startDate":"2026-10-13 03:00:00 +0800","endDate":"2026-10-13 04:30:00 +0800","qty":1.5,"value":"Deep","source":"Apple Watch"}
	]}]}}`
	p, err := ParsePayload(newReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	res := Normalize(p, metric.New(metric.Defaults()), time.UTC)
	if len(res.Samples) != 2 {
		t.Fatalf("分段睡眠应生成 2 条样本，实际 %d", len(res.Samples))
	}
	if res.Samples[0].Meta["stage"] != "Core" {
		t.Fatalf("阶段未写入 meta: %v", res.Samples[0].Meta)
	}
	if res.Samples[1].LocalDate != "2026-10-13" {
		t.Fatalf("分段日期错误: %s", res.Samples[1].LocalDate)
	}
}

func TestBloodPressureSplit(t *testing.T) {
	raw := `{"data":{"metrics":[{"name":"blood_pressure","units":"mmHg","data":[
		{"date":"2026-10-13 08:00:00 +0800","systolic":120,"diastolic":80,"source":"iPhone"}
	]}]}}`
	p, err := ParsePayload(newReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	res := Normalize(p, metric.New(metric.Defaults()), time.UTC)
	if len(res.Samples) != 2 {
		t.Fatalf("血压应拆成 2 条，实际 %d", len(res.Samples))
	}
	got := map[string]float64{}
	for _, s := range res.Samples {
		got[s.Metric] = s.Value
	}
	if got["blood_pressure_systolic"] != 120 || got["blood_pressure_diastolic"] != 80 {
		t.Fatalf("血压取值错误: %v", got)
	}
}

func TestWorkoutNormalization(t *testing.T) {
	p := loadSample(t)
	res := Normalize(p, metric.New(metric.Defaults()), time.UTC)
	w := res.Workouts[0]
	if w.Name != "Walking" || w.DurationMin != 30 {
		t.Fatalf("运动归一化错误: %+v", w)
	}
	if w.TotalDistance == nil || *w.TotalDistance != 2.4 {
		t.Fatalf("距离错误: %v", w.TotalDistance)
	}
	if w.LocalDate != "2026-10-13" {
		t.Fatalf("运动日期错误: %s", w.LocalDate)
	}
}

func TestFloatToleratesStrings(t *testing.T) {
	raw := `{"data":{"metrics":[{"name":"step_count","units":"count","data":[
		{"date":"2026-10-13 08:00:00 +0800","qty":"1500","source":"iPhone"},
		{"date":"2026-10-13 09:00:00 +0800","qty":null,"source":"iPhone"}
	]}]}}`
	p, err := ParsePayload(newReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	res := Normalize(p, metric.New(metric.Defaults()), time.UTC)
	if len(res.Samples) != 1 || res.Samples[0].Value != 1500 {
		t.Fatalf("字符串数值解析错误: %+v", res.Samples)
	}
	if res.Skipped != 1 {
		t.Fatalf("空值应被跳过，skipped=%d", res.Skipped)
	}
}

func TestLocalDateCrossingTimezone(t *testing.T) {
	// 同一样本分别在 +0800 与 -0800 导出，local_date 必须各自按自带偏移计算。
	raw := `{"data":{"metrics":[{"name":"step_count","units":"count","data":[
		{"date":"2026-10-13 23:30:00 +0800","qty":100,"source":"iPhone"},
		{"date":"2026-10-13 23:30:00 -0800","qty":100,"source":"iPhone"}
	]}]}}`
	p, err := ParsePayload(newReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	res := Normalize(p, metric.New(metric.Defaults()), time.UTC)
	if len(res.Samples) != 2 {
		t.Fatal("应有 2 条样本")
	}
	if res.Samples[0].LocalDate != "2026-10-13" || res.Samples[1].LocalDate != "2026-10-13" {
		t.Fatalf("跨时区本地日期错误: %s / %s", res.Samples[0].LocalDate, res.Samples[1].LocalDate)
	}
	if res.Samples[0].StartTime.Equal(res.Samples[1].StartTime) {
		t.Fatal("不同偏移的同一挂钟时间不应相等（应各自转为 UTC）")
	}
	if tz.OffsetOrDefault(res.Samples[1].TZOffsetMin, res.Samples[1].StartTime, time.UTC) != -480 {
		t.Fatal("第二条记录偏移应为 -480")
	}
}
