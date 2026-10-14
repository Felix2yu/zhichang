package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// 集成测试：需要 ZHICHANG_TEST_DSN 指向可用的 TimescaleDB，
// 未设置时自动跳过（与 rollup_test / ingest_test 同一约定）。

// openSleepDB 复用 rollup 的库句柄，另清空 sleep_sessions。
func openSleepDB(t *testing.T) *DB {
	t.Helper()
	db := openRollupDB(t)
	if _, err := db.Pool.Exec(context.Background(), `TRUNCATE sleep_sessions`); err != nil {
		t.Fatalf("清理 sleep_sessions 失败: %v", err)
	}
	return db
}

// at0800 把 +0800 的墙钟时间转成 UTC，便于按本地时间书写断言。
func at0800(mo, d, h, mi int) time.Time {
	return time.Date(2026, time.Month(mo), d, h-8, mi, 0, 0, time.UTC)
}

func fptr(v float64) *float64 { return &v }

// TestSleepRangeExposesAwakeAndInterruptions 锁定 /api/sleep 的口径扩展：
//   - awake_hr 与 tz_offset_min 必须原样透出（前端要靠后者还原本地入睡时刻）
//   - awake_count 统计「入睡时间窗内的清醒分段数」：跨零点后的分段 local_date
//     与睡眠会话的 local_date 并不相同（会话以入睡时刻归属，分段按各自起始日归属），
//     所以必须按时间窗而不是按 local_date 关联，否则凌晨的清醒段全被漏掉。
//     没有分段样本的夜晚必须回 NULL，前端据此回退到「清醒占比」推算。
func TestSleepRangeExposesAwakeAndInterruptions(t *testing.T) {
	db := openSleepDB(t)
	ctx := context.Background()

	// 第一晚：入睡 10-12 23:30，起床 10-13 07:00（睡眠日归属入睡时刻的本地日期）
	sleepStart := at0800(10, 12, 23, 30)
	sleepEnd := at0800(10, 13, 7, 0)
	inBedStart := at0800(10, 12, 23, 10)
	inBedEnd := at0800(10, 13, 7, 10)
	night := sample.SleepSession{
		LocalDate: "2026-10-12", TZOffsetMin: 480, Source: "Apple Watch",
		SleepStart: &sleepStart, SleepEnd: &sleepEnd,
		InBedStart: &inBedStart, InBedEnd: &inBedEnd,
		TotalSleepHr: fptr(7.5), AsleepHr: fptr(7.1), CoreHr: fptr(3.5),
		DeepHr: fptr(1.5), RemHr: fptr(2.0), AwakeHr: fptr(0.4), InBedHr: fptr(8.0),
	}

	// 另一晚：完全没有分段样本，用于断言 awake_count 为 NULL
	otherStart := at0800(10, 10, 23, 0)
	otherEnd := at0800(10, 11, 6, 30)
	other := sample.SleepSession{
		LocalDate: "2026-10-10", TZOffsetMin: 480, Source: "Apple Watch",
		SleepStart: &otherStart, SleepEnd: &otherEnd,
		TotalSleepHr: fptr(6.8), AwakeHr: fptr(0.2), InBedHr: fptr(7.0),
	}

	if _, err := db.WriteSleep(ctx, 1, []sample.SleepSession{night, other}); err != nil {
		t.Fatalf("写入睡眠会话失败: %v", err)
	}

	segment := func(st time.Time, stage string, hours float64, localDate string) sample.Sample {
		return sample.Sample{
			Metric: "sleep_analysis", Source: "Apple Watch",
			StartTime: st, EndTime: st.Add(time.Minute), Value: hours, Unit: "hr",
			LocalDate: localDate, TZOffsetMin: 480, Meta: map[string]any{"stage": stage},
		}
	}
	segments := []sample.Sample{
		// 窗口内的两段清醒；跨零点后的分段 local_date 是 10-13，与会话的 10-12 不同
		segment(at0800(10, 13, 2, 10), "Awake", 0.05, "2026-10-13"),
		segment(at0800(10, 13, 4, 30), "Awake", 0.10, "2026-10-13"),
		// 早于入睡时刻：不算中断
		segment(at0800(10, 12, 22, 0), "Awake", 0.20, "2026-10-12"),
		// 非清醒分段：不算中断
		segment(at0800(10, 13, 2, 20), "Core", 0.90, "2026-10-13"),
	}
	if _, _, _, _, err := db.WriteSamples(ctx, 1, segments, "strict"); err != nil {
		t.Fatalf("写入分段样本失败: %v", err)
	}

	rows, err := db.SleepRange(ctx, "2026-10-10", "2026-10-12")
	if err != nil {
		t.Fatalf("SleepRange 失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("期望 2 行，实际 %d", len(rows))
	}
	got := rows[0] // ORDER BY local_date → 2026-10-10
	if got.LocalDate != "2026-10-10" || rows[1].LocalDate != "2026-10-12" {
		t.Fatalf("排序不符：%s / %s", got.LocalDate, rows[1].LocalDate)
	}
	if got.AwakeCount != nil {
		t.Fatalf("无分段样本的夜晚 awake_count 应为 NULL，实际 %d", *got.AwakeCount)
	}

	n := rows[1]
	if n.AwakeHr == nil || *n.AwakeHr != 0.4 {
		t.Fatalf("awake_hr 未透出，实际 %v", n.AwakeHr)
	}
	if n.TZOffsetMin != 480 {
		t.Fatalf("tz_offset_min 未透出，实际 %d", n.TZOffsetMin)
	}
	if n.SleepStart == nil || *n.SleepStart != sleepStart.UTC().Format(time.RFC3339) {
		t.Fatalf("sleep_start 不符，实际 %v", n.SleepStart)
	}
	if n.AwakeCount == nil {
		t.Fatal("有分段样本的夜晚 awake_count 不应为 NULL")
	}
	if *n.AwakeCount != 2 {
		t.Fatalf("期望 2 段清醒（窗口外与非清醒段不算），实际 %d", *n.AwakeCount)
	}
}

// TestSleepStagesTimeline 锁定分期时间线的口径：
//   - 分段按「入睡时间窗」而非 local_date 关联，凌晨的段（local_date 是次日）必须收进来；
//   - local_min 以睡眠日本地 00:00 为 0 并跨零点顺延（03:00 → 180+1440）；
//   - 窗口外（次日晚间）的段必须排除；
//   - 多来源时择优，会话来源优先；
//   - 在床（inbed）只统计不参与阶段合计的语义由前端决定，这里只确认它被识别。
func TestSleepStagesTimeline(t *testing.T) {
	db := openSleepDB(t)
	ctx := context.Background()

	sleepStart := at0800(10, 12, 23, 30)
	sleepEnd := at0800(10, 13, 7, 0)
	night := sample.SleepSession{
		LocalDate: "2026-10-12", TZOffsetMin: 480, Source: "Apple Watch",
		SleepStart: &sleepStart, SleepEnd: &sleepEnd,
		TotalSleepHr: fptr(7.0), CoreHr: fptr(3.0), DeepHr: fptr(1.0), RemHr: fptr(2.0),
	}
	if _, err := db.WriteSleep(ctx, 1, []sample.SleepSession{night}); err != nil {
		t.Fatalf("写入睡眠会话失败: %v", err)
	}

	seg := func(src string, mo, d, h, mi, durMin int, stage, localDate string) sample.Sample {
		st := at0800(mo, d, h, mi)
		return sample.Sample{
			Metric: "sleep_analysis", Source: src,
			StartTime: st, EndTime: st.Add(time.Duration(durMin) * time.Minute),
			Value: float64(durMin) / 60, Unit: "hr",
			LocalDate: localDate, TZOffsetMin: 480, Meta: map[string]any{"stage": stage},
		}
	}
	segments := []sample.Sample{
		// 窗口内：23:30-00:30 Core（跨零点，local_date 分别落在 10-12 与 10-13）
		seg("Apple Watch", 10, 12, 23, 30, 30, "AsleepCore", "2026-10-12"),
		seg("Apple Watch", 10, 13, 0, 0, 30, "AsleepCore", "2026-10-13"),
		// 凌晨 03:00 深睡 → 本夜轴上应为 180+1440=1620
		seg("Apple Watch", 10, 13, 3, 0, 60, "AsleepDeep", "2026-10-13"),
		// 凌晨清醒 20 分钟
		seg("Apple Watch", 10, 13, 4, 30, 20, "Awake", "2026-10-13"),
		// 在床（不渲染但应被识别）
		seg("Apple Watch", 10, 12, 23, 10, 10, "InBed", "2026-10-12"),
		// 窗口外：次日晚 20:00 的另一觉，必须排除
		seg("Apple Watch", 10, 13, 20, 0, 30, "AsleepREM", "2026-10-13"),
		// 另一来源：覆盖率低，会话来源 Apple Watch 应胜出
		seg("iPhone", 10, 13, 2, 0, 5, "Awake", "2026-10-13"),
	}
	if _, _, _, _, err := db.WriteSamples(ctx, 1, segments, "strict"); err != nil {
		t.Fatalf("写入分期样本失败: %v", err)
	}

	got, err := db.SleepStages(ctx, "2026-10-12")
	if err != nil {
		t.Fatalf("SleepStages 失败: %v", err)
	}
	if !got.HasSession {
		t.Fatal("应识别到睡眠会话")
	}
	if got.Source != "Apple Watch" {
		t.Fatalf("来源择优错误：期望 Apple Watch，实际 %q", got.Source)
	}
	if len(got.Segments) != 6 {
		t.Fatalf("期望 6 段（排除窗口外的 20:00 那段），实际 %d", len(got.Segments))
	}

	// 凌晨 03:00 的深睡应落在 1620（03:00 + 1440）
	var deepMin int = -1
	for _, s := range got.Segments {
		if s.Stage == "deep" {
			deepMin = s.LocalMin
		}
		if s.Raw == "AsleepREM" {
			t.Fatal("窗口外的 REM 分段不应出现")
		}
		if s.Source == "iPhone" {
			t.Fatal("非择优来源的分段不应出现")
		}
	}
	if deepMin != 1620 {
		t.Fatalf("凌晨 03:00 的 local_min 应为 1620，实际 %d", deepMin)
	}

	// 阶段归一化与合计：core 60min=1h、deep 1h、awake 20min、inbed 10min
	if len(got.Segments) == 0 {
		t.Fatal("分段为空")
	}
	first := got.Segments[0]
	if first.Stage != "core" {
		t.Fatalf("AsleepCore 应归一化为 core，实际 %q", first.Stage)
	}
	if v := got.Totals["core"]; v < 0.99 || v > 1.01 {
		t.Fatalf("core 合计应为 1h，实际 %v", v)
	}
	if v := got.Totals["awake"]; v < 0.32 || v > 0.35 {
		t.Fatalf("awake 合计应为 20min(0.33h)，实际 %v", v)
	}
	if v, ok := got.Totals["inbed"]; !ok || v < 0.16 || v > 0.17 {
		t.Fatalf("inbed 应被单独统计为 10min，实际 %v(存在=%v)", v, ok)
	}

	latest, err := db.LatestSleepStageDate(ctx)
	if err != nil {
		t.Fatalf("LatestSleepStageDate 失败: %v", err)
	}
	if latest != "2026-10-13" {
		t.Fatalf("最近有分期的日期应为 2026-10-13，实际 %q", latest)
	}
}
