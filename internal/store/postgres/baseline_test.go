package postgres

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// 本文件锁 baseline.go / insights.go 的口径。概览页的全部可信度都压在这三条上：
//   1. 常态是「不含对比日的过去窗口的中位数」——今天的值不能参与自己的基准；
//   2. 今天没数据时回落到最后有数据的那天，并且标注出来（stale）；
//   3. 样本不足就不给基准——宁可空着也不给假精确的数字。
//
// 这些口径一旦走偏，界面上看不出错：卡片照常显示数字，只是「比常态高 12%」这句话是假的。

// seedSeries 按天灌注一段天级值：从 startDate 起，每天一个值。
func seedSeries(t *testing.T, db *DB, metric, startDate string, values []float64) []string {
	t.Helper()
	dates := make([]string, 0, len(values))
	cur := startDate
	for _, v := range values {
		seedDailyDay(t, db, cur, metric, v, 1, 1, []string{"Apple Watch"}, "")
		dates = append(dates, cur)
		cur = shiftDayStr(cur, 1)
	}
	return dates
}

func getBaseline(t *testing.T, items []BaselineItem, metric string) BaselineItem {
	t.Helper()
	for _, it := range items {
		if it.Metric == metric {
			return it
		}
	}
	t.Fatalf("结果里没有 %s：%+v", metric, items)
	return BaselineItem{}
}

// TestBaselineUsesTrailingMedianExcludingComparedDay 锁定常态的核心口径：
// 窗口不含对比日本身——今天的值不能参与自己的基准。
func TestBaselineUsesTrailingMedianExcludingComparedDay(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	// 09-25 起连续 10 天：100..1000，中位数应是前 9 天的 500
	dates := seedSeries(t, db, "step_count", "2026-09-25", []float64{100, 200, 300, 400, 500, 600, 700, 800, 900, 1000})
	today := dates[len(dates)-1]

	got := getBaseline(t, mustBaseline(t, ctx, db, []string{"step_count"}, today, 30), "step_count")
	if got.Date != today || got.Stale {
		t.Fatalf("最后一天有数据时就该用它对比：date=%s stale=%v", got.Date, got.Stale)
	}
	if got.SampleDays != 9 {
		t.Fatalf("对比日必须先从窗口里剔除，应有 9 天样本，实际 %d", got.SampleDays)
	}
	if got.Baseline == nil || math.Abs(*got.Baseline-500) > 1e-9 {
		t.Fatalf("常态应为前 9 天的中位数 500，实际 %v", got.Baseline)
	}
	if got.DeviationPct == nil || math.Abs(*got.DeviationPct-100) > 1e-6 {
		t.Fatalf("偏离应为 +100%%，实际 %v", got.DeviationPct)
	}
	if got.PrevValue == nil || *got.PrevValue != 900 {
		t.Fatalf("前一日应为 900，实际 %v", got.PrevValue)
	}
	// 迷你走势：截至对比日的最后 7 个点，日期升序
	if len(got.Recent) != 7 {
		t.Fatalf("迷你走势应带回 7 个点，实际 %d", len(got.Recent))
	}
	if got.Recent[0].Date != "2026-09-28" || got.Recent[6].Date != today {
		t.Fatalf("迷你走势区间不对：%v", got.Recent)
	}
}

// TestBaselineFallsBackToLastDataDay 锁定「今天还没同步」的处理：
// 回落到最后有数据的那天，stale 置真，且**常态窗口跟着回落日走**——
// 否则拿「三天前的当天」去比「截至今天这 30 天」，窗口里混进了它之后的日期，口径就歪了。
func TestBaselineFallsBackToLastDataDay(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	// body_mass 到 10-01 为止，之后同步断了；查询日 10-04
	seedSeries(t, db, "body_mass", "2026-09-26", []float64{70, 70.5, 71, 70.8, 70.2, 69.9})

	got := getBaseline(t, mustBaseline(t, ctx, db, []string{"body_mass"}, "2026-10-04", 30), "body_mass")
	if got.Date != "2026-10-01" {
		t.Fatalf("应回落到最后有数据的 2026-10-01，实际 %s", got.Date)
	}
	if !got.Stale {
		t.Fatal("回落时必须把 stale 置真，否则前端会把三天前的数据当今天的显示")
	}
	if got.SampleDays != 5 {
		t.Fatalf("窗口应相对回落日（10-01）回溯，得 5 天样本，实际 %d", got.SampleDays)
	}
	// 窗口里若混入了对比日之后的日期，中位数就会变：这里验证取的是 09-26..09-30 五个数
	if got.Baseline == nil || math.Abs(*got.Baseline-70.5) > 1e-9 {
		t.Fatalf("常态应为剔除回落日后前 5 天的中位数 70.5，实际 %v", got.Baseline)
	}
}

