// Package tz 解析 Health Auto Export 的时间戳，并推导记录自带的本地日期。
package tz

import (
	"strings"
	"time"
)

const (
	// LayoutFull 是 HAE 的标准时间格式：yyyy-MM-dd HH:mm:ss Z。
	LayoutFull = "2006-01-02 15:04:05 -0700"
	// LayoutNoOffset 为不带偏移的时间格式（罕见，兜底）。
	LayoutNoOffset = "2006-01-02 15:04:05"
	// LayoutDate 为仅日期格式（聚合睡眠记录使用）。
	LayoutDate = "2006-01-02"
)

// OffsetUnknown 表示 payload 未携带时区偏移。
const OffsetUnknown = 1 << 30

// Parse 解析 HAE 时间字符串，返回 UTC 时间、payload 自带偏移（分钟）与是否成功。
// offMin 为 OffsetUnknown 时表示 payload 未携带偏移，需由调用方回落到 home timezone。
func Parse(s string) (time.Time, int, bool) {
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"`))
	if s == "" {
		return time.Time{}, OffsetUnknown, false
	}
	if t, err := time.Parse(LayoutFull, s); err == nil {
		_, off := t.Zone()
		return t.UTC(), off / 60, true
	}
	if t, err := time.Parse(LayoutNoOffset, s); err == nil {
		return t.UTC(), OffsetUnknown, true
	}
	if t, err := time.Parse(LayoutDate, s); err == nil {
		return t.UTC(), OffsetUnknown, true
	}
	return time.Time{}, OffsetUnknown, false
}

// ParseWithFallback 在 payload 未携带偏移时，使用 loc 解释时间。
func ParseWithFallback(s string, loc *time.Location) (time.Time, int, bool) {
	t, off, ok := Parse(s)
	if !ok {
		return t, off, false
	}
	if off == OffsetUnknown && loc != nil {
		// 无偏移：把原字符串按 loc 重新解释（对纯日期取 loc 的午夜）。
		if tt, err := time.ParseInLocation(LayoutNoOffset, strings.TrimSpace(s), loc); err == nil {
			return tt.UTC(), OffsetUnknown, true
		}
		if tt, err := time.ParseInLocation(LayoutDate, strings.TrimSpace(s), loc); err == nil {
			return tt.UTC(), OffsetUnknown, true
		}
	}
	return t, off, true
}

// LocalDate 计算记录自带的本地日期：
//   - payload 携带偏移时，用该固定偏移求日期（不经过服务端时区）；
//   - 未携带偏移时，回落到 home location；
//   - 仍无则按 UTC。
func LocalDate(t time.Time, offMin int, home *time.Location) string {
	switch {
	case offMin != OffsetUnknown:
		return t.In(time.FixedZone("hae", offMin*60)).Format(LayoutDate)
	case home != nil:
		return t.In(home).Format(LayoutDate)
	default:
		return t.UTC().Format(LayoutDate)
	}
}

// OffsetOrDefault 把未知偏移换成 home location 当日的偏移，用于落库展示。
func OffsetOrDefault(offMin int, t time.Time, home *time.Location) int {
	if offMin != OffsetUnknown {
		return offMin
	}
	if home == nil {
		return 0
	}
	_, off := t.In(home).Zone()
	return off / 60
}
