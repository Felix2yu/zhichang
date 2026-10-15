package sample

import "strings"

// CleanSource 归一来源名里「肉眼一样、字节不一样」的字符。
//
// 为什么需要这一层：HAE 写出的设备名用的是 U+00A0（不换行空格）——
// 实测真实导出里 249 个点位的来源全是同一台手表，名字写作「小明的Apple\u00a0Watch」（示例名，真实设备名已脱敏）。它和
// 普通空格版「小明的Apple Watch」在界面上一模一样，却是两个不同的字符串：
//
//   - sources 表会多出一行「近似重复」的来源，样本数被劈成两半；
//   - 去重唯一索引 (start_time, metric, source, value) 会因为 source 不同
//     而放行本该判为同一份数据的点，同一时刻的同一个值入两遍。
//
// 换一次输入法、换一台设备、手工导出一次，就足以让普通空格版冒出来，
// 所以必须在入口处收敛，而不是等界面上出现两行才发现。
//
// HAE 还会把多来源拼成 "小明的Apple Watch|小明的iPhone"，且管两侧的空格
// 不固定（实测 "A | B" 与 "A|B" 并存）——同样会把本是一组的来源分裂成两行。
//
// 只做**空白等价**替换，不碰任何可见字符：不折叠大小写、不删标点、不猜语义、
// 不拆分多来源。设备名是人写的，任何「聪明」的改写都可能把两台真不同的设备
// 合并成一台，或在库里留下一个谁也没见过的名字。
func CleanSource(s string) string {
	if s == "" {
		return ""
	}
	// 零宽字符**不是**空白（unicode.IsSpace 不含它们），strings.Fields 不会在
	// 那里切分，必须先删掉：否则 "Apple\u200bWatch" 会被当成一个词原样留下，
	// 与 "Apple Watch" 依旧分裂——这类字符几乎都是复制粘贴带进来的。
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\u200b', // 零宽空格
			'\u200c', // 零宽非连接符
			'\u200d', // 零宽连接符
			'\u2060', // 词连接符
			'\u180e', // 蒙古文元音分隔符（曾是空白，现归格式字符）
			'\ufeff': // BOM / 零宽不换行空格
			continue
		}
		b.WriteRune(r)
	}
	// strings.Fields 按 unicode.IsSpace 切分，而 NBSP(U+00A0)、表意空格(U+3000)、
	// 各种窄空格(U+2000..U+200A / U+202F / U+205F) 都在 White_Space 属性里，
	// 所以这一步顺带把「看着是空格、其实不是 U+0020」的字符一并规整，
	// 同时顺带压缩连续空白与去掉首尾空白。
	out := strings.Join(strings.Fields(b.String()), " ")
	// 多来源分隔符两侧的空格统一成 " | "：同一组来源不再因写法差异分裂。
	// 空段（如 "a||b"）直接丢弃，不留下 "a |  | b" 这种畸形名。
	if strings.Contains(out, "|") {
		segs := strings.Split(out, "|")
		kept := segs[:0]
		for _, seg := range segs {
			if seg = strings.TrimSpace(seg); seg != "" {
				kept = append(kept, seg)
			}
		}
		out = strings.Join(kept, " | ")
	}
	return out
}
