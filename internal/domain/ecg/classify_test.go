package ecg

import "testing"

// 英文枚举取自 HAE 的导出文档，中文取自 Apple 官方 IFU（简中 / 港繁）与 ECG v2 新增措辞。
// 这些串是要长期吃的输入，钉死它们，避免日后改关键字时把某一类悄悄吃掉。
func TestClassify(t *testing.T) {
	tests := []struct {
		raw  string
		want Code
	}{
		// —— 上游文档里的 8 个英文枚举 ——
		{"Sinus Rhythm", CodeSinusRhythm},
		{"Atrial Fibrillation", CodeAtrialFibrillation},
		{"High Heart Rate", CodeHighHeartRate},
		{"Inconclusive Low Heart Rate", CodeInconclusiveLowHeartRate},
		{"Inconclusive High Heart Rate", CodeInconclusiveHighHeartRate},
		{"Inconclusive", CodeInconclusive},
		{"Inconclusive Poor Recording", CodeInconclusivePoorRecording},
		{"Unrecognized", CodeUnrecognized},

		// —— 实机导出的简中原文（本仓库的真实样例）——
		{"窦性心律", CodeSinusRhythm},
		{"房颤", CodeAtrialFibrillation},
		{"不确定", CodeInconclusive},
		{"记录欠佳", CodeInconclusivePoorRecording},
		// 简中 IFU 把两侧并成一句，说不清是哪一侧，只能给笼统结论
		{"低心率或高心率", CodeInconclusive},
		{"高低心率", CodeInconclusive},

		// —— 港繁 / 台繁写法 ——
		{"竇性心律", CodeSinusRhythm},
		{"心房顫動", CodeAtrialFibrillation},
		{"不明確", CodeInconclusive},
		{"無法判定", CodeInconclusive},
		{"判定不能", CodeInconclusive},
		{"記錄欠佳", CodeInconclusivePoorRecording},
		{"無法辨識", CodeUnrecognized},

		// —— ECG v2 的组合措辞 ——
		{"高心率 - 侦测不到心房颤动", CodeHighHeartRate},
		{"房颤 - 高心率", CodeAtrialFibrillation},
		{"Atrial Fibrillation - High Heart Rate", CodeAtrialFibrillation},

		// —— 形态容错：大小写、多余空白、NBSP、全角空格、各种连字符 ——
		{"  sinus   rhythm ", CodeSinusRhythm},
		{"SINUS RHYTHM", CodeSinusRhythm},
		{"窦性心律\u00a0", CodeSinusRhythm},
		{"窦性\u3000心律", CodeSinusRhythm},
		{"高心率–侦测不到心房颤动", CodeHighHeartRate},
		{"Inconclusive-Low Heart Rate", CodeInconclusiveLowHeartRate},

		// —— 认不出来一律 unknown，绝不猜 ——
		{"", CodeUnknown},
		{"   ", CodeUnknown},
		{"Not Set", CodeUnknown},
		{"Sinus Bradycardia", CodeSinusRhythm}, // 含 sinus：属于窦性一类，不是猜测而是关键字命中
		{"室性早搏", CodeUnknown},
		{"Some Future Classification", CodeUnknown},
	}
	for _, tt := range tests {
		if got := Classify(tt.raw); got != tt.want {
			t.Errorf("Classify(%q) = %q，期望 %q", tt.raw, got, tt.want)
		}
	}
}

// 顺序敏感：这三个串同时命中多个关键字，归类取决于判定顺序。
// 单独立一组用例，改动顺序时会立刻红。
func TestClassifyOrderSensitive(t *testing.T) {
	tests := []struct {
		raw  string
		want Code
		why  string
	}{
		{"Inconclusive High Heart Rate", CodeInconclusiveHighHeartRate,
			"同时含 high heart rate：不能归成高心率，它的结论是「测不准」"},
		{"房颤 - 高心率", CodeAtrialFibrillation,
			"同时含房颤与高心率：真正的心律结论是房颤"},
		{"记录欠佳", CodeInconclusivePoorRecording,
			"记录质量问题优先于泛泛的无法分类"},
	}
	for _, tt := range tests {
		if got := Classify(tt.raw); got != tt.want {
			t.Errorf("Classify(%q) = %q，期望 %q（%s）", tt.raw, got, tt.want, tt.why)
		}
	}
}

func TestDisplayZHAndKnown(t *testing.T) {
	for _, c := range []Code{
		CodeSinusRhythm, CodeAtrialFibrillation, CodeHighHeartRate,
		CodeInconclusiveLowHeartRate, CodeInconclusiveHighHeartRate,
		CodeInconclusive, CodeInconclusivePoorRecording, CodeUnrecognized,
	} {
		if DisplayZH(c) == "" {
			t.Errorf("%q 应有中文展示名", c)
		}
		if !Known(c) {
			t.Errorf("%q 应算已知（有展示名）", c)
		}
	}
	// unknown 没有展示名：界面据此回落展示上游原文，而不是替上游下一个结论
	if DisplayZH(CodeUnknown) != "" || Known(CodeUnknown) {
		t.Fatal("unknown 必须没有展示名，否则界面会用自造措辞盖掉上游原文")
	}
}
