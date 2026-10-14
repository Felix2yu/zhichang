package postgres

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// rollup_more_test.go 补 rollup.go 里那些「口径开关」：
// crossExprFor 决定跨来源怎么收敛（错一个符号就会把步数双计或把心率平均错），
// recomputeLast 决定身高/VO2max 这类「取当日最后一次」的指标取哪一条。

func init() {
	// Run 的后台 worker 会刻意打错误日志（测库不可用的分支），测试输出里不要刷屏。
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// probeRegistry 造一份只含探针指标的字典，用来精确控制 day_agg × cross_source_agg 的组合。
func probeRegistry(defs ...metric.Def) *metric.Registry {
	return metric.New(defs)
}

func probeDef(key, dayAgg, crossAgg string) metric.Def {
	return metric.Def{
		Metric: key, DisplayNameZH: "探针", Kind: metric.Kind("cumulative"),
		DayAgg: metric.DayAgg(dayAgg), CrossSourceAgg: metric.CrossAgg(crossAgg),
		Unit: "count", ValueField: "qty", Chart: "line", Enabled: true, SortOrder: 10,
	}
}

// TestCrossExprFor 锁死聚合表达式的选择表。这里的每一个字符串都对应一条业务口径：
// 累计量跨来源取 max 是为了消除 iPhone/Watch 双计，瞬时量按样本数加权是为了
// 「等价于全样本均值」而不是「各来源均值的均值」。
func TestCrossExprFor(t *testing.T) {
	const weighted = "sum(d.sum_value) / nullif(sum(d.sample_count), 0)"
	cases := []struct {
		name    string
		dayAgg  string
		cross   string
		metrics []string
		want    string
	}{
		{"累计量×求和", "sum", "sum", []string{"m"}, "sum(d.sum_value)"},
		{"累计量×均值", "sum", "mean", []string{"m"}, weighted},
		{"累计量×最大（消双计）", "sum", "max", []string{"m"}, "max(d.sum_value)"},
		{"累计量×未登记指标回落最大", "sum", "max", []string{"missing"}, "max(d.sum_value)"},
		{"累计量×空指标列表回落最大", "sum", "max", nil, "max(d.sum_value)"},
		{"累计量×latest 走默认分支", "sum", "latest", []string{"m"}, "max(d.sum_value)"},
		{"瞬时量×最大", "mean", "max", []string{"m"}, "max(d.mean_value)"},
		{"瞬时量×均值＝按样本数加权", "mean", "mean", []string{"m"}, weighted},
		{"瞬时量×求和也走加权", "mean", "sum", []string{"m"}, weighted},
		{"峰值×均值", "max", "mean", []string{"m"}, "avg(d.max_value)"},
		{"峰值×最大", "max", "max", []string{"m"}, "max(d.max_value)"},
		{"谷值×均值", "min", "mean", []string{"m"}, "avg(d.min_value)"},
		{"谷值×最小", "min", "min", []string{"m"}, "min(d.min_value)"},
		{"计数×求和", "count", "sum", []string{"m"}, "sum(d.sample_count)::float8"},
		{"计数×均值", "count", "mean", []string{"m"}, "avg(d.sample_count)::float8"},
		{"计数×最大", "count", "max", []string{"m"}, "max(d.sample_count)::float8"},
		{"未知日内口径按计数处理", "weird", "max", []string{"m"}, "max(d.sample_count)::float8"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := probeRegistry(probeDef("m", c.dayAgg, c.cross))
			// day_agg 由 metrics[0] 的声明决定，cross 也是：与 Recompute 的用法一致
			got := crossExprFor(metric.DayAgg(c.dayAgg), c.metrics, reg)
			if got != c.want {
				t.Fatalf("day=%s cross=%s → %q，期望 %q", c.dayAgg, c.cross, got, c.want)
			}
		})
	}
}

// TestToDateArray 锁定「坏日期静默跳过」：重算队列里混进脏日期时，
// 不能让整个批次的重算失败（宁可少算一天，也别把队列卡死）。
func TestToDateArray(t *testing.T) {
	got := toDateArray([]string{"2026-10-13", "bogus", "", "2026-10-14"})
	if len(got) != 2 {
		t.Fatalf("应只留下 2 个合法日期，实际 %d", len(got))
	}
	if got[0].Time.Format("2006-01-02") != "2026-10-13" || !got[0].Valid {
		t.Fatalf("第一个日期不对：%v", got[0])
	}
	if toDateArray(nil) == nil || len(toDateArray(nil)) != 0 {
		t.Fatal("空输入要给非 nil 空切片（ANY 的右值不能是 NULL）")
	}
}

