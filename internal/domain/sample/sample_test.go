package sample

import (
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/tz"
)

func shanghai(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("缺少 Asia/Shanghai 时区数据")
	}
	return loc
}

func TestNormalize(t *testing.T) {
	home := shanghai(t)

	t.Run("EndTime 零值回拉到 StartTime", func(t *testing.T) {
		st := time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC)
		s := Sample{Metric: "heart_rate", StartTime: st, TZOffsetMin: 480}
		s.Normalize(home)
		if !s.EndTime.Equal(st) {
			t.Fatalf("EndTime 应回拉为 StartTime，实际 %s", s.EndTime)
		}
	})

	t.Run("EndTime 早于 StartTime 回拉（HAE 偶发倒序字段）", func(t *testing.T) {
		st := time.Date(2026, 10, 13, 5, 0, 0, 0, time.UTC)
		en := time.Date(2026, 10, 13, 4, 59, 0, 0, time.UTC)
		s := Sample{StartTime: st, EndTime: en, TZOffsetMin: 480}
		s.Normalize(home)
		if !s.EndTime.Equal(st) {
			t.Fatalf("倒序区间应被拉平，实际 EndTime=%s", s.EndTime)
		}
	})

	t.Run("EndTime 正常时不动", func(t *testing.T) {
		st := time.Date(2026, 10, 13, 5, 0, 0, 0, time.UTC)
		en := st.Add(time.Minute)
		s := Sample{StartTime: st, EndTime: en, TZOffsetMin: 480, LocalDate: "2026-10-13"}
		s.Normalize(home)
		if !s.EndTime.Equal(en) {
			t.Fatalf("合法区间不应被改写，实际 EndTime=%s", s.EndTime)
		}
	})

	// local_date 由记录自带偏移推导，不经过服务端时区。
	t.Run("补齐 LocalDate 用 payload 偏移", func(t *testing.T) {
		// 2026-10-13 16:30Z 在 +0800 已是 10-14。
		st := time.Date(2026, 10, 13, 16, 30, 0, 0, time.UTC)
		s := Sample{StartTime: st, TZOffsetMin: 480}
		s.Normalize(home)
		if s.LocalDate != "2026-10-14" {
			t.Fatalf("LocalDate = %s，期望 2026-10-14", s.LocalDate)
		}
	})

	t.Run("偏移未知时回落 home 再回落 UTC", func(t *testing.T) {
		st := time.Date(2026, 10, 13, 16, 30, 0, 0, time.UTC)
		s := Sample{StartTime: st, TZOffsetMin: tz.OffsetUnknown}
		s.Normalize(home)
		if s.LocalDate != "2026-10-14" {
			t.Fatalf("home（+0800）下应为 2026-10-14，实际 %s", s.LocalDate)
		}

		noHome := Sample{StartTime: st, TZOffsetMin: tz.OffsetUnknown}
		noHome.Normalize(nil)
		if noHome.LocalDate != "2026-10-13" {
			t.Fatalf("无 home 时应按 UTC 记日，实际 %s", noHome.LocalDate)
		}
	})

	t.Run("已有 LocalDate 不被覆盖", func(t *testing.T) {
		// 聚合链路已按业务口径写好日期，Normalize 不能二次改写。
		s := Sample{
			StartTime:   time.Date(2026, 10, 13, 16, 30, 0, 0, time.UTC),
			TZOffsetMin: 480,
			LocalDate:   "2026-10-13",
		}
		s.Normalize(home)
		if s.LocalDate != "2026-10-13" {
			t.Fatalf("显式 LocalDate 应保留，实际 %s", s.LocalDate)
		}
	})
}

func TestSetTimeInvalidStartReturnsFalse(t *testing.T) {
	s := Sample{Metric: "step_count"}
	if s.SetTime("完全不是时间", "2026-10-13 09:00:00 +0800", shanghai(t)) {
		t.Fatal("起始时间不可解析应返回 false")
	}
	if !s.StartTime.IsZero() || !s.EndTime.IsZero() {
		t.Fatalf("失败路径不应写入时间，实际 start=%s end=%s", s.StartTime, s.EndTime)
	}
	if s.LocalDate != "" {
		t.Fatalf("失败路径不应补 LocalDate，实际 %q", s.LocalDate)
	}
}

