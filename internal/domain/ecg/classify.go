// Package ecg 定义心电图的判定口径：把上游的本地化文本收敛成跨语言稳定的分类码。
//
// 为什么需要这一层：HAE 导出的 classification 是**随手机语言本地化的展示串**，
// 同一次「窦性心律」在英文手机上写成 "Sinus Rhythm"。若直接拿字符串当枚举用，
// 换一次手机语言，历史数据就与之后的数据分裂成两个值，统计与筛选全部失效。
// 而 HealthKit 本身给的是稳定枚举（HKElectrocardiogram.Classification），
// 只是被 HAE 渲染成了人话——这里把它还原回来。
//
// 兜底原则：认不出来一律 unknown，**绝不猜**。原始文本由调用方原样入库，
// 界面上 code 未知时回落展示原文，所以猜错不会丢数据，只会少一层归一。
package ecg

import "strings"

// Code 是跨语言稳定的心电图分类码。取值与 HealthKit 的枚举一一对应。
type Code string

const (
	// CodeSinusRhythm 窦性心律：心脏以 50~100 次/分的规律模式跳动。
	CodeSinusRhythm Code = "sinus_rhythm"
	// CodeAtrialFibrillation 房颤：不规律心律（含 ECG v2 的「房颤 - 高心率」）。
	CodeAtrialFibrillation Code = "atrial_fibrillation"
	// CodeHighHeartRate 高心率（未检测到房颤）。
	CodeHighHeartRate Code = "high_heart_rate"
	// CodeInconclusiveLowHeartRate 因心率过低无法分类。
	CodeInconclusiveLowHeartRate Code = "inconclusive_low_heart_rate"
	// CodeInconclusiveHighHeartRate 因心率过高无法分类。
	CodeInconclusiveHighHeartRate Code = "inconclusive_high_heart_rate"
	// CodeInconclusive 无法确定分类。
	CodeInconclusive Code = "inconclusive"
	// CodeInconclusivePoorRecording 因信号质量差（记录欠佳）无法分类。
	CodeInconclusivePoorRecording Code = "inconclusive_poor_recording"
	// CodeUnrecognized 上游声称有判定，但不在已知枚举内。
	CodeUnrecognized Code = "unrecognized"
	// CodeUnknown 完全认不出的文本。
	CodeUnknown Code = "unknown"
)

// 中文展示名。UI 一律用这里的措辞，而不是上游原文——这样界面文案不随手机语言变化。
// 括号里的限定是上游信息的一部分（Apple 把「心率过低/过高」与笼统的「不确定」分开报），
// 平铺成一个「不确定」会把这份区分丢掉。
var displayZH = map[Code]string{
	CodeSinusRhythm:               "窦性心律",
	CodeAtrialFibrillation:        "房颤",
	CodeHighHeartRate:             "高心率",
	CodeInconclusiveLowHeartRate:  "不确定（心率过低）",
	CodeInconclusiveHighHeartRate: "不确定（心率过高）",
	CodeInconclusive:              "不确定",
	CodeInconclusivePoorRecording: "记录欠佳",
	CodeUnrecognized:              "未识别",
}

// DisplayZH 返回分类码的中文展示名；unknown 无展示名，调用方应回落展示上游原文。
func DisplayZH(c Code) string { return displayZH[c] }

// Known 判断该分类码是否有中文展示名。只有 unknown 没有——它意味着
// 「上游给了判定文本，但我们一个字都没认出来」，此时界面应回落展示上游原文，
// 而不是显示一个自己编的结论。
func Known(c Code) bool {
	_, ok := displayZH[c]
	return ok
}