// TestDateRange 锁定重算区间的展开：两端都含，非法顺序要报错而不是给一个空列表
// （空列表会被调用方当成「重算成功」）。
func TestDateRange(t *testing.T) {
	dates, err := dateRange("2026-10-11", "2026-10-13")
	if err != nil {
		t.Fatal(err)
	}
	if len(dates) != 3 || dates[0] != "2026-10-11" || dates[2] != "2026-10-13" {
		t.Fatalf("区间应两端都含：%v", dates)
	}
	same, err := dateRange("2026-10-13", "2026-10-13")
	if err != nil || len(same) != 1 {
		t.Fatalf("同一天应展开成 1 项，实际 %v / %v", same, err)
	}
	// 跨月、跨闰年
	feb, err := dateRange("2028-02-27", "2028-03-02")
	if err != nil || len(feb) != 5 || feb[4] != "2028-03-02" {
		t.Fatalf("跨月展开不对：%v / %v", feb, err)
	}
	for _, c := range [][2]string{{"bad", "2026-10-13"}, {"2026-10-13", "bad"}, {"2026-10-13", "2026-10-11"}} {
		if _, err := dateRange(c[0], c[1]); err == nil {
			t.Fatalf("dateRange(%q, %q) 应报错", c[0], c[1])
		}
	}
	if _, err := dateRange("2026-10-13", "2026-10-11"); err != nil && err.Error() != "结束日期早于起始日期" {
		t.Fatalf("倒序区间的报错口径不对: %v", err)
	}
}