func TestSetTime(t *testing.T) {
	home := shanghai(t)

	cases := []struct {
		name       string
		start, end string
		wantStart  time.Time
		wantEnd    time.Time
		wantOff    int
		wantLocal  string
	}{
		{
			// 起止都带偏移：UTC 落值，local_date 按 payload 偏移，跨零点归到起始日。
			name:      "带偏移的区间",
			start:     "2026-10-13 23:50:00 +0800",
			end:       "2026-10-14 00:10:00 +0800",
			wantStart: time.Date(2026, 10, 13, 15, 50, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 10, 13, 16, 10, 0, 0, time.UTC),
			wantOff:   480,
			wantLocal: "2026-10-13",
		},
		{
			name:      "负偏移记录",
			start:     "2026-10-13 20:00:00 -0800",
			end:       "2026-10-13 20:30:00 -0800",
			wantStart: time.Date(2026, 10, 14, 4, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 10, 14, 4, 30, 0, 0, time.UTC),
			wantOff:   -480,
			wantLocal: "2026-10-13",
		},
		{
			// end 为空：Normalize 把 EndTime 拉成 StartTime（点状样本的约定）。
			name:      "只有起始时间",
			start:     "2026-10-13 08:00:00 +0800",
			wantStart: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
			wantOff:   480,
			wantLocal: "2026-10-13",
		},
		{
			// end 非法时忽略而非报错：HAE 的 end 字段偶尔是空串或乱码，
			// 主时间轴仍然可用，不能整条样本丢弃。
			name:      "end 非法被忽略",
			start:     "2026-10-13 08:00:00 +0800",
			end:       "not-a-time",
			wantStart: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
			wantOff:   480,
			wantLocal: "2026-10-13",
		},
		{
			name:      "end 早于 start 被拉平",
			start:     "2026-10-13 08:00:00 +0800",
			end:       "2026-10-13 07:00:00 +0800",
			wantStart: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
			wantOff:   480,
			wantLocal: "2026-10-13",
		},
		{
			// 无偏移：按 home 解释时刻，偏移落库为 home 当日偏移。
			name:      "无偏移按 home 解释",
			start:     "2026-10-13 23:30:00",
			end:       "2026-10-14 00:30:00",
			wantStart: time.Date(2026, 10, 13, 15, 30, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 10, 13, 16, 30, 0, 0, time.UTC),
			wantOff:   480,
			wantLocal: "2026-10-13",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Sample{Metric: "step_count", Value: 1}
			if !s.SetTime(tc.start, tc.end, home) {
				t.Fatalf("SetTime(%q,%q) 应成功", tc.start, tc.end)
			}
			if !s.StartTime.Equal(tc.wantStart) {
				t.Errorf("StartTime = %s，期望 %s", s.StartTime, tc.wantStart)
			}
			if !s.EndTime.Equal(tc.wantEnd) {
				t.Errorf("EndTime = %s，期望 %s", s.EndTime, tc.wantEnd)
			}
			if s.TZOffsetMin != tc.wantOff {
				t.Errorf("TZOffsetMin = %d，期望 %d", s.TZOffsetMin, tc.wantOff)
			}
			if s.LocalDate != tc.wantLocal {
				t.Errorf("LocalDate = %q，期望 %q", s.LocalDate, tc.wantLocal)
			}
			if s.EndTime.Before(s.StartTime) {
				t.Error("Normalize 之后仍出现 EndTime 早于 StartTime")
			}
		})
	}
}

// 纯日期的聚合睡眠记录：偏移未知，日期与偏移都来自 home。
func TestSetTimeDateOnly(t *testing.T) {
	home := shanghai(t)
	s := Sample{Metric: "sleep_analysis"}
	if !s.SetTime("2026-10-13", "", home) {
		t.Fatal("仅日期的记录应解析成功")
	}
	if s.LocalDate != "2026-10-13" {
		t.Errorf("LocalDate = %q", s.LocalDate)
	}
	if s.TZOffsetMin != 480 {
		t.Errorf("TZOffsetMin = %d，期望 home 偏移 480", s.TZOffsetMin)
	}
	if !s.EndTime.Equal(s.StartTime) {
		t.Errorf("点记录 EndTime 应等于 StartTime，实际 %s", s.EndTime)
	}
}

func TestSetTimeWithoutHomeLocation(t *testing.T) {
	s := Sample{Metric: "heart_rate"}
	if !s.SetTime("2026-10-13 23:30:00", "", nil) {
		t.Fatal("应解析成功")
	}
	// 没有 home：时刻按 UTC 解释，偏移记 0，local_date 也按 UTC。
	if want := time.Date(2026, 10, 13, 23, 30, 0, 0, time.UTC); !s.StartTime.Equal(want) {
		t.Errorf("StartTime = %s，期望 %s", s.StartTime, want)
	}
	if s.TZOffsetMin != 0 {
		t.Errorf("TZOffsetMin = %d，期望 0", s.TZOffsetMin)
	}
	if s.LocalDate != "2026-10-13" {
		t.Errorf("LocalDate = %q", s.LocalDate)
	}
}