// 关键字分组。写全简繁两套：同一条记录可能来自简中、港繁或台繁手机。
var (
	// 记录质量差：与「判定得出但结论是不确定」区分开，它是「采集本身不可用」。
	poorRecording = []string{
		"poor recording", "poor quality", "记录欠佳", "記錄欠佳", "记录不佳", "記錄不佳",
		"记录不良", "記錄不良", "记录状况不佳", "記錄狀況不佳",
		"信号质量差", "訊號品質差", "信号不佳", "訊號不佳",
	}
	// 高低心率并列成一句：说不出是哪一侧，只能给笼统结论。
	lowOrHigh = []string{"高低心率", "低心率或高心率", "高心率或低心率", "low or high heart rate"}
	// 明确得出「房颤」的关键词。
	afib = []string{"房颤", "心房颤动", "心房顫動", "心房纖維性顫動", "atrial fibrillation", "afib"}
	// 否定房颤的措辞：ECG v2 的「高心率 - 侦测不到心房颤动」全靠这里区分，
	// 少了它就会把一条「没有房颤」的结果标成房颤。
	afibNegated = []string{
		"侦测不到", "偵測不到", "未侦测到", "未偵測到", "未检测到", "未檢測到",
		"检测不到", "檢測不到", "未发现", "未發現",
		"not detected", "no atrial fibrillation", "without atrial fibrillation",
	}
	// 上游明说「没法给结论」的各种措辞。
	// 「未定论」是 iOS 简中对 Inconclusive 的实际渲染（实测一年导出里 3 条如此），
	// 漏掉它的后果不是显示难看：那条记录落到 unknown，按判定统计时凭空消失。
	inconclusiveMark = []string{
		"无法判定", "無法判定", "判定不能", "不确定", "不明确", "不明確", "无法分类", "無法分類",
		"未定论", "未定論", "无法定论", "無法定論", "结论待定", "結論待定",
		"inconclusive", "undetermined", "unclassified",
	}
	// 上游连分类器都没认出来的措辞。
	unrecognizedMark = []string{
		"unrecognized", "not recognized", "无法识别", "無法辨識", "無法識別", "未识别", "未識別",
	}
	lowHR  = []string{"过低", "過低", "偏低", "低心率", "过缓", "過緩", "low heart rate", "bradycardia"}
	highHR = []string{"过高", "過高", "偏高", "高心率", "过速", "過速", "high heart rate", "tachycardia"}
	sinus  = []string{"窦性", "竇性", "sinus"}
)

// Classify 把上游的判定文本归一成分类码。
//
// 按关键字而非整串精确匹配：Apple 各语言、各版本的措辞都在变
// （简中「窦性心律」/ 港繁「竇性心律」/ v2 的「记录欠佳」/「高心率 - 侦测不到心房颤动」），
// 精确匹配表会在下一次措辞变化时静默失效。
//
// 判定顺序有讲究，改这一段之前先看 classify_test.go 里的顺序敏感用例：
//   - 「记录欠佳」是采集不可用，任何心律关键词都不该把它拉走；
//   - 「低心率或高心率」是并列表述，两侧都命中，只能给笼统结论；
//   - 「高心率 - 侦测不到心房颤动」同时含「高心率」与「心房颤动」，
//     靠否定词才能得出正确的「高心率」；
//   - 「Inconclusive High Heart Rate」含 "high heart rate"，但结论是「测不准」。
func Classify(raw string) Code {
	s := normalize(raw)
	if s == "" {
		return CodeUnknown
	}

	if containsAny(s, poorRecording...) {
		return CodeInconclusivePoorRecording
	}
	if containsAny(s, lowOrHigh...) {
		return CodeInconclusive
	}
	// 出现房颤关键词但被否定：结论不是房颤。此时若同时报了高心率，就是 v2 的
	// 「高心率，未检测到房颤」；否则只能说给不出结论。
	if containsAny(s, afib...) && containsAny(s, afibNegated...) {
		if containsAny(s, highHR...) {
			return CodeHighHeartRate
		}
		return CodeInconclusive
	}
	if containsAny(s, inconclusiveMark...) || containsAny(s, unrecognizedMark...) {
		if containsAny(s, unrecognizedMark...) {
			return CodeUnrecognized
		}
		if containsAny(s, lowHR...) {
			return CodeInconclusiveLowHeartRate
		}
		if containsAny(s, highHR...) {
			return CodeInconclusiveHighHeartRate
		}
		return CodeInconclusive
	}
	// 判断得出明确结论的几类。房颤排在「高心率」之前：
	// v2 的「房颤 - 高心率」两类关键词都有，而真正的心律结论是房颤。
	if containsAny(s, afib...) {
		return CodeAtrialFibrillation
	}
	if containsAny(s, sinus...) {
		return CodeSinusRhythm
	}
	if containsAny(s, highHR...) {
		return CodeHighHeartRate
	}
	return CodeUnknown
}

// normalize 统一大小写与空白：NBSP（HAE 会写进来源名与部分文本）、全角空格、
// 各种连字符与引号都先抹平，省得每种写法都单独列一遍。
func normalize(raw string) string {
	s := strings.NewReplacer(
		"\u00a0", " ", // NBSP
		"\u3000", " ", // 全角空格
		"\u2010", "-", "\u2011", "-", "\u2012", "-", "\u2013", "-", "\u2014", "-",
		"\u2018", "'", "\u2019", "'", "\u201c", "\"", "\u201d", "\"",
	).Replace(raw)
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