// TestRecomputeLastMetric 锁定 day_agg=last 的指标（身高、VO2max）取哪一条：
// 当日最后一次测量，且 start_time 完全相同时按来源名升序，保证结果可重现。
func TestRecomputeLastMetric(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	const date = "2026-10-13"

	at := func(h, m int) time.Time { return time.Date(2026, 10, 13, h-8, m, 0, 0, time.UTC) }
	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		{Metric: "height", Source: "Apple Watch", StartTime: at(8, 0), EndTime: at(8, 0), Value: 170, Unit: "cm", LocalDate: date, TZOffsetMin: 480},
		{Metric: "height", Source: "iPhone", StartTime: at(9, 0), EndTime: at(9, 0), Value: 172, Unit: "cm", LocalDate: date, TZOffsetMin: 480},
		{Metric: "height", Source: "Apple Watch", StartTime: at(10, 0), EndTime: at(10, 0), Value: 171, Unit: "cm", LocalDate: date, TZOffsetMin: 480},
		// 同一时刻两个来源：按来源名升序，Apple Watch 胜出（口径必须可重现）
		{Metric: "height", Source: "iPhone", StartTime: at(10, 0), EndTime: at(10, 0), Value: 199, Unit: "cm", LocalDate: date, TZOffsetMin: 480},
		{Metric: "weight", Source: "iPhone", StartTime: at(7, 0), EndTime: at(7, 0), Value: 70, Unit: "kg", LocalDate: date, TZOffsetMin: 480},
		{Metric: "weight", Source: "iPhone", StartTime: at(11, 0), EndTime: at(11, 0), Value: 69, Unit: "kg", LocalDate: date, TZOffsetMin: 480},
	}, "strict"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	reg := probeRegistry(
		probeDef("height", "last", "latest"),
		probeDef("weight", "last", "latest"),
	)
	r := NewRollup(db, reg)
	if err := r.Recompute(ctx, []string{date}, []string{"height", "weight"}); err != nil {
		t.Fatalf("last 口径重算失败: %v", err)
	}

	var (
		height, weight float64
		hSamples, hSrc int
		sources        []string
	)
	err := db.Pool.QueryRow(ctx, `SELECT value, sample_count, source_count, sources FROM daily_metrics_day
		WHERE local_date = '2026-10-13' AND metric = 'height'`).Scan(&height, &hSamples, &hSrc, &sources)
	if err != nil {
		t.Fatalf("height 没算出天级值: %v", err)
	}
	if height != 171 {
		t.Fatalf("last 口径应取当日最后一次（171），实际 %v", height)
	}
	if hSamples != 4 || hSrc != 2 {
		t.Fatalf("last 口径也要如实统计样本数/来源数：%d / %d", hSamples, hSrc)
	}
	if len(sources) != 2 {
		t.Fatalf("来源清单应含两个来源：%v", sources)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT value FROM daily_metrics_day
		WHERE local_date = '2026-10-13' AND metric = 'weight'`).Scan(&weight); err != nil {
		t.Fatal(err)
	}
	if weight != 69 {
		t.Fatalf("weight 应取 11:00 那次（69），实际 %v", weight)
	}
}

// TestRecomputeUnregisteredMetricFallsBack 锁定「字典里没有的指标」的回落：
// 按累计量 + 跨来源取 max 处理。字典是后加的、样本可能先存在，回落方向必须是
// 「不要双计」，否则历史步数会凭空翻倍。
func TestRecomputeUnregisteredMetricFallsBack(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	const date = "2026-10-13"
	at := func(h int) time.Time { return time.Date(2026, 10, 13, h-8, 0, 0, 0, time.UTC) }

	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		{Metric: "mystery", Source: "iPhone", StartTime: at(8), EndTime: at(8), Value: 10, LocalDate: date, TZOffsetMin: 480},
		{Metric: "mystery", Source: "iPhone", StartTime: at(9), EndTime: at(9), Value: 5, LocalDate: date, TZOffsetMin: 480},
		{Metric: "mystery", Source: "Apple Watch", StartTime: at(8), EndTime: at(8), Value: 12, LocalDate: date, TZOffsetMin: 480},
	}, "strict"); err != nil {
		t.Fatal(err)
	}
	r := NewRollup(db, probeRegistry()) // 空字典：所有指标都走回落
	if err := r.Recompute(ctx, []string{date}, []string{"mystery"}); err != nil {
		t.Fatalf("未登记指标不该报错: %v", err)
	}
	var value float64
	var cnt, srcCnt int
	if err := db.Pool.QueryRow(ctx, `SELECT value, sample_count, source_count FROM daily_metrics_day
		WHERE local_date = '2026-10-13' AND metric = 'mystery'`).Scan(&value, &cnt, &srcCnt); err != nil {
		t.Fatal(err)
	}
	// 日内 sum：iPhone 15、Watch 12 → 跨来源 max 取 15
	if value != 15 || cnt != 3 || srcCnt != 2 {
		t.Fatalf("回落口径（sum + max）不符：%v / %d / %d", value, cnt, srcCnt)
	}
}

// TestRecomputeWeightedMeanAcrossSources 锁定瞬时量的加权均值边界：
// 单来源时加权均值就是它自己的均值；两来源样本数相等时等于两侧均值的算术平均。
func TestRecomputeWeightedMeanAcrossSources(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	const date = "2026-10-13"
	at := func(h, m int) time.Time { return time.Date(2026, 10, 13, h-8, m, 0, 0, time.UTC) }
	s := func(src string, h, m int, v float64) sample.Sample {
		return sample.Sample{Metric: "hr", Source: src, StartTime: at(h, m), EndTime: at(h, m),
			Value: v, Unit: "bpm", LocalDate: date, TZOffsetMin: 480}
	}

	reg := probeRegistry(probeDef("hr", "mean", "mean"))
	r := NewRollup(db, reg)

	// 单来源：均值 70（(60+80)/2），加权不会引入别的来源
	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		s("iPhone", 8, 0, 60), s("iPhone", 9, 0, 80),
	}, "strict"); err != nil {
		t.Fatal(err)
	}
	if err := r.Recompute(ctx, []string{date}, []string{"hr"}); err != nil {
		t.Fatal(err)
	}
	var value float64
	if err := db.Pool.QueryRow(ctx, `SELECT value FROM daily_metrics_day WHERE local_date = '2026-10-13' AND metric = 'hr'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 70 {
		t.Fatalf("单来源加权均值应等于自身均值 70，实际 %v", value)
	}

	// 两来源样本数相等：Watch 两条 (100,100)、iPhone 两条 (60,80) → 总均值 85
	if _, _, _, _, err := db.WriteSamples(ctx, 2, []sample.Sample{
		s("Apple Watch", 10, 0, 100), s("Apple Watch", 10, 5, 100),
	}, "strict"); err != nil {
		t.Fatal(err)
	}
	if err := r.Recompute(ctx, []string{date}, []string{"hr"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT value FROM daily_metrics_day WHERE local_date = '2026-10-13' AND metric = 'hr'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 85 {
		t.Fatalf("样本数相等时应为两侧均值的算术平均 85（等价于全样本均值），实际 %v", value)
	}
	var srcCnt int
	if err := db.Pool.QueryRow(ctx, `SELECT source_count FROM daily_metrics_day WHERE local_date = '2026-10-13' AND metric = 'hr'`).Scan(&srcCnt); err != nil {
		t.Fatal(err)
	}
	if srcCnt != 2 {
		t.Fatalf("来源计数不符：%d", srcCnt)
	}
}

// TestRecomputeNoSamplesAndEmptyArgs 锁定「没样本 / 没参数」的形态：
// 重算必须把旧的聚合行删掉（当天数据被清空时不能留下昨天的值），
// 而空参数直接返回，不打库。
func TestRecomputeNoSamplesAndEmptyArgs(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	const date = "2026-10-13"

	if err := NewRollup(db, probeRegistry()).Recompute(ctx, nil, []string{"hr"}); err != nil {
		t.Fatalf("空日期列表应直接返回: %v", err)
	}
	if err := NewRollup(db, probeRegistry()).Recompute(ctx, []string{date}, nil); err != nil {
		t.Fatalf("空指标列表应直接返回: %v", err)
	}

	// 先有样本算出一版，再清空样本重算：天级行必须消失
	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		{Metric: "hr", Source: "iPhone", StartTime: time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC),
			Value: 60, LocalDate: date, TZOffsetMin: 480},
	}, "strict"); err != nil {
		t.Fatal(err)
	}
	r := NewRollup(db, probeRegistry(probeDef("hr", "mean", "max")))
	if err := r.Recompute(ctx, []string{date}, []string{"hr"}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `TRUNCATE samples`)
	if err := r.Recompute(ctx, []string{date}, []string{"hr"}); err != nil {
		t.Fatal(err)
	}
	var cnt int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM daily_metrics_day WHERE local_date = '2026-10-13'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("样本清空后天级聚合必须一起消失，实际 %d 行", cnt)
	}
	// 中间表 daily_metrics 也要一起被删（否则来源级数据会一直留着旧值）
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM daily_metrics`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("daily_metrics 残留 %d 行", cnt)
	}

	// 全坏日期：toDateArray 给出空数组，等于是「什么都不算」但不能报错
	if err := r.Recompute(ctx, []string{"不是日期"}, []string{"hr"}); err != nil {
		t.Fatalf("坏日期不该报错: %v", err)
	}
}

// TestRecomputePendingDrainsQueue 锁定重算队列的消费语义：
//   - 每次最多取 limit 条，取完即从队列删除（不是标记完成）；
//   - 取出的项会真的重算到 daily_metrics_day；
//   - 队列空的时候返回 0 而不是报错。
func TestRecomputePendingDrainsQueue(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	reg := metric.New(metric.Defaults())
	r := NewRollup(db, reg)

	if n, err := r.RecomputePending(ctx, 10); err != nil || n != 0 {
		t.Fatalf("空队列应返回 0：%d %v", n, err)
	}

	// step_count 两天 + 心率一天（用字典里真实存在的指标，重算才走真实口径）
	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		{Metric: "step_count", Source: "iPhone", StartTime: time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC),
			Value: 1000, LocalDate: "2026-10-12", TZOffsetMin: 480},
		{Metric: "step_count", Source: "iPhone", StartTime: time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC),
			Value: 2000, LocalDate: "2026-10-13", TZOffsetMin: 480},
		{Metric: "heart_rate", Source: "iPhone", StartTime: time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC),
			Value: 70, LocalDate: "2026-10-13", TZOffsetMin: 480},
	}, "strict"); err != nil {
		t.Fatal(err)
	}
	if err := db.EnqueueRollup(ctx, map[string]map[string]struct{}{
		"2026-10-12": {"step_count": {}},
		"2026-10-13": {"step_count": {}, "heart_rate": {}},
	}); err != nil {
		t.Fatal(err)
	}

	n, err := r.RecomputePending(ctx, 2)
	if err != nil {
		t.Fatalf("重算待办失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("limit=2 应处理 2 条，实际 %d", n)
	}
	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM rollup_queue`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("处理完的项必须出队，队列还剩 %d 条", left)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM daily_metrics_day`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 2 {
		t.Fatalf("两条待办应产出 2 行天级聚合，实际 %d", left)
	}

	n, err = r.RecomputePending(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("剩下的 1 条应被处理，实际 %d", n)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM rollup_queue`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("队列应排空，实际剩 %d", left)
	}
	// 三天里每天的值都要对（10-12 步数 1000）
	var v float64
	if err := db.Pool.QueryRow(ctx, `SELECT value FROM daily_metrics_day
		WHERE local_date = '2026-10-12' AND metric = 'step_count'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 1000 {
		t.Fatalf("重算结果不符：%v", v)
	}
}

// TestRecomputeRange 锁定运维命令「整段重建」的口径：日期数按两端都含返回，
// 区间内所有已登记指标都重算一遍；坏区间必须报错。
func TestRecomputeRange(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	reg := metric.New(metric.Defaults())
	r := NewRollup(db, reg)

	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		{Metric: "step_count", Source: "iPhone", StartTime: time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC),
			Value: 500, LocalDate: "2026-10-11", TZOffsetMin: 480},
		{Metric: "step_count", Source: "iPhone", StartTime: time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC),
			Value: 600, LocalDate: "2026-10-13", TZOffsetMin: 480},
	}, "strict"); err != nil {
		t.Fatal(err)
	}

	n, err := r.RecomputeRange(ctx, "2026-10-11", "2026-10-13")
	if err != nil {
		t.Fatalf("整段重算失败: %v", err)
	}
	if n != 3 {
		t.Fatalf("应报告 3 天，实际 %d", n)
	}
	var cnt int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM daily_metrics_day WHERE metric = 'step_count'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 2 {
		t.Fatalf("只有两天有样本，应产出 2 行，实际 %d", cnt)
	}

	if _, err := r.RecomputeRange(ctx, "bad", "2026-10-13"); err == nil {
		t.Fatal("非法起始日期要报错")
	}
	if _, err := r.RecomputeRange(ctx, "2026-10-13", "bad"); err == nil {
		t.Fatal("非法结束日期要报错")
	}
	if _, err := r.RecomputeRange(ctx, "2026-10-13", "2026-10-11"); err == nil {
		t.Fatal("倒序区间要报错")
	}
}

// TestRollupRunBackgroundLoop 锁定后台 worker 的生命周期：
//   - workers < 1 与 idle <= 0 都要收敛到安全默认值（否则 0 个 worker 或空转打爆库）；
//   - ctx 取消后必须退出（Run 返回），否则服务关不掉；
//   - 库报错时只记 warn 并重试，不能让整个后台 goroutine 死掉。
func TestRollupRunBackgroundLoop(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	reg := metric.New(metric.Defaults())
	if err := db.EnqueueRollup(ctx, map[string]map[string]struct{}{
		"2026-10-13": {"step_count": {}},
	}); err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		NewRollup(db, reg).Run(runCtx, 0, 0) // workers=0 / idle=0 都要有兜底
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run 没有随 ctx 超时退出")
	}
	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM rollup_queue`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("worker 应把队列里的项吃掉，实际剩 %d", left)
	}

	// 库不可用：只 warn 不退出，多个 worker 也要能一起随 ctx 收工
	broken := openMissingSchemaDB(t)
	brokenCtx, brokenCancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer brokenCancel()
	bdone := make(chan struct{})
	go func() {
		NewRollup(broken, reg).Run(brokenCtx, 2, time.Millisecond)
		close(bdone)
	}()
	select {
	case <-bdone:
	case <-time.After(10 * time.Second):
		t.Fatal("报错路径下 Run 也要随 ctx 退出")
	}
}

// TestRecomputeErrorPaths 覆盖重算中途失败的分支（关系不可见时 DELETE 就炸）。
func TestRecomputeErrorPaths(t *testing.T) {
	broken := openMissingSchemaDB(t)
	ctx := context.Background()

	if err := NewRollup(broken, probeRegistry()).Recompute(ctx, []string{"2026-10-13"}, []string{"hr"}); err == nil {
		t.Fatal("库不可用时 Recompute 必须报错")
	}
	if _, err := NewRollup(broken, probeRegistry()).RecomputePending(ctx, 10); err == nil {
		t.Fatal("库不可用时 RecomputePending 必须报错")
	}
}
