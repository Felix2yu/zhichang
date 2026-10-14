package mcp

import "testing"

// 这两个换算是 MCP 睡眠详情里最容易错的地方：本夜轴跨零点顺延、
// UTC 时刻要按记录自带偏移还原成本地钟点（服务端时区不参与口径）。

func TestNightClock(t *testing.T) {
	cases := []struct {
		min  float64
		want string
	}{
		{0, "00:00"},
		{1430, "23:50"},
		{1620, "03:00"},   // 凌晨 03:00 在本夜轴上是 180+1440
		{2880, "00:00"},   // 整两天后回到零点
		{-30, "23:30"},    // 负数不能吐出 -00:30
		{1570.4, "02:10"}, // 四舍五入到分钟
	}
	for _, c := range cases {
		if got := nightClock(c.min); got != c.want {
			t.Errorf("nightClock(%v) = %s，期望 %s", c.min, got, c.want)
		}
	}
}

func TestLocalClock(t *testing.T) {
	// 2026-10-12T15:30:00Z 在 +0800 是 23:30
	if got := localClock("2026-10-12T15:30:00Z", 480); got != "23:30" {
		t.Errorf("+0800 入睡时刻应为 23:30，实际 %s", got)
	}
	// 同一时刻在 UTC 就是 15:30
	if got := localClock("2026-10-12T15:30:00Z", 0); got != "15:30" {
		t.Errorf("零偏移应原样给 UTC 钟点，实际 %s", got)
	}
	// 缺时刻/坏值不猜
	if got := localClock("", 480); got != "—" {
		t.Errorf("空时刻应回破折号，实际 %s", got)
	}
	if got := localClock("昨天半夜", 480); got != "—" {
		t.Errorf("不可解析的时刻应回破折号，实际 %s", got)
	}
}
