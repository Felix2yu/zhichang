package postgres

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"
)

// 「今日 vs 我的常态」的口径定义。
//
// 为什么不是「今日 vs 昨日」：体重、静息心率、HRV 这类指标的日内噪声远大于日间趋势，
// 一天涨跌几点百分点几乎没有信息量，看久了只会让人怀疑数据是不是坏了。
// 产品名取自「知常曰明」——要回答的是「今天偏离我自己的常态多少」，
// 所以对比基准取**对比日之前一段时间的中位数**，这也是苹果健康「与典型值比较」的口径。
//
// 三条硬口径（改之前先看这里的理由）：
//  1. 常态窗口**不含对比日本身**。否则今天的值会参与自己的基准，
//     偏离看起来永远只有一半。
//  2. 窗口相对**对比日**而非请求日回溯。今天没同步时对比日回落到最后有数据的那天，
//     基准跟着它走，否则拿「三天前的当天」比「三十天前」这段，跨了日历却没跨口径。
//  3. 样本不足（低于 MinBaselineDays）时**不给基准**。两天算出来的中位数是假精确，
//     宁可让卡片显示「暂无常态」也不要编一个数字。

const (
	// DefaultBaselineWindow 默认常态窗口（天）。
	DefaultBaselineWindow = 30
	// MinBaselineWindow / MaxBaselineWindow 窗口的取值边界。
	// 下界防止「近 3 天常态」这类无意义的窗口，上界防止一次拉全表。
	MinBaselineWindow = 7
	MaxBaselineWindow = 365
	// MinBaselineDays 计算常态所需的最少样本天数。
	MinBaselineDays = 4
	// RecentPointLimit 迷你走势（卡片里的 sparkline）带回的点数。
	RecentPointLimit = 7
)

// BaselinePoint 是迷你走势上的一个点（有数据的日期才出现）。
type BaselinePoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// BaselineItem 是一个指标「最新值 vs 我的常态」的对照。
type BaselineItem struct {
	Metric string `json:"metric"`
	// Date 是对比值所在日期；今日无数据时回落到最后有数据的那天（此时 Stale 为 true）。
	Date string `json:"date"`
	// Value 对比日的天级值；窗口内完全无数据时为 null。
	Value *float64 `json:"value"`
	// PrevValue 上一日的值（按期日序相邻的那天，不一定正好 −1 天）。
	PrevValue *float64 `json:"prev_value"`
	// Baseline 常态：常态窗口内天级值的中位数。
	Baseline *float64 `json:"baseline"`
	// DeviationPct 相对常态的偏离百分比；常态缺失或为 0 时为 null。
	DeviationPct *float64 `json:"deviation_pct"`
	// SampleDays 参与常态计算的天数。低于 MinBaselineDays 时 Baseline 为 null。
	SampleDays int `json:"sample_days"`
	// WindowDays 本次使用的常态窗口天数。
	WindowDays int `json:"window_days"`
	// Recent 截至对比日的最后若干个有数据的点（日期升序），用于卡片里的迷你走势。
	Recent []BaselinePoint `json:"recent"`
	// Stale 为 true 表示对比日不是请求的日期（今天还没数据，回落到更早的一天）。
	Stale bool `json:"stale"`
}

// dayValue 是某指标某天的天级值。
type dayValue struct {
	date  string
	value float64
}

// baselineWindow 是一个指标常态窗口的中间结果（四分位只给 Insights 用，不出现在 JSON 里）。
type baselineWindow struct {
	values []float64 // 升序
	p25    float64
	p50    float64
	p75    float64
}

// Baseline 返回若干指标的「最新值 vs 常态」对照。
//
// 返回顺序与入参 metrics 一致（便于前端直接按下标渲染），调用方负责去重。
func (db *DB) Baseline(ctx context.Context, metrics []string, date string, windowDays int) ([]BaselineItem, error) {
	if len(metrics) == 0 {
		return []BaselineItem{}, nil
	}
	anchor, err := parseDate(date)
	if err != nil {
		return nil, err
	}
	window := clampWindow(windowDays)
	series, err := db.fetchDaySeries(ctx, metrics, anchor, window)
	if err != nil {
		return nil, err
	}
	anchorStr := anchor.Time.Format("2006-01-02")
	out := make([]BaselineItem, 0, len(metrics))
	for _, m := range metrics {
		item, _ := buildBaseline(m, series[m], anchorStr, window)
		out = append(out, item)
	}
	return out, nil
}

