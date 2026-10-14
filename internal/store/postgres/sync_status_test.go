package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// sync_status_test.go 锁 /api/sync/health 的数据口径：空库首启也要能渲染，
// 缺口天数按 step_count 算，且所有 slice 字段绝不能是 null。

// currentDate 读库里的 CURRENT_DATE：缺口是按它算的，不能在 Go 侧猜当天。
func currentDate(t *testing.T, db *DB) time.Time {
	t.Helper()
	var s string
	if err := db.Pool.QueryRow(context.Background(), `SELECT CURRENT_DATE::text`).Scan(&s); err != nil {
		t.Fatalf("读 CURRENT_DATE 失败: %v", err)
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("CURRENT_DATE 格式不对: %q", s)
	}
	return d
}

// TestSyncStatusOfOnEmptyDB 锁定空库的契约：三个 slice 字段都是 []，
// last_run 整个消失（nil 指针 + omitempty），缺口天数 = days + 1（含今天）。
func TestSyncStatusOfOnEmptyDB(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	for _, days := range []int{0, 1, 7, 30} {
		st, err := db.SyncStatusOf(ctx, days)
		if err != nil {
			t.Fatalf("空库同步状态查询失败: %v", err)
		}
		if st.LastRun != nil {
			t.Fatalf("没有 run 时 last_run 必须是 nil，实际 %+v", st.LastRun)
		}
		if st.RecentRuns == nil || st.Watermarks == nil || st.MissingDays == nil {
			t.Fatalf("slice 字段不能是 nil（JSON 会变 null）：%+v", st)
		}
		if len(st.RecentRuns) != 0 || len(st.Watermarks) != 0 {
			t.Fatalf("空库不该有 run/水位：%+v", st)
		}
		// generate_series 两端都含：CURRENT_DATE - days .. CURRENT_DATE → days+1 天
		if got := len(st.MissingDays); got != days+1 {
			t.Fatalf("days=%d 时期望 %d 个缺口，实际 %d", days, days+1, got)
		}
		want := currentDate(t, db)
		if st.MissingDays[len(st.MissingDays)-1] != want.Format("2006-01-02") {
			t.Fatalf("缺口序列最后一天必须是今天，实际 %q", st.MissingDays[len(st.MissingDays)-1])
		}
		if st.MissingDays[0] != want.AddDate(0, 0, -days).Format("2006-01-02") {
			t.Fatalf("缺口序列第一天应是今天减 %d 天，实际 %q", days, st.MissingDays[0])
		}
		// 升序
		for i := 1; i < len(st.MissingDays); i++ {
			if st.MissingDays[i] <= st.MissingDays[i-1] {
				t.Fatalf("缺口日期必须升序：%v", st.MissingDays)
			}
		}
		// HomeTZ 不由 store 填（口径留在 config 层），必须是空串
		if st.HomeTZ != "" {
			t.Fatalf("store 不该填 HomeTZ，实际 %q", st.HomeTZ)
		}
		b, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "null") {
			t.Fatalf("days=%d 的响应里出现了 null，前端会崩：%s", days, b)
		}
	}

	// days 为负：起点在终点之后，generate_series 自然为空而不是报错
	st, err := db.SyncStatusOf(ctx, -1)
	if err != nil {
		t.Fatalf("days=-1 不该报错: %v", err)
	}
	if len(st.MissingDays) != 0 {
		t.Fatalf("days=-1 应是空缺口数组，实际 %v", st.MissingDays)
	}
}

