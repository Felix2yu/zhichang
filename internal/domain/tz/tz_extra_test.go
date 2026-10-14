package tz

import (
	"testing"
	"time"
)

func shanghai(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("缺少 Asia/Shanghai 时区数据")
	}
	return loc
}

// HAE 的 payload 时间字段常带 JSON 引号或首尾空格，必须在解析前清掉。
func TestParseAcceptsQuotedAndPaddedInput(t *testing.T) {
	cases := []string{
		`"2026-10-13 08:00:00 +0800"`,
		`  2026-10-13 08:00:00 +0800  `,
		`" 2026-10-13 08:00:00 +0800 "`,
	}
	want := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got, off, ok := Parse(in)
			if !ok {
				t.Fatalf("应解析成功: %q", in)
			}
			if off != 480 {
				t.Fatalf("偏移 = %d，期望 480", off)
			}
			if !got.Equal(want) {
				t.Fatalf("时间 = %s，期望 %s", got, want)
			}
		})
	}
}

func TestParseFormats(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantOK  bool
		wantOff int
		wantUTC time.Time
	}{
		{
			name: "带正偏移", in: "2026-10-13 08:00:00 +0800", wantOK: true, wantOff: 480,
			wantUTC: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "带负偏移", in: "2026-10-13 20:30:00 -0800", wantOK: true, wantOff: -480,
			wantUTC: time.Date(2026, 10, 14, 4, 30, 0, 0, time.UTC),
		},
		{
			// 半小时偏移（印度 +0530）必须按分钟原样带回，否则本地日期算错一天。
			name: "非整小时偏移", in: "2026-10-13 21:00:00 +0530", wantOK: true, wantOff: 330,
			wantUTC: time.Date(2026, 10, 13, 15, 30, 0, 0, time.UTC),
		},
		{
			name: "UTC 偏移", in: "2026-10-13 08:00:00 +0000", wantOK: true, wantOff: 0,
			wantUTC: time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC),
		},
		{
			// 不带偏移：时间先按 UTC 落值，偏移标记为未知交由 home timezone 回落。
			name: "不带偏移", in: "2026-10-13 08:00:00", wantOK: true, wantOff: OffsetUnknown,
			wantUTC: time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC),
		},
		{
			// 聚合睡眠记录只有日期。
			name: "仅日期", in: "2026-10-13", wantOK: true, wantOff: OffsetUnknown,
			wantUTC: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
		},
		{name: "空串", in: "", wantOK: false, wantOff: OffsetUnknown},
		{name: "仅空白", in: "   ", wantOK: false, wantOff: OffsetUnknown},
		{name: "空引号", in: `""`, wantOK: false, wantOff: OffsetUnknown},
		{name: "ISO 格式不支持", in: "2026-10-13T08:00:00Z", wantOK: false, wantOff: OffsetUnknown},
		{name: "字段缺失", in: "2026-13-45 99:99:99 +0800", wantOK: false, wantOff: OffsetUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, off, ok := Parse(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("Parse(%q) ok = %v，期望 %v", tc.in, ok, tc.wantOK)
			}
			if off != tc.wantOff {
				t.Fatalf("Parse(%q) off = %d，期望 %d", tc.in, off, tc.wantOff)
			}
			if tc.wantOK && !got.Equal(tc.wantUTC) {
				t.Fatalf("Parse(%q) = %s，期望 %s", tc.in, got, tc.wantUTC)
			}
			if !tc.wantOK && !got.IsZero() {
				t.Fatalf("解析失败应返回零值时间，实际 %s", got)
			}
		})
	}
}

func TestParseWithFallbackBranches(t *testing.T) {
	home := shanghai(t)

	t.Run("失败原样返回不套 fallback", func(t *testing.T) {
		got, off, ok := ParseWithFallback("完全不是时间", home)
		if ok {
			t.Fatal("应失败")
		}
		if off != OffsetUnknown || !got.IsZero() {
			t.Fatalf("失败路径应返回零值+未知偏移，实际 %s / %d", got, off)
		}
	})

	t.Run("带偏移的记录不受 loc 影响", func(t *testing.T) {
		got, off, ok := ParseWithFallback("2026-10-13 20:30:00 -0800", home)
		if !ok || off != -480 {
			t.Fatalf("偏移应原样带回，实际 ok=%v off=%d", ok, off)
		}
		if want := time.Date(2026, 10, 14, 4, 30, 0, 0, time.UTC); !got.Equal(want) {
			t.Fatalf("got %s want %s", got, want)
		}
	})

	t.Run("无偏移时间按 home 解释", func(t *testing.T) {
		got, off, ok := ParseWithFallback("2026-10-13 23:30:00", home)
		if !ok || off != OffsetUnknown {
			t.Fatalf("应标记未知偏移，实际 ok=%v off=%d", ok, off)
		}
		// 23:30 +0800 => 15:30 UTC
		if want := time.Date(2026, 10, 13, 15, 30, 0, 0, time.UTC); !got.Equal(want) {
			t.Fatalf("got %s want %s", got, want)
		}
	})

	t.Run("纯日期取 home 的午夜", func(t *testing.T) {
		got, off, ok := ParseWithFallback("2026-10-13", home)
		if !ok || off != OffsetUnknown {
			t.Fatalf("应标记未知偏移，实际 ok=%v off=%d", ok, off)
		}
		if want := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC); !got.Equal(want) {
			t.Fatalf("聚合睡眠日期应按 home 午夜入库，got %s want %s", got, want)
		}
	})

	// 缺 home location 时不做回落解释，保持 Parse 的 UTC 语义（不猜时区）。
	t.Run("loc 为 nil 时不回落", func(t *testing.T) {
		got, off, ok := ParseWithFallback("2026-10-13 08:00:00", nil)
		if !ok || off != OffsetUnknown {
			t.Fatalf("应解析成功且偏移未知，实际 ok=%v off=%d", ok, off)
		}
		if want := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC); !got.Equal(want) {
			t.Fatalf("got %s want %s", got, want)
		}
	})

	// fallback 的再解析只 TrimSpace、不剥引号：带引号的无偏移输入落到 Parse 的 UTC 语义。
	// 记录现状（与不带引号的分支不同），改动这里说明是修正了这一不一致。
	t.Run("引号包裹的无偏移时间不套 home", func(t *testing.T) {
		got, _, ok := ParseWithFallback(`"2026-10-13 08:00:00"`, home)
		if !ok {
			t.Fatal("应解析成功")
		}
		if want := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC); !got.Equal(want) {
			t.Fatalf("got %s want %s", got, want)
		}
	})

	t.Run("首尾空白的无偏移时间仍按 home 解释", func(t *testing.T) {
		got, _, ok := ParseWithFallback("  2026-10-13 08:00:00  ", home)
		if !ok {
			t.Fatal("应解析成功")
		}
		if want := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
			t.Fatalf("got %s want %s", got, want)
		}
	})
}