// TestBaselineRefusesThinSample 锁定「样本不足就不给基准」：
// 两天算出来的中位数是假精确，宁可让卡片显示「常态数据不足」。
func TestBaselineRefusesThinSample(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	seedSeries(t, db, "caffeine", "2026-10-01", []float64{1, 2, 3})

	got := getBaseline(t, mustBaseline(t, ctx, db, []string{"caffeine"}, "2026-10-03", 30), "caffeine")
	if got.SampleDays != 2 {
		t.Fatalf("窗口应有 2 天样本，实际 %d", got.SampleDays)
	}
	if got.Baseline != nil || got.DeviationPct != nil {
		t.Fatalf("样本不足时不应给出常态，实际 baseline=%v pct=%v", got.Baseline, got.DeviationPct)
	}
	if got.Value == nil || *got.Value != 3 {
		t.Fatalf("数值本身仍应给出，实际 %v", got.Value)
	}
}

// TestBaselineEmptyRowsStayJSONSafe 锁定 JSON 契约：空结果与无数据指标都不能出现 null 数组。
func TestBaselineEmptyRowsStayJSONSafe(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	empty, err := db.Baseline(ctx, nil, "2026-10-04", 30)
	if err != nil {
		t.Fatalf("空指标列表不该报错: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("空指标列表应为非 nil 空数组，实际 %#v", empty)
	}

	items := mustBaseline(t, ctx, db, []string{"never_measured"}, "2026-10-04", 30)
	got := getBaseline(t, items, "never_measured")
	if got.Value != nil || got.Baseline != nil || got.Recent == nil {
		t.Fatalf("无数据指标应为空值 + 非 nil 空走势：%+v", got)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	if !strings.Contains(string(b), `"recent":[]`) {
		t.Fatalf("recent 必须编码为 []，实际 %s", b)
	}
}

// TestInsightsFlagsStreakAndOutlier 锁定两条触发线各自的触发条件。
func TestInsightsFlagsStreakAndOutlier(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	// step_count：前 8 天在 8000 附近小幅抖动，最后 3 天抬到 9000 → 连续偏离
	steps := []float64{8000, 8050, 7980, 8020, 8010, 7990, 8030, 8000, 9000, 9000, 9000}
	seedSeries(t, db, "step_count", "2026-09-24", steps)
	// resting_heart_rate：长期稳定在 60，只在最后一天跳到 75 → 单点离群
	rhr := []float64{59, 60, 61, 60, 59, 60, 61, 60, 59, 60, 75}
	seedSeries(t, db, "resting_heart_rate", "2026-09-24", rhr)

	items, err := db.Insights(ctx, []string{"step_count", "resting_heart_rate"}, "2026-10-04", 30, 6)
	if err != nil {
		t.Fatalf("Insights 失败: %v", err)
	}
	byMetric := map[string]InsightItem{}
	for _, it := range items {
		byMetric[it.Metric] = it
	}

	streak, ok := byMetric["step_count"]
	if !ok {
		t.Fatalf("连续抬高的步数应被上报，实际 %+v", items)
	}
	if streak.Kind != "streak" || streak.StreakDays != 3 {
		t.Fatalf("应为连续 3 天偏离，实际 kind=%s days=%d", streak.Kind, streak.StreakDays)
	}
	if streak.Direction != "high" {
		t.Fatalf("方向应为 high，实际 %s", streak.Direction)
	}

	outlier, ok := byMetric["resting_heart_rate"]
	if !ok {
		t.Fatalf("最后一天跳到 75 的静息心率应判离群，实际 %+v", items)
	}
	if outlier.Kind != "outlier" || outlier.Direction != "high" {
		t.Fatalf("应为单点离群且偏高，实际 %+v", outlier)
	}
	if outlier.Date != "2026-10-04" {
		t.Fatalf("日期应为最后一天，实际 %s", outlier.Date)
	}
	// 幅度大的排前面
	if len(items) != 2 || items[0].Metric != "resting_heart_rate" {
		t.Fatalf("应按偏离幅度降序，实际顺序 %+v", items)
	}
}

// TestInsightsIgnoresNoiseAndFlatSeries 锁定「不打扰」：小幅波动与取值恒定的序列不上报。
func TestInsightsIgnoresNoiseAndFlatSeries(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	// noise：最后一天只比常态高 2%，属于日常噪声
	seedSeries(t, db, "noise_metric", "2026-09-24", []float64{
		100, 101, 99, 100, 100, 102, 98, 100, 101, 99, 102,
	})
	// flat：长期恒定 70，最后一天 74（+5.7% 达标），但四分位距为 0
	seedSeries(t, db, "flat_metric", "2026-09-24", []float64{
		70, 70, 70, 70, 70, 70, 70, 70, 70, 70, 74,
	})

	items, err := db.Insights(ctx, []string{"noise_metric", "flat_metric"}, "2026-10-04", 30, 6)
	if err != nil {
		t.Fatalf("Insights 失败: %v", err)
	}
	for _, it := range items {
		if it.Metric == "noise_metric" {
			t.Fatalf("2%% 的波动不该上报：%+v", it)
		}
		if it.Metric == "flat_metric" {
			t.Fatalf("四分位距为 0 时任何偏离都会越过栅栏，必须跳过：%+v", it)
		}
	}
	if len(items) != 0 {
		t.Fatalf("应完全没有提示，实际 %+v", items)
	}

	// 指标列表为空：不查库也要回非 nil 空数组
	empty, err := db.Insights(ctx, nil, "2026-10-04", 30, 6)
	if err != nil {
		t.Fatalf("空指标列表不该报错: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("应为非 nil 空数组，实际 %#v", empty)
	}
}

// TestInsightsLimitIsRespected 锁定条数上限：概览页放不下太多，也不该堆噪声。
func TestInsightsLimitIsRespected(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	for i, m := range []string{"m_a", "m_b", "m_c"} {
		base := 100.0 + float64(i)
		// 常态期内要有抖动，否则四分位距为 0 会被「平坦序列」规则挡掉，测不到 limit
		seedSeries(t, db, m, "2026-09-24", []float64{
			base * 0.99, base * 1.01, base * 0.98, base * 1.02, base * 0.99,
			base * 1.01, base * 0.98, base * 1.02, base * 0.99, base * 1.01, base * 1.5,
		})
	}
	items, err := db.Insights(ctx, []string{"m_a", "m_b", "m_c"}, "2026-10-04", 30, 2)
	if err != nil {
		t.Fatalf("Insights 失败: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("limit=2 应只回 2 条，实际 %d", len(items))
	}
}

// TestCoverageOfWindow 锁定覆盖度的窗口闭合行数与时差计算：
// 它回答的是「这张卡为什么是空的」，天数差算错会让 UI 显示「明天更新」这种荒唐话。
func TestCoverageOfWindow(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	seedDailyDay(t, db, "2026-10-01", "step_count", 8000, 1, 1, []string{"iPhone"}, "")
	seedDailyDay(t, db, "2026-10-02", "step_count", 9000, 1, 1, []string{"iPhone"}, "")
	seedDailyDay(t, db, "2026-10-04", "step_count", 7000, 1, 1, []string{"iPhone"}, "")
	seedDailyDay(t, db, "2026-10-04", "oxygen_saturation", 98, 1, 1, []string{"Apple Watch"}, "")
	// 窗口外的一天：必须被排除
	seedDailyDay(t, db, "2026-08-01", "step_count", 123, 1, 1, []string{"iPhone"}, "")

	cov, err := db.CoverageOf(ctx, "2026-10-04", 30)
	if err != nil {
		t.Fatalf("CoverageOf 失败: %v", err)
	}
	if cov.From != "2026-09-05" || cov.To != "2026-10-04" || cov.WindowDays != 30 {
		t.Fatalf("窗口边界不对：%+v", cov)
	}
	if len(cov.Rows) != 2 {
		t.Fatalf("窗口内应只有 2 个有数据的指标，实际 %+v", cov.Rows)
	}
	byMetric := map[string]CoverageItem{}
	for _, r := range cov.Rows {
		byMetric[r.Metric] = r
	}
	steps := byMetric["step_count"]
	if steps.Days != 3 {
		t.Fatalf("步数应有 3 天数据（窗口外那天不算），实际 %d", steps.Days)
	}
	if steps.LastDate != "2026-10-04" || steps.DaysSince != 0 {
		t.Fatalf("最后更新日/距今天数不对：%+v", steps)
	}
	o2 := byMetric["oxygen_saturation"]
	if o2.Days != 1 || o2.DaysSince != 0 {
		t.Fatalf("血氧覆盖不对：%+v", o2)
	}
	if cov.Rows[0].Metric != "oxygen_saturation" {
		t.Fatalf("应按指标名排序保证顺序稳定，实际 %+v", cov.Rows)
	}

	// 完全没有数据的窗口：rows 必须是 [] 而不是 null
	empty, err := db.CoverageOf(ctx, "2020-01-01", 30)
	if err != nil {
		t.Fatalf("空窗口不该报错: %v", err)
	}
	if empty.Rows == nil || len(empty.Rows) != 0 {
		t.Fatalf("空窗口应为非 nil 空数组，实际 %#v", empty.Rows)
	}
	b, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	if strings.Contains(string(b), "null") {
		t.Fatalf("空覆盖度不能含 null：%s", b)
	}
}

func mustBaseline(t *testing.T, ctx context.Context, db *DB, metrics []string, date string, window int) []BaselineItem {
	t.Helper()
	items, err := db.Baseline(ctx, metrics, date, window)
	if err != nil {
		t.Fatalf("Baseline 失败: %v", err)
	}
	return items
}
