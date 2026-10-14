package postgres

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// 集成测试（需 ZHICHANG_TEST_DSN，未设置自动跳过）：锁定「按入睡时间窗关联生理指标」的口径。
//
// 生理指标（心率/HRV/血氧/呼吸频率）的样本 local_date 按各自起始日归属，
// 跨零点后半夜的样本落在次日，而睡眠会话的 local_date 是入睡时刻那天。
// 若按 local_date 关联，凌晨那段（恰恰是夜间最低心率所在）会被整段漏掉。

// hr 造一条心率样本：瞬时量，end=start。
func hr(src string, st time.Time, localDate string, avg, min, max float64) sample.Sample {
	return sample.Sample{
		Metric: "heart_rate", Source: src,
		StartTime: st, EndTime: st,
		Value: avg, ValueMin: &min, ValueMax: &max,
		Unit: "count/min", LocalDate: localDate, TZOffsetMin: 480,
	}
}

// TestSleepNightMetricsUsesSleepWindow 锁定 /api/sleep/night-metrics 的口径：
//   - 窗口外的样本（入睡前、起床后）不算；
//   - 跨零点后半夜的样本必须收进来，哪怕它的 local_date 是次日；
//   - 一晚只认一个来源：会话来源优先，即使另一来源样本更多也不平均；
//   - 最低值取样本自带的 value_min（Apple 的夜间最低心率就是这个）；
//   - 同一晚有午睡 + 夜觉时，窗口取夜觉那条。
func TestSleepNightMetricsUsesSleepWindow(t *testing.T) {
	db := openSleepDB(t)
	ctx := context.Background()

	nightStart, nightEnd := at0800(10, 12, 23, 30), at0800(10, 13, 7, 0)
	if _, err := db.WriteSleep(ctx, 1, []sample.SleepSession{
		{
			LocalDate: "2026-10-12", TZOffsetMin: 480, Source: "Apple Watch",
			SleepStart: &nightStart, SleepEnd: &nightEnd, TotalSleepHr: fptr(7.5),
		},
		// 同一晚两条会话：午睡 + 夜觉，窗口必须取夜觉
		{
			LocalDate: "2026-10-11", TZOffsetMin: 480, Source: "Apple Watch",
			SleepStart: ptr(at0800(10, 11, 13, 0)), SleepEnd: ptr(at0800(10, 11, 14, 0)),
			TotalSleepHr: fptr(1.0),
		},
		{
			LocalDate: "2026-10-11", TZOffsetMin: 480, Source: "Apple Watch",
			SleepStart: ptr(at0800(10, 11, 23, 0)), SleepEnd: ptr(at0800(10, 12, 6, 0)),
			TotalSleepHr: fptr(7.0),
		},
		// 完全没有心率样本的一晚：不应出现在结果里
		{
			LocalDate: "2026-10-09", TZOffsetMin: 480, Source: "Apple Watch",
			SleepStart: ptr(at0800(10, 9, 23, 0)), SleepEnd: ptr(at0800(10, 10, 6, 30)),
			TotalSleepHr: fptr(7.0),
		},
	}); err != nil {
		t.Fatalf("写入睡眠会话失败: %v", err)
	}

	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		// 窗口内（入睡当晚）
		hr("Apple Watch", at0800(10, 12, 23, 50), "2026-10-12", 58, 56, 60),
		// 跨零点的后半夜：local_date 是次日，按日期关联就会漏掉这条
		hr("Apple Watch", at0800(10, 13, 2, 10), "2026-10-13", 48, 46, 50),
		// 另一来源样本更多，但会话来源优先
		hr("iPhone", at0800(10, 13, 1, 0), "2026-10-13", 90, 88, 92),
		hr("iPhone", at0800(10, 13, 1, 5), "2026-10-13", 91, 89, 93),
		hr("iPhone", at0800(10, 13, 1, 10), "2026-10-13", 92, 90, 94),
		// 窗口外：入睡前 / 起床后
		hr("Apple Watch", at0800(10, 12, 22, 0), "2026-10-12", 65, 64, 66),
		hr("Apple Watch", at0800(10, 13, 8, 30), "2026-10-13", 70, 69, 71),
		// 10-11 夜觉窗口内 + 午睡窗口内的样本
		hr("Apple Watch", at0800(10, 12, 2, 0), "2026-10-12", 50, 49, 51),
		hr("Apple Watch", at0800(10, 11, 13, 30), "2026-10-11", 80, 79, 81),
	}, "strict"); err != nil {
		t.Fatalf("写入心率样本失败: %v", err)
	}

	rows, err := db.SleepNightMetrics(ctx, "2026-10-09", "2026-10-12", []string{"heart_rate"})
	if err != nil {
		t.Fatalf("SleepNightMetrics 失败: %v", err)
	}
	if rows == nil {
		t.Fatal("结果不能是 nil slice，会被编码成 JSON null")
	}
	byDate := map[string]NightMetricRow{}
	for _, r := range rows {
		byDate[r.LocalDate] = r
	}
	if len(rows) != 2 {
		t.Fatalf("期望 2 晚有数据（无样本的那晚不该出行），实际 %d：%v", len(rows), rows)
	}

	n := byDate["2026-10-12"]
	if n.Source != "Apple Watch" {
		t.Fatalf("来源应择优会话来源，实际 %q", n.Source)
	}
	if n.SampleCount != 2 {
		t.Fatalf("期望窗口内 2 个点（跨零点那条要收进来），实际 %d", n.SampleCount)
	}
	if n.Min != 46 {
		t.Fatalf("夜间最低应取样本的 value_min=46，实际 %v", n.Min)
	}
	if n.Max != 60 {
		t.Fatalf("夜间最高应取样本的 value_max=60，实际 %v", n.Max)
	}
	if math.Abs(n.Avg-53) > 1e-9 {
		t.Fatalf("夜间平均应为 (58+48)/2=53，实际 %v", n.Avg)
	}
	if want := at0800(10, 13, 2, 10).UTC().Format(time.RFC3339); n.MinAt != want {
		t.Fatalf("最低值时刻应为凌晨那条 %s，实际 %q", want, n.MinAt)
	}
	// 还原钟点必须用打出最低值那条样本自带的偏移，不能拿 0 当 UTC 凑
	if n.MinAtTZOffsetMin == nil || *n.MinAtTZOffsetMin != 480 {
		t.Fatalf("min_at 的偏移应为样本自带的 480，实际 %v", n.MinAtTZOffsetMin)
	}

	// 午睡不算夜觉：10-11 只应统计夜觉窗口内的凌晨样本
	if p := byDate["2026-10-11"]; p.SampleCount != 1 || p.Min != 49 {
		t.Fatalf("10-11 应只取夜觉窗口（1 点、最低 49），实际 %d 点 / 最低 %v", p.SampleCount, p.Min)
	}
	if _, ok := byDate["2026-10-09"]; ok {
		t.Fatal("没有心率样本的夜晚不该出行")
	}

	// 白名单外的指标一律忽略；空结果必须是 []
	if got, err := db.SleepNightMetrics(ctx, "2026-10-09", "2026-10-12", []string{"step_count"}); err != nil {
		t.Fatalf("白名单过滤失败: %v", err)
	} else if len(got) != 0 || got == nil {
		t.Fatalf("白名单外应返回空数组，实际 %#v", got)
	}
}