// LocalDate 的三级优先：payload 偏移 > home > UTC。
func TestLocalDate(t *testing.T) {
	home := shanghai(t)
	cases := []struct {
		name  string
		in    string
		off   int
		home  *time.Location
		local string
		utc   string
	}{
		{
			name: "负偏移跨到前一日", in: "2026-10-13 23:30:00 -0800", off: -480, home: home,
			local: "2026-10-13", utc: "2026-10-14",
		},
		{
			// +0800 的凌晨两点在 UTC 还是前一天：必须按记录自己的偏移记日。
			name: "正偏移跨到后一日", in: "2026-10-14 02:00:00 +0800", off: 480, home: home,
			local: "2026-10-14", utc: "2026-10-13",
		},
		{
			name: "半小时偏移", in: "2026-10-14 00:30:00 +0530", off: 330, home: home,
			local: "2026-10-14", utc: "2026-10-13",
		},
		{
			name: "偏移为 0", in: "2026-10-13 23:30:00 +0000", off: 0, home: home,
			local: "2026-10-13", utc: "2026-10-13",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, off, ok := Parse(tc.in)
			if !ok {
				t.Fatalf("Parse(%q) 失败", tc.in)
			}
			if off != tc.off {
				t.Fatalf("偏移 = %d，期望 %d", off, tc.off)
			}
			if d := LocalDate(got, off, tc.home); d != tc.local {
				t.Errorf("payload 偏移下的本地日期 = %s，期望 %s", d, tc.local)
			}
			// 同一条记录在 home 时区下会记到另一天的场景，证明没有走服务端时区。
			if got.UTC().Format(LayoutDate) != tc.utc {
				t.Errorf("UTC 日期 = %s，期望 %s", got.UTC().Format(LayoutDate), tc.utc)
			}
		})
	}

	t.Run("无偏移且无 home 时按 UTC", func(t *testing.T) {
		// 23:30Z 的记录：home 为 nil 时只能按 UTC 记日。
		got := time.Date(2026, 10, 13, 23, 30, 0, 0, time.UTC)
		if d := LocalDate(got, OffsetUnknown, nil); d != "2026-10-13" {
			t.Errorf("UTC 回落日期 = %s", d)
		}
		if d := LocalDate(got, OffsetUnknown, home); d != "2026-10-14" {
			t.Errorf("home 时区（+0800）下应记为次日，实际 %s", d)
		}
	})

	t.Run("home 时区跨零点", func(t *testing.T) {
		got := time.Date(2026, 3, 15, 15, 30, 0, 0, time.UTC)
		if d := LocalDate(got, OffsetUnknown, home); d != "2026-03-15" {
			t.Errorf("15:30Z 在 +0800 是同日 23:30，实际 %s", d)
		}
		got2 := time.Date(2026, 3, 15, 16, 30, 0, 0, time.UTC)
		if d := LocalDate(got2, OffsetUnknown, home); d != "2026-03-16" {
			t.Errorf("16:30Z 在 +0800 已跨零点，实际 %s", d)
		}
	})
}

func TestOffsetOrDefaultBranches(t *testing.T) {
	home := shanghai(t)
	utcDay := time.Date(2026, 10, 13, 4, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		off   int
		t     time.Time
		home  *time.Location
		input int
		want  int
	}{
		{name: "已知正偏移原样返回", input: 330, t: utcDay, home: home, want: 330},
		{name: "已知负偏移原样返回", input: -480, t: utcDay, home: home, want: -480},
		{name: "已知 0 偏移不被当成缺失", input: 0, t: utcDay, home: home, want: 0},
		{name: "未知偏移回落 home", input: OffsetUnknown, t: utcDay, home: home, want: 480},
		{name: "未知偏移且无 home", input: OffsetUnknown, t: utcDay, home: nil, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OffsetOrDefault(tc.input, tc.t, tc.home); got != tc.want {
				t.Fatalf("OffsetOrDefault(%d) = %d，期望 %d", tc.input, got, tc.want)
			}
		})
	}

	// home 在夏令时与冬令时给出不同偏移，说明取的是记录当日而非当前时刻。
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("缺少 America/New_York 时区数据")
	}
	summer := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if got := OffsetOrDefault(OffsetUnknown, summer, ny); got != -240 {
		t.Fatalf("夏令时应为 -240，实际 %d", got)
	}
	winter := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if got := OffsetOrDefault(OffsetUnknown, winter, ny); got != -300 {
		t.Fatalf("冬令时应为 -300，实际 %d", got)
	}
}
