package tz

import (
	"testing"
	"time"
)

func TestParseFullOffset(t *testing.T) {
	got, off, ok := Parse("2026-10-13 08:00:00 +0800")
	if !ok {
		t.Fatal("应解析成功")
	}
	if off != 480 {
		t.Fatalf("偏移应为 480，实际 %d", off)
	}
	if !got.UTC().Equal(time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("UTC 时间错误: %s", got.UTC())
	}
}

func TestLocalDateUsesPayloadOffsetNotServerTZ(t *testing.T) {
	// 服务端时区设为 UTC，但记录自带 -0800：本地日期必须按 -0800 计算。
	home := time.UTC
	got, off, ok := Parse("2026-10-13 20:30:00 -0800")
	if !ok {
		t.Fatal("应解析成功")
	}
	if d := LocalDate(got, off, home); d != "2026-10-13" {
		t.Fatalf("local_date 应为 2026-10-13（按记录自带偏移），实际 %s", d)
	}
	// 同一时刻在服务端时区下会是 10-14，证明没有走服务端时区。
	if got.UTC().Format(LayoutDate) == "2026-10-13" {
		t.Log("UTC 日期恰好相同，改用边界值复核")
	}
	got2, off2, _ := Parse("2026-10-13 23:30:00 -0800")
	if d := LocalDate(got2, off2, home); d != "2026-10-13" {
		t.Fatalf("跨日边界错误: %s", d)
	}
	if got2.UTC().Format(LayoutDate) != "2026-10-14" {
		t.Fatalf("UTC 应为 10-14，实际 %s", got2.UTC())
	}
}

func TestLocalDateFallbackToHomeTZ(t *testing.T) {
	home, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("缺少 Asia/Shanghai 时区数据")
	}
	// 聚合睡眠只有日期，无偏移：回落到 home timezone。
	got, off, ok := Parse("2026-10-13")
	if !ok {
		t.Fatal("应解析成功")
	}
	if off != OffsetUnknown {
		t.Fatal("仅日期的记录应标记为未知偏移")
	}
	if d := LocalDate(got, off, home); d != "2026-10-13" {
		t.Fatalf("local_date 应为 2026-10-13，实际 %s", d)
	}
}

func TestParseWithFallback(t *testing.T) {
	home, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("缺少 Asia/Shanghai 时区数据")
	}
	got, off, ok := ParseWithFallback("2026-10-13 08:00:00", home)
	if !ok {
		t.Fatal("应解析成功")
	}
	if off != OffsetUnknown {
		t.Fatal("无偏移字符串应标记为未知")
	}
	want := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC) // 08:00 +0800
	if !got.Equal(want) {
		t.Fatalf("回落解析错误: got %s want %s", got, want)
	}
}

func TestOffsetOrDefault(t *testing.T) {
	home, _ := time.LoadLocation("Asia/Shanghai")
	got, _, _ := Parse("2026-10-13")
	if off := OffsetOrDefault(OffsetUnknown, got, home); off != 480 {
		t.Fatalf("回落偏移应为 480，实际 %d", off)
	}
	if off := OffsetOrDefault(-300, got, home); off != -300 {
		t.Fatalf("已知偏移应原样返回，实际 %d", off)
	}
}