// TestSyncStatusOfRunsWatermarksAndGaps 锁定有数据时的口径：
//   - recent_runs 按 received_at 倒序，last_run 就是最近那次；
//   - automation_id / session_id 为 NULL 时经 COALESCE 落成空串；
//   - error 为 NULL 时字段整个消失，失败的那次要把原因带出来；
//   - 水位按 metric 升序，时刻是 UTC RFC3339，日期是 YYYY-MM-DD；
//   - 缺口只按 step_count 判定：别的指标有数不算补上。
func TestSyncStatusOfRunsWatermarksAndGaps(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()
	today := currentDate(t, db)
	d1 := today.AddDate(0, 0, -1).Format("2006-01-02")
	d0 := today.Format("2006-01-02")

	older, _, err := db.BeginRun(ctx, RunMeta{Transport: "webhook", AutomationID: "auto-1", SessionID: "sess-old"})
	if err != nil {
		t.Fatal(err)
	}
	newest, _, err := db.BeginRun(ctx, RunMeta{Transport: "upload"})
	if err != nil {
		t.Fatal(err)
	}
	failed, _, err := db.BeginRun(ctx, RunMeta{Transport: "webhook", AutomationID: "auto-2", SessionID: "sess-fail"})
	if err != nil {
		t.Fatal(err)
	}
	// received_at 是 now()（同一微秒内可能撞），显式拉开才保证倒序可断言
	mustExec(t, db, `UPDATE ingest_runs SET received_at = '2026-10-11T00:00:00Z'::timestamptz WHERE id = $1`, older)
	mustExec(t, db, `UPDATE ingest_runs SET received_at = '2026-10-13T00:00:00Z'::timestamptz WHERE id = $1`, newest)
	mustExec(t, db, `UPDATE ingest_runs SET received_at = '2026-10-12T00:00:00Z'::timestamptz WHERE id = $1`, failed)

	if err := db.FinishRun(ctx, older, IngestStats{Received: 5, Inserted: 4, Duplicates: 1, AffectedFrom: d1, AffectedTo: d0}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishRun(ctx, newest, IngestStats{Received: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishRun(ctx, failed, IngestStats{}, fmt.Errorf("payload 不是合法 JSON")); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateWatermark(ctx, "auto-1", map[string]Watermark{
		"step_count":    {Time: time.Date(2026, 10, 12, 16, 30, 0, 0, time.FixedZone("CST", 8*3600)), LocalDate: d1},
		"active_energy": {Time: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), LocalDate: "2026-10-11"},
	}); err != nil {
		t.Fatal(err)
	}

	st, err := db.SyncStatusOf(ctx, 1)
	if err != nil {
		t.Fatalf("SyncStatusOf 失败: %v", err)
	}
	if len(st.RecentRuns) != 3 {
		t.Fatalf("期望 3 次 run，实际 %d", len(st.RecentRuns))
	}
	if st.RecentRuns[0].ID != newest || st.RecentRuns[1].ID != failed || st.RecentRuns[2].ID != older {
		t.Fatalf("recent_runs 必须按接收时间倒序：%v", st.RecentRuns)
	}
	if st.LastRun == nil || st.LastRun.ID != newest {
		t.Fatalf("last_run 应是最近那次：%+v", st.LastRun)
	}
	if st.RecentRuns[0].AutomationID != "" || st.RecentRuns[0].SessionID != "" {
		t.Fatalf("NULL 的 automation/session 应经 COALESCE 落成空串，实际 %q/%q",
			st.RecentRuns[0].AutomationID, st.RecentRuns[0].SessionID)
	}
	if st.RecentRuns[0].Error != nil {
		t.Fatalf("成功 run 的 error 必须是指针 nil（JSON 里字段消失），实际 %v", *st.RecentRuns[0].Error)
	}
	if st.RecentRuns[1].Error == nil || !stringsContain(*st.RecentRuns[1].Error, "合法 JSON") {
		t.Fatalf("失败 run 要带原因，实际 %v", st.RecentRuns[1].Error)
	}
	if st.RecentRuns[1].Status != "error" || st.RecentRuns[0].Status != "ok" {
		t.Fatalf("status 不符：%q / %q", st.RecentRuns[1].Status, st.RecentRuns[0].Status)
	}
	if st.RecentRuns[2].SampleCount != 5 || st.RecentRuns[2].Inserted != 4 || st.RecentRuns[2].Duplicates != 1 {
		t.Fatalf("统计没回填：%+v", st.RecentRuns[2])
	}
	if st.RecentRuns[0].ReceivedAt != "2026-10-13T00:00:00Z" {
		t.Fatalf("received_at 必须是 UTC RFC3339，实际 %q", st.RecentRuns[0].ReceivedAt)
	}
	// +0800 的 16:30 存进去就是 08:30Z：水位出参必须已经换成 UTC 表述
	if len(st.Watermarks) != 2 {
		t.Fatalf("期望 2 条水位，实际 %d", len(st.Watermarks))
	}
	if st.Watermarks[0].Metric != "active_energy" || st.Watermarks[1].Metric != "step_count" {
		t.Fatalf("水位要按指标升序：%v", st.Watermarks)
	}
	if st.Watermarks[1].LastStartTime != "2026-10-12T08:30:00Z" {
		t.Fatalf("水位时刻没按 UTC 出参，实际 %q", st.Watermarks[1].LastStartTime)
	}
	if st.Watermarks[1].LastLocalDate != d1 {
		t.Fatalf("水位本地日期不符：期望 %s 实际 %q", d1, st.Watermarks[1].LastLocalDate)
	}

	// 缺口：今天补上 step_count 后只剩昨天
	seedDailyDay(t, db, d0, "step_count", 8000, 3, 1, []string{"iPhone"}, "")
	// 昨天只有心率：缺口按 step_count 判，所以昨天仍然缺
	seedDailyDay(t, db, d1, "heart_rate", 70, 3, 1, []string{"Apple Watch"}, "")
	st, err = db.SyncStatusOf(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.MissingDays) != 1 || st.MissingDays[0] != d1 {
		t.Fatalf("缺口应只剩昨天（只按 step_count 判），实际 %v", st.MissingDays)
	}

	seedDailyDay(t, db, d1, "step_count", 9000, 3, 1, []string{"iPhone"}, "")
	st, err = db.SyncStatusOf(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.MissingDays) != 0 {
		t.Fatalf("补齐后不该有缺口，实际 %v", st.MissingDays)
	}
	b, _ := json.Marshal(st.MissingDays)
	if string(b) != "[]" {
		t.Fatalf("补齐后 missing_days 要编码成 []，实际 %s", b)
	}

	// run 明细超过 10 次时只留最近 10 次（LIMIT 10），且留的是最新的而不是最早的
	for i := 0; i < 12; i++ {
		id, _, err := db.BeginRun(ctx, RunMeta{Transport: "webhook", SessionID: fmt.Sprintf("sess-more-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `UPDATE ingest_runs SET received_at = $1 WHERE id = $2`,
			time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC), id)
	}
	st, err = db.SyncStatusOf(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RecentRuns) != 10 {
		t.Fatalf("recent_runs 上限应是 10，实际 %d", len(st.RecentRuns))
	}
	// 三条 10 月的 run 比 1 月那批新，仍然排在最前
	if st.RecentRuns[0].ID != newest || st.RecentRuns[1].ID != failed || st.RecentRuns[2].ID != older {
		t.Fatalf("截断必须是「保留最近」，实际前三条 %+v", st.RecentRuns[:3])
	}
	if st.RecentRuns[3].SessionID != "sess-more-11" {
		t.Fatalf("第 4 条应是 1 月里最新的那次，实际 %q", st.RecentRuns[3].SessionID)
	}
	if st.RecentRuns[9].SessionID != "sess-more-5" {
		t.Fatalf("第 10 条应正好截到 sess-more-5，实际 %q", st.RecentRuns[9].SessionID)
	}
	for _, r := range st.RecentRuns {
		if r.SessionID == "sess-more-4" {
			t.Fatal("超出 10 条的旧 run 不该出现")
		}
	}
	// last_run 始终跟最近一次对齐
	if st.LastRun == nil || st.LastRun.ID != newest {
		t.Fatalf("last_run 应与 recent_runs[0] 一致：%+v", st.LastRun)
	}
}

func stringsContain(s, sub string) bool { return strings.Contains(s, sub) }

// TestSleepStagesWithoutSession 锁定「只有分期、没有会话」那一路的口径：
// 窗口退化成「睡眠日 18:00 → 次日 18:00」，来源靠覆盖率择优（会话来源不存在）。
func TestSleepStagesWithoutSession(t *testing.T) {
	db := openSleepDB(t)
	ctx := context.Background()

	seg := func(src string, mo, d, h, mi, durMin int, stage, localDate string) sample.Sample {
		st := at0800(mo, d, h, mi)
		return sample.Sample{
			Metric: "sleep_analysis", Source: src, StartTime: st, EndTime: st.Add(time.Duration(durMin) * time.Minute),
			Value: float64(durMin) / 60, Unit: "hr", LocalDate: localDate, TZOffsetMin: 480,
			Meta: map[string]any{"stage": stage},
		}
	}
	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		seg("Apple Watch", 10, 12, 22, 0, 30, "InBed", "2026-10-12"),     // 1320，窗口内
		seg("Apple Watch", 10, 13, 2, 0, 60, "AsleepDeep", "2026-10-13"), // 1560，窗口内
		seg("Apple Watch", 10, 13, 20, 0, 30, "AsleepREM", "2026-10-13"), // 2640，超出 2520：排除
		seg("iPhone", 10, 13, 1, 0, 5, "Awake", "2026-10-13"),            // 覆盖率低：被择优排除
		seg("iPhone", 10, 13, 1, 10, 5, "Awake", "2026-10-13"),
		seg("Apple Watch", 10, 12, 17, 0, 30, "AsleepCore", "2026-10-12"), // 1020 < 1080：窗口前，排除
	}, "strict"); err != nil {
		t.Fatalf("写入分期失败: %v", err)
	}
	// 心率：Watch 一个点、iPhone 两个点 → 没有会话来源时按样本数择优到 iPhone
	hrCase := func(src string, mo, d, h, mi int, v float64) sample.Sample {
		st := at0800(mo, d, h, mi)
		return sample.Sample{Metric: "heart_rate", Source: src, StartTime: st, EndTime: st, Value: v,
			Unit: "count/min", LocalDate: dateOf(mo, d), TZOffsetMin: 480}
	}
	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		hrCase("Apple Watch", 10, 13, 2, 30, 50),
		hrCase("iPhone", 10, 13, 2, 40, 80),
		hrCase("iPhone", 10, 13, 2, 50, 90),
	}, "strict"); err != nil {
		t.Fatalf("写入心率失败: %v", err)
	}

	got, err := db.SleepStages(ctx, "2026-10-12")
	if err != nil {
		t.Fatalf("SleepStages 失败: %v", err)
	}
	if got.HasSession {
		t.Fatal("没有会话时不该报 has_session")
	}
	if got.SleepStart != nil || got.SleepEnd != nil {
		t.Fatalf("没有会话时入睡/起床时刻应为 nil：%v / %v", got.SleepStart, got.SleepEnd)
	}
	if got.Source != "Apple Watch" {
		t.Fatalf("分期应按覆盖率择优，实际 %q", got.Source)
	}
	if len(got.Segments) != 2 {
		t.Fatalf("期望 2 段（窗口外与低覆盖来源都排除），实际 %d：%v", len(got.Segments), got.Segments)
	}
	if got.Segments[0].Stage != "inbed" || got.Segments[0].LocalMin != 1320 {
		t.Fatalf("第一段应是 inbed@1320，实际 %s@%d", got.Segments[0].Stage, got.Segments[0].LocalMin)
	}
	if got.TZOffsetMin != 0 {
		t.Fatalf("没有会话就没有偏移，应为 0，实际 %d", got.TZOffsetMin)
	}
	if got.Totals["deep"] < 0.99 || got.Totals["deep"] > 1.01 {
		t.Fatalf("deep 合计应为 1h，实际 %v", got.Totals["deep"])
	}
	if len(got.Physiology) != 1 {
		t.Fatalf("期望 1 条夜间序列，实际 %d", len(got.Physiology))
	}
	p := got.Physiology[0]
	if p.Source != "iPhone" || p.SampleCount != 2 {
		t.Fatalf("生理序列在无会话来源时也应按样本数择优，实际 %s / %d", p.Source, p.SampleCount)
	}
	if p.Min != 80 || p.Max != 90 || p.Avg != 85 {
		t.Fatalf("择优后统计应只含该来源的点：%v / %v / %v", p.Min, p.Max, p.Avg)
	}
	if p.Unit != "count/min" {
		t.Fatalf("单位应从点里带出来，实际 %q", p.Unit)
	}
}

