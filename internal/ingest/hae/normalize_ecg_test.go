package hae

import (
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/ecg"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// 本文件锁心电的归一化口径。本版只落元数据（一行一次测量），不解析波形。
// 最容易出错的是判定文本：它是**本地化**的，必须走 ecg.Classify 而不是按整串比。

func ecgPayload(body string) string {
	return `{"data":{"ecg":[` + body + `]}}`
}

// 一段最小但形态完整的心电记录（时间带偏移，与真实导出一致）。
const ecgBase = `"start":"2026-10-13 09:16:15 +0800","end":"2026-10-13 09:16:45 +0800",` +
	`"classification":"窦性心律","averageHeartRate":78,"numberOfVoltageMeasurements":3,` +
	`"samplingFrequency":512,"source":{"name":"移动心电图房颤提示软件","identifier":"com.apple.health.ecg"}`

func TestNormalizeECGRecord(t *testing.T) {
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC,
		ecgPayload(`{`+ecgBase+`,"voltageMeasurements":[`+
			`{"date":1791882975.8,"voltage":-63.28,"units":"mcV"},`+
			`{"date":1791882975.81,"voltage":0,"units":"mcV"},`+
			`{"date":1791882975.82,"voltage":66.42,"units":"mcV"}]}`))

	if len(res.ECG) != 1 {
		t.Fatalf("应归一 1 条心电记录，实际 %d（skipped=%d）", len(res.ECG), res.Skipped)
	}
	r := res.ECG[0]
	if r.Source != "移动心电图房颤提示软件" {
		t.Fatalf("来源（对象形态）应取 name，实际 %q", r.Source)
	}
	if r.Classification != "窦性心律" || r.ClassificationCode != ecg.CodeSinusRhythm {
		t.Fatalf("判定归一错误: %q / %q", r.Classification, r.ClassificationCode)
	}
	if r.AverageHeartRate == nil || *r.AverageHeartRate != 78 {
		t.Fatalf("平均心率错误: %v", r.AverageHeartRate)
	}
	if r.SamplingFrequency == nil || *r.SamplingFrequency != 512 {
		t.Fatalf("采样率错误: %v", r.SamplingFrequency)
	}
	// 点数取上游声明的 numberOfVoltageMeasurements
	if r.VoltageCount != 3 {
		t.Fatalf("波形点数应取上游声明值 3，实际 %d", r.VoltageCount)
	}
	// 时间与 local_date 由记录自带偏移推导：+0800 的 23:16 属于 10-13
	if got := r.StartTime.UTC().Format(time.RFC3339); got != "2026-10-13T01:16:15Z" {
		t.Fatalf("起始时间错误: %s", got)
	}
	if got := r.EndTime.UTC().Format(time.RFC3339); got != "2026-10-13T01:16:45Z" {
		t.Fatalf("结束时间错误: %s", got)
	}
	if r.LocalDate != "2026-10-13" || r.TZOffsetMin != 480 {
		t.Fatalf("local_date/偏移错误: %s / %d", r.LocalDate, r.TZOffsetMin)
	}
	if res.Skipped != 0 {
		t.Fatalf("不该跳过任何记录，实际 %d", res.Skipped)
	}
}

// 判定文本是本地化文本：同一份数据换个手机语言就是另一个串，
// 两者必须落到同一个分类码上，否则统计会被语言拆成两半。
func TestNormalizeECGClassificationIsLocalized(t *testing.T) {
	tests := []struct {
		raw  string
		want ecg.Code
	}{
		{"窦性心律", ecg.CodeSinusRhythm},
		{"Sinus Rhythm", ecg.CodeSinusRhythm},
		{"房颤", ecg.CodeAtrialFibrillation},
		{"Atrial Fibrillation", ecg.CodeAtrialFibrillation},
		{"不确定", ecg.CodeInconclusive},
		{"Inconclusive Poor Recording", ecg.CodeInconclusivePoorRecording},
	}
	for _, tt := range tests {
		raw := ecgPayload(`{"start":"2026-10-13 09:16:15 +0800","end":"2026-10-13 09:16:45 +0800",` +
			`"classification":` + `"` + tt.raw + `",` +
			`"averageHeartRate":70,"samplingFrequency":512,"source":"Apple Watch"}`)
		res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
		if len(res.ECG) != 1 {
			t.Fatalf("%q：应归一 1 条记录，实际 %d", tt.raw, len(res.ECG))
		}
		if res.ECG[0].ClassificationCode != tt.want {
			t.Errorf("%q 的分类码应为 %q，实际 %q", tt.raw, tt.want, res.ECG[0].ClassificationCode)
		}
		// 原文必须原样留着：码认不出时界面要靠它兜底展示
		if res.ECG[0].Classification != tt.raw {
			t.Errorf("%q 的原文被改动了: %q", tt.raw, res.ECG[0].Classification)
		}
	}
}

