package mcp

import (
	"regexp"
	"unicode/utf8"
)

// keepRe 匹配「日期 / 钟点」——脱敏时原样保留。
// 它们是结构而不是大小：AI 客户端需要知道哪一天、几点睡，才能回答
// 「哪几天有数据」「最近一次测量在什么时候」。
var keepRe = regexp.MustCompile(
	`\d{4}-\d{2}-\d{2}(?:[T ]\d{1,2}:\d{2}(?::\d{2})?(?:Z|[+-]\d{2}:?\d{2})?)?|\d{1,2}:\d{2}(?::\d{2})?`)

// numRe 匹配其余任何数字串（含正负号、小数、千分位与结尾百分号）。
// 刻意宽松：不带单位的整数（天级表里的「7102」）同样是测量值，
// 只挡小数等于给它开了后门；连正负号一起吃掉，否则「+12.5%」会剩下一个加号。
var numRe = regexp.MustCompile(`[+-]?\d[\d_]*(?:[.,]\d[\d_]*)*%?`)

// sentinelRe 还原 maskNumbers 埋下的哨兵。
var sentinelRe = regexp.MustCompile("\x00[\uE000-\uE0FF]\x01")

// Redacted 是脱敏后替代数值的占位符。
//
// 用固定字面量而不是把数字直接删掉：删掉会让「7.5 小时」变成「 小时」，
// 那比泄露更难被查出来——读的人以为原文就是这么写的。
const Redacted = "‹已脱敏›"

// maskNumbers 把文本里的数字换成占位符，日期与钟点除外。
//
// 先把要保留的片段换成不含数字的私有区哨兵（哨兵里带下标，
// 若用普通标记会被 numRe 再吃一遍），掩掉其余数字，最后还原。
func maskNumbers(s string) string {
	var kept []string
	s = keepRe.ReplaceAllStringFunc(s, func(m string) string {
		kept = append(kept, m)
		return "\x00" + string(rune(0xE000+len(kept)-1)) + "\x01"
	})
	s = numRe.ReplaceAllString(s, Redacted)
	if len(kept) == 0 {
		return s
	}
	return sentinelRe.ReplaceAllStringFunc(s, func(m string) string {
		// 哨兵里的下标是一个多字节 rune（\x00 之后的第一个字节不是整个码点），
		// 必须按 UTF-8 解码后再减基址。
		r, _ := utf8.DecodeRuneInString(m[1:])
		return kept[int(r)-0xE000]
	})
}