func dateOf(mo, d int) string {
	return fmt.Sprintf("2026-%02d-%02d", mo, d)
}

// TestSleepStagesWindowTail 锁定「窗口尾巴裁切」：压在起床时刻上的分段
// 要么被裁到刚好剩 0（整段丢弃），要么被裁短，绝不能画出窗外的时长。
func TestSleepStagesWindowTail(t *testing.T) {
	db := openSleepDB(t)
	ctx := context.Background()

	start, end := at0800(10, 12, 23, 30), at0800(10, 13, 7, 0) // 窗口 1410 ~ 1860
	if _, err := db.WriteSleep(ctx, 1, []sample.SleepSession{
		{LocalDate: "2026-10-12", Source: "Apple Watch", TZOffsetMin: 480, SleepStart: &start, SleepEnd: &end},
	}); err != nil {
		t.Fatal(err)
	}
	seg := func(h, mi, durMin int, stage string) sample.Sample {
		st := at0800(10, 13, h, mi)
		return sample.Sample{Metric: "sleep_analysis", Source: "Apple Watch", StartTime: st,
			EndTime: st.Add(time.Duration(durMin) * time.Minute), Value: float64(durMin) / 60, Unit: "hr",
			LocalDate: "2026-10-13", TZOffsetMin: 480, Meta: map[string]any{"stage": stage}}
	}
	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		seg(6, 50, 40, "Awake"),     // 起点 1850，落在窗口内但尾巴超出 10 分钟 → 裁到 10
		seg(7, 0, 20, "AsleepCore"), // 起点正好 1860 == winEnd → 裁完为 0，整段丢弃
	}, "strict"); err != nil {
		t.Fatal(err)
	}

	got, err := db.SleepStages(ctx, "2026-10-12")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Segments) != 1 {
		t.Fatalf("压在起床时刻的分段应被整段丢掉，实际 %d 段：%v", len(got.Segments), got.Segments)
	}
	s := got.Segments[0]
	if s.Stage != "awake" || s.LocalMin != 1850 {
		t.Fatalf("保留的应是 awake@1850，实际 %s@%d", s.Stage, s.LocalMin)
	}
	if s.DurationMin != 10 {
		t.Fatalf("超出窗口的部分要裁掉：期望 10 分钟，实际 %v", s.DurationMin)
	}
	if got.Totals["awake"] < 0.16 || got.Totals["awake"] > 0.17 {
		t.Fatalf("裁短后的合计应是 10min(0.166h)，实际 %v", got.Totals["awake"])
	}
	// JSON 契约：没有的键不该出现
	b, _ := json.Marshal(got)
	if stringsContain(string(b), `"asleep"`) {
		t.Fatalf("被丢弃的分段不该进入合计：%s", b)
	}
}