// fetchDaySeries 读取若干指标在 [对比日 − window, 请求日] 内的天级值。
//
// 区间左端点按请求日回溯而不是按某个指标的对比日——后者需要先知道对比日，
// 会绕成两趟查询。多取的天数只在 SampleDays 上表现为「没那么满」，不影响口径。
func (db *DB) fetchDaySeries(ctx context.Context, metrics []string, anchor pgtype.Date, windowDays int) (map[string][]dayValue, error) {
	from := pgtype.Date{Time: anchor.Time.AddDate(0, 0, -windowDays), Valid: true}
	rows, err := db.Pool.Query(ctx, `SELECT local_date, metric, value
		FROM daily_metrics_day
		WHERE metric = ANY($1) AND local_date BETWEEN $2 AND $3
		ORDER BY metric, local_date`, metrics, from, anchor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]dayValue, len(metrics))
	for rows.Next() {
		var ld pgtype.Date
		var m string
		var v float64
		if err := rows.Scan(&ld, &m, &v); err != nil {
			return nil, err
		}
		out[m] = append(out[m], dayValue{date: ld.Time.Format("2006-01-02"), value: v})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// buildBaseline 由一段天级序列算出对照结果。
// cells 必须按日期升序（fetchDaySeries 的 ORDER BY 保证了这一点）。
func buildBaseline(metric string, cells []dayValue, anchor string, windowDays int) (BaselineItem, baselineWindow) {
	item := BaselineItem{
		Metric:     metric,
		WindowDays: windowDays,
		Recent:     make([]BaselinePoint, 0, RecentPointLimit),
	}
	var empty baselineWindow
	if len(cells) == 0 {
		return item, empty
	}

	last := cells[len(cells)-1]
	v := last.value
	item.Date = last.date
	item.Value = &v
	item.Stale = last.date != anchor

	if pv, ok := lookupDay(cells, shiftDayStr(last.date, -1)); ok {
		prev := pv
		item.PrevValue = &prev
	}

	win := windowOf(cells, last.date, windowDays)
	item.SampleDays = len(win.values)
	if len(win.values) >= MinBaselineDays {
		base := win.p50
		item.Baseline = &base
		if pct := deviationPct(v, base); pct != nil {
			item.DeviationPct = pct
		}
	}

	start := len(cells) - RecentPointLimit
	if start < 0 {
		start = 0
	}
	for _, c := range cells[start:] {
		item.Recent = append(item.Recent, BaselinePoint{Date: c.date, Value: c.value})
	}
	return item, win
}

// windowOf 截取 [valueDate − windowDays, valueDate − 1] 的值并算好四分位。
func windowOf(cells []dayValue, valueDate string, windowDays int) baselineWindow {
	from := shiftDayStr(valueDate, -windowDays)
	vals := make([]float64, 0, len(cells))
	for _, c := range cells {
		if c.date >= from && c.date < valueDate {
			vals = append(vals, c.value)
		}
	}
	sort.Float64s(vals)
	w := baselineWindow{values: vals}
	if len(vals) > 0 {
		w.p25 = quantileSorted(vals, 0.25)
		w.p50 = quantileSorted(vals, 0.5)
		w.p75 = quantileSorted(vals, 0.75)
	}
	return w
}

// lookupDay 按日期取值。
func lookupDay(cells []dayValue, date string) (float64, bool) {
	for _, c := range cells {
		if c.date == date {
			return c.value, true
		}
	}
	return 0, false
}

// shiftDayStr 把 'YYYY-MM-DD' 平移 n 天。日期串按字典序即时间序，比较时直接用。
func shiftDayStr(date string, days int) string {
	d, err := parseDate(date)
	if err != nil {
		return date
	}
	return d.Time.AddDate(0, 0, days).Format("2006-01-02")
}

// deviationPct 计算相对基准的偏离百分比；基准为 0 时无法给出百分比。
func deviationPct(value, base float64) *float64 {
	if base == 0 {
		return nil
	}
	pct := (value - base) / base * 100
	return &pct
}

// quantileSorted 线性插值分位数，与 PostgreSQL 的 percentile_cont 同口径。
// 入参必须升序。
func quantileSorted(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	pos := q * float64(n-1)
	lo := int(pos)
	if lo >= n-1 {
		return sorted[n-1]
	}
	frac := pos - float64(lo)
	return sorted[lo] + (sorted[lo+1]-sorted[lo])*frac
}

// clampWindow 把常态窗口夹到合理区间。
func clampWindow(days int) int {
	if days <= 0 {
		return DefaultBaselineWindow
	}
	if days < MinBaselineWindow {
		return MinBaselineWindow
	}
	if days > MaxBaselineWindow {
		return MaxBaselineWindow
	}
	return days
}