// TestSleepStagesNightPhysiology 锁定分期时间线上的叠加曲线数据：
//   - 与分段共用同一条本夜时间轴（local_min 跨零点顺延）；
//   - 窗口外的点被排除；
//   - 无数据时 physiology 是空数组而不是 null。
func TestSleepStagesNightPhysiology(t *testing.T) {
	db := openSleepDB(t)
	ctx := context.Background()

	nightStart, nightEnd := at0800(10, 12, 23, 30), at0800(10, 13, 7, 0)
	if _, err := db.WriteSleep(ctx, 1, []sample.SleepSession{
		{
			LocalDate: "2026-10-12", TZOffsetMin: 480, Source: "Apple Watch",
			SleepStart: &nightStart, SleepEnd: &nightEnd, TotalSleepHr: fptr(7.5),
		},
	}); err != nil {
		t.Fatalf("写入睡眠会话失败: %v", err)
	}
	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		hr("Apple Watch", at0800(10, 12, 23, 50), "2026-10-12", 58, 56, 60),
		hr("Apple Watch", at0800(10, 13, 2, 10), "2026-10-13", 48, 46, 50),
		hr("Apple Watch", at0800(10, 13, 8, 30), "2026-10-13", 70, 69, 71), // 起床后：排除
	}, "strict"); err != nil {
		t.Fatalf("写入心率样本失败: %v", err)
	}

	got, err := db.SleepStages(ctx, "2026-10-12")
	if err != nil {
		t.Fatalf("SleepStages 失败: %v", err)
	}
	if len(got.Physiology) != 1 {
		t.Fatalf("期望 1 条夜间序列，实际 %d", len(got.Physiology))
	}
	s := got.Physiology[0]
	if s.Metric != "heart_rate" || s.Source != "Apple Watch" {
		t.Fatalf("指标或来源不符：%s / %s", s.Metric, s.Source)
	}
	if len(s.Points) != 2 {
		t.Fatalf("期望 2 个点（起床后那条排除），实际 %d", len(s.Points))
	}
	// 23:50 → 1430；凌晨 02:10 在本夜轴上是 130+1440=1570
	if s.Points[0].LocalMin != 1430 || s.Points[1].LocalMin != 1570 {
		t.Fatalf("本夜轴位置不符：%v / %v", s.Points[0].LocalMin, s.Points[1].LocalMin)
	}
	if s.Min != 46 || s.MinLocalMin != 1570 {
		t.Fatalf("最低值与其位置应为 46 @1570，实际 %v @%v", s.Min, s.MinLocalMin)
	}

	// 没有分期也没有样本的一晚：契约要求空数组
	empty, err := db.SleepStages(ctx, "2026-10-01")
	if err != nil {
		t.Fatalf("空夜晚查询失败: %v", err)
	}
	if empty.Physiology == nil {
		t.Fatal("physiology 不能是 nil slice，会被编码成 JSON null")
	}
}

func ptr(t time.Time) *time.Time { return &t }