// TestSleepStagesRangePicksLongestSessionAcrossSources 锁定矩阵在多来源时的口径：
//   - 会话取时长最长的那条，来源也以它为准；
//   - 该来源在分段里没有数据时退回覆盖率最高的来源（不能因为来源对不上就整晚空白）；
//   - 只有会话、没有分段的夜晚不出行；
//   - 分段只落在 to+1 天时不算进区间（它是次日的候选，不是新的一晚）。
func TestSleepStagesRangePicksLongestSessionAcrossSources(t *testing.T) {
	db := openSleepDB(t)
	ctx := context.Background()

	if _, err := db.WriteSleep(ctx, 1, []sample.SleepSession{
		// 同一晚两条来源：Watch 8.5h 胜出
		{LocalDate: "2026-10-12", Source: "iPhone", TZOffsetMin: 480,
			SleepStart: ptr(at0800(10, 12, 23, 50)), SleepEnd: ptr(at0800(10, 13, 6, 20)), TotalSleepHr: fptr(6.5)},
		{LocalDate: "2026-10-12", Source: "Apple Watch", TZOffsetMin: 480,
			SleepStart: ptr(at0800(10, 12, 23, 30)), SleepEnd: ptr(at0800(10, 13, 7, 0)), TotalSleepHr: fptr(7.5)},
		// 只有会话、没有分段的夜晚：不出行
		{LocalDate: "2026-10-08", Source: "Apple Watch", TZOffsetMin: 480,
			SleepStart: ptr(at0800(10, 8, 23, 0)), SleepEnd: ptr(at0800(10, 9, 6, 0)), TotalSleepHr: fptr(7.0)},
		// 会话没有入睡时刻（只有上床时间）：has_session=false，但分段照样出
		{LocalDate: "2026-10-10", Source: "Apple Watch", TZOffsetMin: 480,
			InBedStart: ptr(at0800(10, 10, 22, 30)), InBedEnd: ptr(at0800(10, 11, 7, 0)), InBedHr: fptr(8.5)},
	}); err != nil {
		t.Fatal(err)
	}

	seg := func(src string, mo, d, h, mi, durMin int, stage string) sample.Sample {
		st := at0800(mo, d, h, mi)
		return sample.Sample{Metric: "sleep_analysis", Source: src, StartTime: st,
			EndTime: st.Add(time.Duration(durMin) * time.Minute), Value: float64(durMin) / 60, Unit: "hr",
			LocalDate: dateOf(mo, d), TZOffsetMin: 480, Meta: map[string]any{"stage": stage}}
	}
	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		// 10-12：只有 iPhone 有分段（会话来源 Watch 落空 → 退回覆盖率择优）
		seg("iPhone", 10, 12, 23, 40, 40, "AsleepCore"),
		seg("iPhone", 10, 13, 3, 0, 60, "AsleepDeep"),
		seg("Whoop", 10, 13, 3, 0, 5, "Awake"),
		// 10-10：Watch 的分段
		seg("Apple Watch", 10, 10, 23, 0, 60, "AsleepREM"),
		// 只落在 to+1（10-13 的样本属于 10-12 那晚，这里造一条 10-14 的当日分段）
		seg("Apple Watch", 10, 14, 23, 0, 60, "AsleepCore"),
		// 完全没有会话的一晚：睡眠日只能从分段反推出来（22:00 落在粗窗口 18:00~次日18:00 内）
		seg("Apple Watch", 10, 9, 22, 0, 60, "AsleepDeep"),
		// 10-07：分段全在窗口外（白天 10:00），这一晚该整行不出现
		seg("Apple Watch", 10, 7, 10, 0, 60, "AsleepCore"),
	}, "strict"); err != nil {
		t.Fatal(err)
	}

	rows, err := db.SleepStagesRange(ctx, "2026-10-07", "2026-10-12")
	if err != nil {
		t.Fatal(err)
	}
	byDate := map[string]NightStages{}
	for _, r := range rows {
		byDate[r.LocalDate] = r
	}
	if len(rows) != 3 {
		t.Fatalf("期望 3 晚（10-08 无分段、10-07 分段全在窗口外、10-13/14 在区间外），实际 %d：%v", len(rows), datesOf(rows))
	}
	if _, ok := byDate["2026-10-08"]; ok {
		t.Fatal("只有会话没有分段的夜晚不该出行")
	}
	if _, ok := byDate["2026-10-07"]; ok {
		t.Fatal("分段全部落在窗口外的夜晚不该出行")
	}
	n12 := byDate["2026-10-12"]
	if n12.Source != "iPhone" {
		t.Fatalf("会话来源没有分段时应退回覆盖率最高的来源，实际 %q", n12.Source)
	}
	if len(n12.Segments) != 2 {
		t.Fatalf("低覆盖来源(Whoop)该被排除，期望 2 段，实际 %d", len(n12.Segments))
	}
	if !n12.HasSession || n12.SleepStart == nil || *n12.SleepStart != at0800(10, 12, 23, 30).UTC().Format(time.RFC3339) {
		t.Fatalf("会话应取时长最长的 Watch 那条入睡时刻，实际 %+v", n12)
	}
	if n12.TZOffsetMin != 480 {
		t.Fatalf("偏移应从会话带出，实际 %d", n12.TZOffsetMin)
	}
	n10 := byDate["2026-10-10"]
	if n10.HasSession {
		t.Fatal("只有上床时间、没有入睡时刻的会话不该报 has_session")
	}
	if n10.SleepStart != nil || n10.SleepEnd != nil {
		t.Fatalf("没有入睡时刻时两个字段都应是 nil：%v / %v", n10.SleepStart, n10.SleepEnd)
	}
	if len(n10.Segments) != 1 || n10.Segments[0].Stage != "rem" {
		t.Fatalf("10-10 应有 1 段 rem，实际 %v", n10.Segments)
	}
	// 只有分段、完全没有会话的一晚：睡眠日由分段的 local_date 反推，窗口退到粗锚点
	n9 := byDate["2026-10-09"]
	if n9.HasSession || n9.SleepStart != nil {
		t.Fatalf("无会话的夜晚不该报会话信息：%+v", n9)
	}
	if n9.Source != "Apple Watch" || len(n9.Segments) != 1 || n9.Segments[0].LocalMin != 1320 {
		t.Fatalf("无会话夜晚的分段应按粗窗口落位，实际 %+v", n9.Segments)
	}
}

func datesOf(rows []NightStages) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.LocalDate)
	}
	return out
}

// TestSleepRangeEmptyAndSleepStagesEmptyRange 补齐「区间内什么都没有」的返回形态。
func TestSleepRangeEmptyAndSleepStagesEmptyRange(t *testing.T) {
	db := openSleepDB(t)
	ctx := context.Background()

	rows, err := db.SleepRange(ctx, "2026-01-01", "2026-01-02")
	if err != nil {
		t.Fatal(err)
	}
	if rows == nil || len(rows) != 0 {
		t.Fatalf("无睡眠记录应是非 nil 空数组，实际 %#v", rows)
	}
	if _, err := db.SleepStages(ctx, "2026-01-01"); err != nil {
		t.Fatalf("完全没有数据的一晚不该报错: %v", err)
	}
	got, err := db.SleepStages(ctx, "2026-01-01")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	want := `{"local_date":"2026-01-01","source":"","tz_offset_min":0,"has_session":false,"segments":[],"totals":{},"physiology":[]}`
	if string(b) != want {
		t.Fatalf("空夜晚的 JSON 契约不符：\n实际 %s\n期望 %s", b, want)
	}
}