// 判定文本完全认不出时：码是 unknown，但记录照样入库、原文照样留着。
// 多了一层归一不等于可以丢数据。
func TestNormalizeECGUnknownClassificationStillStored(t *testing.T) {
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC,
		ecgPayload(`{"start":"2026-10-13 09:16:15 +0800","classification":"室性早搏",`+
			`"source":"Apple Watch"}`))
	if len(res.ECG) != 1 {
		t.Fatalf("认不出判定也要入库，实际 %d 条", len(res.ECG))
	}
	if res.ECG[0].ClassificationCode != ecg.CodeUnknown {
		t.Fatalf("应为 unknown，实际 %q", res.ECG[0].ClassificationCode)
	}
	if res.ECG[0].Classification != "室性早搏" {
		t.Fatalf("原文必须保留，实际 %q", res.ECG[0].Classification)
	}
}

// 缺 end 时不能把结束时间留在零值（会算出一个公元 1 年的时长）。
func TestNormalizeECGEndFallsBackToStart(t *testing.T) {
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC,
		ecgPayload(`{"start":"2026-10-13 09:16:15 +0800","classification":"窦性心律",`+
			`"source":"Apple Watch"}`))
	if len(res.ECG) != 1 {
		t.Fatalf("应归一 1 条，实际 %d", len(res.ECG))
	}
	r := res.ECG[0]
	if !r.EndTime.Equal(r.StartTime) {
		t.Fatalf("缺 end 时应回落到 start，实际 %s", r.EndTime.UTC())
	}
}

// 时间不可解析的记录只能丢弃（没有时刻就没法定位），且必须计入 skipped。
func TestNormalizeECGSkipsUnparsableTime(t *testing.T) {
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC,
		ecgPayload(`{"start":"","classification":"窦性心律","source":"Apple Watch"}`))
	if len(res.ECG) != 0 || res.Skipped != 1 {
		t.Fatalf("应跳过 1 条，实际 ecg=%d skipped=%d", len(res.ECG), res.Skipped)
	}
}

// 上游本就没给波形（该自动化没勾选电压数据）时，记录照常入库、点数为 0。
func TestNormalizeECGWithoutVoltageMeasurements(t *testing.T) {
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC,
		ecgPayload(`{"start":"2026-10-13 09:16:15 +0800","classification":"窦性心律",`+
			`"source":"Apple Watch"}`))
	if len(res.ECG) != 1 {
		t.Fatalf("应归一 1 条，实际 %d", len(res.ECG))
	}
	if res.ECG[0].VoltageCount != 0 {
		t.Fatalf("上游没声明点数时不该造出数来，实际 %d", res.ECG[0].VoltageCount)
	}
}

// 采样率是时轴的分母，0 或负数一律按缺失处理，否则推时轴会除零。
func TestNormalizeECGIgnoresNonPositiveSamplingFrequency(t *testing.T) {
	for _, raw := range []string{`"samplingFrequency":0`, `"samplingFrequency":-512`} {
		res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC,
			ecgPayload(`{"start":"2026-10-13 09:16:15 +0800","classification":"窦性心律",`+
				`"source":"Apple Watch",`+raw+`}`))
		if len(res.ECG) != 1 {
			t.Fatalf("%s：应归一 1 条，实际 %d", raw, len(res.ECG))
		}
		if res.ECG[0].SamplingFrequency != nil {
			t.Fatalf("%s：非正采样率应视为缺失，实际 %d", raw, *res.ECG[0].SamplingFrequency)
		}
	}
}

// ECG 不产生 samples/运动/睡眠：把它混进这些通道会让天级聚合凭空多出数据。
func TestNormalizeECGDoesNotLeakIntoOtherChannels(t *testing.T) {
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC,
		ecgPayload(`{`+ecgBase+`}`))
	if len(res.Samples) != 0 || len(res.Workouts) != 0 || len(res.Sleep) != 0 {
		t.Fatalf("心电不该进其它通道: samples=%d workouts=%d sleep=%d",
			len(res.Samples), len(res.Workouts), len(res.Sleep))
	}
	if len(res.Unknown) != 0 {
		t.Fatalf("心电不是指标，不该出现在 unknown_metrics 里: %v", res.Unknown)
	}
}

// 真实导出文件的形态：source 是对象、时间带偏移。
// 这里只断言「解析器不挑形态」，具体数值由真实文件端到端用例覆盖。
func TestParseECGAcceptsRealShape(t *testing.T) {
	raw := ecgPayload(`{"start":"2026-10-13 09:16:15 +0800","end":"2026-10-13 09:16:45 +0800",` +
		`"classification":"窦性心律","averageHeartRate":78,"numberOfVoltageMeasurements":2,` +
		`"samplingFrequency":512,"source":{"name":"移动心电图房颤提示软件"},` +
		`"voltageMeasurements":[{"date":1791882975.806,"voltage":-63.28,"units":"mcV"},` +
		`{"date":1791882975.808,"voltage":"66.42","units":"mcV"}]}`)
	p, err := ParsePayload(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("心电图形态变化不该导致整包解析失败: %v", err)
	}
	if len(p.Data.ECG) != 1 {
		t.Fatalf("应解析出 1 条心电记录，实际 %d", len(p.Data.ECG))
	}
	// 心电图不计入 received（与聚合睡眠同理：不产生 samples）
	if p.Count() != 0 {
		t.Fatalf("心电不该计入 Count，实际 %d", p.Count())
	}
	// 但「只有心电」的导出绝不能被判成空包
	if p.IsEmpty() {
		t.Fatal("只有心电图数据的导出不是空包，判空会让整份数据被静默丢弃")
	}
}
