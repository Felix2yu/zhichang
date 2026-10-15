package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

func TestMaskNumbers(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"小数", "步数: 7143.00 count", "步数: " + Redacted + " count"},
		{"不带单位的整数", "| 2026-10-13 | 7143 |", "| 2026-10-13 | " + Redacted + " |"},
		{"百分号", "变化 +12.5%", "变化 " + Redacted},
		{"日期保留", "2026-10-13 概览", "2026-10-13 概览"},
		{"钟点保留", "入睡 23:30 · 起床 07:10", "入睡 23:30 · 起床 07:10"},
		{"ISO 时间戳整体保留", "2026-10-13T23:16:15Z 一次测量", "2026-10-13T23:16:15Z 一次测量"},
		{"计数一起挡", "共 48 项，近 30 天", "共 " + Redacted + " 项，近 " + Redacted + " 天"},
		{"千分位", "总 1,701,537 步", "总 " + Redacted + " 步"},
		{"没有数字原样返回", "窦性心律", "窦性心律"},
	}
	for _, tt := range tests {
		if got := maskNumbers(tt.in); got != tt.want {
			t.Fatalf("%s: 期望 %q，实际 %q", tt.name, tt.want, got)
		}
	}
}

// TestRedactHidesEveryToolValue 把 11 个工具都跑一遍脱敏档。
//
// 断言用的是「种进去的值有没有出现」而不是「文本里还有没有数字」：
// 前者能抓到漏网的取值点（比如某个表格列没走占位符），后者会被保留的日期干扰。
func TestRedactHidesEveryToolValue(t *testing.T) {
	d := newDeps(t)
	ctx := context.Background()
	today := day(0)

	putDaily(t, d, daily("step_count", today, 7143), daily("active_energy", today, 512.75),
		daily("heart_rate", today, 88.4), daily("resting_heart_rate", today, 63.5),
		daily("heart_rate_variability", today, 47.2), daily("oxygen_saturation", today, 96.3))
	putSleep(t, d, sample.SleepSession{
		LocalDate: today, Source: "Apple Watch", TZOffsetMin: 480,
		SleepStart: ptrAt(at(today, 23, 30)), SleepEnd: ptrAt(atMinute(nextDay(today), 420)),
		TotalSleepHr: f64(7.5), DeepHr: f64(1.25), CoreHr: f64(4.5), RemHr: f64(1.4), AwakeHr: f64(0.35),
	})
	total, dist, dur, avgHR, maxHR := 410.5, 8.25, 47.5, 133.7, 168.2
	putWorkouts(t, d, sample.Workout{
		Name: "户外 跑步", Source: "Apple Watch", StartTime: at(today, 7, 0), EndTime: at(today, 7, 47),
		DurationMin: dur, TotalEnergy: &total, EnergyUnit: "kcal", TotalDistance: &dist, DistanceUnit: "km",
		AvgHR: &avgHR, MaxHR: &maxHR, LocalDate: today, TZOffsetMin: 480,
	})
	// 心电：平均心率与采样率都是数值
	if _, err := d.DB.Pool.Exec(ctx, `INSERT INTO ecg_records
		(start_time, end_time, classification, classification_code, average_heart_rate,
		 sampling_frequency, voltage_count, source, local_date, tz_offset_min)
		VALUES ($1,$1,'窦性心律','sinus_rhythm',71.5,512,15360,'手表',$2,480)`,
		at(today, 22, 15), today); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DB.Pool.Exec(ctx, `INSERT INTO ingest_runs
		(transport, session_id, sample_count, inserted, workout_count, sleep_count, ecg_count, status)
		VALUES ('rest','r-seed',12,12,1,1,1,'ok')`); err != nil {
		t.Fatal(err)
	}

	seeds := []string{"7143", "512.75", "88.4", "63.5", "47.2", "96.3", "7.5", "1.25", "4.5", "1.4",
		"410.5", "8.25", "133.7", "168.2", "71.5", "512", "15360", "12.5"}

	cases := []struct {
		name string
		call func() (string, error)
	}{
		{"list_metrics", func() (string, error) { return d.ListMetrics(ctx) }},
		{"daily_summary", func() (string, error) { return d.DailySummary(ctx, today) }},
		{"query", func() (string, error) { return d.Query(ctx, "step_count,heart_rate", today, today) }},
		{"compare", func() (string, error) {
			return d.Compare(ctx, "step_count", day(-6), today, day(-13), day(-7))
		}},
		{"latest", func() (string, error) { return d.Latest(ctx, "step_count,heart_rate") }},
		{"sleep", func() (string, error) { return d.Sleep(ctx, day(-6), today) }},
		{"sleep_detail", func() (string, error) { return d.SleepDetail(ctx, today) }},
		{"workouts", func() (string, error) { return d.Workouts(ctx, day(-6), today) }},
		{"ecg", func() (string, error) { return d.ECG(ctx, day(-6), today) }},
		{"coverage", func() (string, error) { return d.Coverage(ctx, 7) }},
		{"correlation", func() (string, error) {
			return d.Correlation(ctx, "step_count", "active_energy", day(-6), today)
		}},
	}

	for _, tc := range cases {
		// 同一个工具跑两遍：不脱敏时种子值必须出现（否则这条用例什么都没锁住），
		// 脱敏后一个都不许出现。
		//
		// 必须经 result() 这个出口调用——生产路径上脱敏就挂在那里，
		// 直接调工具方法等于绕过被测行为。
		d.Redact = false
		_, plain, err := d.result(tc.call())
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if strings.TrimSpace(plain.Text) == "" {
			t.Fatalf("%s 输出为空，用例锁不住任何东西", tc.name)
		}
		d.Redact = true
		_, masked, err := d.result(tc.call())
		d.Redact = false
		if err != nil {
			t.Fatalf("%s(脱敏): %v", tc.name, err)
		}
		for _, v := range seeds {
			if strings.Contains(masked.Text, v) && strings.Contains(plain.Text, v) {
				t.Fatalf("%s 脱敏后仍带着测量值 %q:\n%s", tc.name, v, masked.Text)
			}
		}
		// 结构要留下：日期与指标名不许被一起挡掉，否则脱敏档就废了
		if !strings.Contains(masked.Text, today) && strings.Contains(plain.Text, today) {
			t.Fatalf("%s 把日期也挡掉了，脱敏后应仍能问「哪几天有数据」:\n%s", tc.name, masked.Text)
		}
	}
}

// 错误分支也要过脱敏：数据库报错常把出问题的值原样带出来。
func TestRedactCoversErrorBranch(t *testing.T) {
	d := newDeps(t)
	d.Redact = true
	_, res, err := d.result("", &fakeErr{msg: "值 7143.5 越界"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "7143.5") {
		t.Fatalf("错误文本未脱敏: %s", res.Text)
	}
	if !strings.Contains(res.Text, Redacted) {
		t.Fatalf("错误文本应带占位符: %s", res.Text)
	}
}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }
