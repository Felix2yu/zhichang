package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// 睡眠是唯一「不产生 samples 却有天级值」的指标：会话只进 sleep_sessions。
// 聚合如果不单独从会话表取，daily_metrics_day 里就永远没有 sleep_analysis 行——
// 于是趋势页的「睡眠时长」卡片显示暂无数据，而睡眠页明明列着几百晚，
// 概览的基线/偏离、相关性、MCP 的 health_query 也都取不到睡眠。
func TestRollupSleepFromSessions(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()
	r := NewRollup(db, metric.New(metric.Defaults()))

	// 2026-08-27 这一晚：手表报 7.60、第三方 App 报 4.50 → 跨来源取最大，不是求和
	// 2026-08-26 这一晚：同来源两段（夜觉 5.80 + 午睡 1.20）→ 同来源内求和
	// 2026-08-25：total_sleep_hr 为空 → 不该产出行
	sess := func(day int, source string, hour, min int, total float64) sample.SleepSession {
		st := time.Date(2026, 8, day, hour, min, 0, 0, time.FixedZone("+08", 8*3600)).UTC()
		en := st.Add(8 * time.Hour)
		v := total
		return sample.SleepSession{LocalDate: fmt.Sprintf("2026-08-%02d", day), Source: source,
			SleepStart: &st, SleepEnd: &en, TotalSleepHr: &v, TZOffsetMin: 480}
	}
	if _, err := db.WriteSleep(ctx, 0, []sample.SleepSession{
		sess(27, "Apple Watch", 23, 10, 7.60),
		sess(27, "SleepLog", 23, 40, 4.50),
		sess(26, "Apple Watch", 22, 50, 5.80),
		sess(26, "Apple Watch", 13, 0, 1.20),
	}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO sleep_sessions (local_date, source, total_sleep_hr, tz_offset_min)
		VALUES ('2026-08-25','Apple Watch',NULL,480)`)

	if err := r.Recompute(ctx, []string{"2026-08-25", "2026-08-26", "2026-08-27"}, []string{SleepMetric}); err != nil {
		t.Fatalf("睡眠天级重算失败: %v", err)
	}

	day := func(date string) (float64, int) {
		t.Helper()
		var v float64
		var srcs int
		if err := db.Pool.QueryRow(ctx,
			`SELECT value, source_count FROM daily_metrics_day WHERE local_date=$1 AND metric=$2`,
			date, SleepMetric).Scan(&v, &srcs); err != nil {
			t.Fatalf("%s 没有睡眠天级行: %v", date, err)
		}
		return v, srcs
	}
	if v, srcs := day("2026-08-27"); v != 7.60 || srcs != 2 {
		t.Fatalf("跨来源应取最大 7.60（两个来源），实际 %v / %d 来源", v, srcs)
	}
	if v, srcs := day("2026-08-26"); v != 7.00 || srcs != 1 {
		t.Fatalf("同来源分段应求和 7.00（5.80+1.20），实际 %v / %d 来源", v, srcs)
	}
	var n int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM daily_metrics_day WHERE local_date='2026-08-25' AND metric=$1`,
		SleepMetric).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("没有总睡眠时长的夜晚不该产生天级行，实际 %d 行", n)
	}
	// 中间层：每 (日期 × 来源) 一条
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM daily_metrics WHERE metric=$1 AND source='Apple Watch'`,
		SleepMetric).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("Apple Watch 应有 2 个夜晚的中间行，实际 %d", n)
	}
}

// 睡眠与 samples 两条路径同时重算时，两边的行都必须保住：
// 谁把对方刚写的中间行删掉，都是不报错的静默丢数。
func TestRollupSleepCoexistsWithSampleMetrics(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()
	r := NewRollup(db, metric.New(metric.Defaults()))

	st := time.Date(2026, 10, 12, 15, 30, 0, 0, time.UTC)
	en := st.Add(7 * time.Hour)
	total := 7.5
	if _, err := db.WriteSleep(ctx, 0, []sample.SleepSession{
		{LocalDate: "2026-10-12", Source: "Apple Watch", SleepStart: &st, SleepEnd: &en,
			TotalSleepHr: &total, TZOffsetMin: 480},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := db.WriteSamples(ctx, 0, []sample.Sample{
		{Metric: "step_count", Source: "Apple Watch", Value: 8000, Unit: "count",
			StartTime: st, EndTime: st.Add(time.Hour), LocalDate: "2026-10-12", TZOffsetMin: 480},
	}, "strict"); err != nil {
		t.Fatal(err)
	}
	if err := r.Recompute(ctx, []string{"2026-10-12"}, []string{SleepMetric, "step_count"}); err != nil {
		t.Fatal(err)
	}
	for m, want := range map[string]float64{SleepMetric: 7.5, "step_count": 8000} {
		var v float64
		if err := db.Pool.QueryRow(ctx,
			`SELECT value FROM daily_metrics_day WHERE local_date='2026-10-12' AND metric=$1`,
			m).Scan(&v); err != nil {
			t.Fatalf("%s 的天级行没了（被另一条路径覆盖）: %v", m, err)
		}
		if v != want {
			t.Fatalf("%s 天级值应为 %v，实际 %v", m, want, v)
		}
	}
}
