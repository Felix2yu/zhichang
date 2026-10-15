package tz

import (
	"testing"
	"time"
)

// NightDate 是「一晚归到哪一天」的唯一口径。它与入库、历史迁移、天级聚合三处共用，
// 判错一天的后果不是显示偏差，而是两晚挤进同一格（天级值翻倍）或一晚被挪到隔壁（趋势断裂）。
func TestNightDateAnchorsToSixPM(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"深夜入睡算当晚", "2026-08-27 23:39:00 +0800", "2026-08-27"},
		{"凌晨入睡算前一晚", "2026-08-27 02:23:00 +0800", "2026-08-26"},
		{"午夜整点算前一晚", "2026-08-27 00:00:00 +0800", "2026-08-26"},
		{"18:00 是分界本身，算当晚", "2026-08-27 18:00:00 +0800", "2026-08-27"},
		{"17:59 还属于前一晚", "2026-08-27 17:59:59 +0800", "2026-08-26"},
		{"跨月：月初凌晨归上月", "2026-09-01 01:30:00 +0800", "2026-08-31"},
		{"跨年：元旦凌晨归去年", "2027-01-01 03:00:00 +0800", "2026-12-31"},
		// 偏移不同、本地钟点相同 → 归属必须相同：判据是本地时间，不是 UTC。
		{"负偏移的凌晨", "2026-08-27 02:23:00 -0800", "2026-08-26"},
		{"同一本地钟点不因偏移而变", "2026-08-27 23:39:00 -0500", "2026-08-27"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, off, ok := Parse(tt.raw)
			if !ok {
				t.Fatalf("时间没解析成功: %s", tt.raw)
			}
			if got := NightDate(got, off, nil); got != tt.want {
				t.Fatalf("%s 应归到 %s，实际 %s", tt.raw, tt.want, got)
			}
		})
	}
}

// 无偏移的记录按 home 解释：同一个 UTC 时刻在 +0800 与 -0800 下可以属于两个不同的夜晚。
func TestNightDateWithoutOffset(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	utc := time.Date(2026, 8, 26, 15, 30, 0, 0, time.UTC) // 上海 23:30 / UTC 15:30
	if got := NightDate(utc, OffsetUnknown, sh); got != "2026-08-26" {
		t.Fatalf("上海口径下 23:30 入睡属于 26 号那晚，实际 %s", got)
	}
	if got := NightDate(utc, OffsetUnknown, time.UTC); got != "2026-08-25" {
		t.Fatalf("UTC 口径下 15:30 早于分界，应算 25 号那晚，实际 %s", got)
	}
}

// 归属与本地日期必须同源：LocalDate 是「哪天」，NightDate 是「哪一晚」，
// 18:00 之后两者同一天，之前差一天。
func TestNightDateAgreesWithLocalDate(t *testing.T) {
	for _, raw := range []string{"2026-08-27 19:05:00 +0800", "2026-08-27 09:05:00 +0800"} {
		got, off, ok := Parse(raw)
		if !ok {
			t.Fatal(raw)
		}
		day, night := LocalDate(got, off, nil), NightDate(got, off, nil)
		same := raw[11:13] >= "18"
		if same != (day == night) {
			t.Fatalf("%s：本地日 %s 与本夜 %s 的关系应与 18:00 分界一致", raw, day, night)
		}
	}
}
