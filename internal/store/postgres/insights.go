package postgres

import (
	"context"
	"math"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"
)

// 「偏离常态提示」的判定口径。
//
// 两条触发线，任一命中即上报：
//  1. **离群**（kind=outlier）：当天值落在常态窗口的 Tukey 栅栏之外
//     （低于 p25 − 1.5×IQR 或高于 p75 + 1.5×IQR）。用四分位距而不是标准差，
//     是因为健康数据常有单个异常点，标准差会被它自身拉大从而漏报。
//  2. **连续偏离**（kind=streak）：连续若干天都朝同一方向偏离常态且幅度达标。
//     单日的离群可能只是设备没戴好，连续三天才是真信号。
//
// 两条线都要求偏离幅度不低于 InsightMinPct：体重 68.0 与 68.1 的差异值得记录，
// 但不值得推到概览页上占位置。
const (
	// InsightMinPct 视为「有别于常态」的最小相对幅度（%）。
	InsightMinPct = 5
	// InsightStreakDays 连续偏离达到该天数即上报。
	InsightStreakDays = 3
	// insightFence 离群判定的四分位距倍数（Tukey 栅栏）。
	insightFence = 1.5
	// MinInsightDays 参与判定所需的最少常态样本天数。样本太少时四分位不可信。
	MinInsightDays = 7
	// DefaultInsightLimit 默认返回条数。
	DefaultInsightLimit = 6
	// MaxInsightLimit 条数上限：概览页放不下太多，也不该把噪声堆上去。
	MaxInsightLimit = 20
)

// InsightItem 是一条「值得关注」的偏离。
type InsightItem struct {
	Metric string `json:"metric"`
	// Kind 为 outlier（离群）或 streak（连续偏离）。
	Kind string `json:"kind"`
	// Direction 为 high（高于常态）或 low（低于常态）。刻意不带「好 / 坏」判断：
	// 步数低与静息心率低含义相反，判好坏需要用户自己的目标，不该由服务端代劳。
	Direction    string  `json:"direction"`
	Date         string  `json:"date"`
	Value        float64 `json:"value"`
	Baseline     float64 `json:"baseline"`
	DeviationPct float64 `json:"deviation_pct"`
	// StreakDays 连续偏离的天数；streak 类型才有意义，outlier 为 0。
	StreakDays int `json:"streak_days"`
}

// Insights 扫描指标列表，返回偏离常态最明显的若干条。
//
// 与 Baseline 共用同一套窗口口径，避免「卡片说偏高、提示说正常」的口径分裂。
func (db *DB) Insights(ctx context.Context, metrics []string, date string, windowDays, limit int) ([]InsightItem, error) {
	if len(metrics) == 0 {
		return []InsightItem{}, nil
	}
	anchor, err := parseDate(date)
	if err != nil {
		return nil, err
	}
	window := clampWindow(windowDays)
	if limit <= 0 {
		limit = DefaultInsightLimit
	}
	if limit > MaxInsightLimit {
		limit = MaxInsightLimit
	}

	series, err := db.fetchDaySeries(ctx, metrics, anchor, window)
	if err != nil {
		return nil, err
	}
	anchorStr := anchor.Time.Format("2006-01-02")

	out := make([]InsightItem, 0, limit)
	for _, m := range metrics {
		cells := series[m]
		if len(cells) == 0 {
			continue
		}
		item, win := buildBaseline(m, cells, anchorStr, window)
		if item.Value == nil || item.Baseline == nil || len(win.values) < MinInsightDays {
			continue
		}
		if insight, ok := judgeInsight(item, win, cells); ok {
			out = append(out, insight)
		}
	}
	// 幅度大的排前面；相等时按指标名稳定排序，避免每次刷新顺序乱跳
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := math.Abs(out[i].DeviationPct), math.Abs(out[j].DeviationPct)
		if di != dj {
			return di > dj
		}
		return out[i].Metric < out[j].Metric
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// judgeInsight 判定某指标是否值得关注。
func judgeInsight(item BaselineItem, win baselineWindow, cells []dayValue) (InsightItem, bool) {
	value := *item.Value
	base := *item.Baseline
	// 基准为 0（窗口内取值恒为 0）时百分比无意义，跳过而不是拿 ±Inf 去比较
	if base == 0 {
		return InsightItem{}, false
	}
	pct := (value - base) / base * 100
	if math.Abs(pct) < InsightMinPct {
		return InsightItem{}, false
	}
	direction := "high"
	if pct < 0 {
		direction = "low"
	}

	res := InsightItem{
		Metric:       item.Metric,
		Date:         item.Date,
		Value:        value,
		Baseline:     base,
		DeviationPct: pct,
		Direction:    direction,
	}

	// 连续偏离优先：它比单点离群更能说明问题，也更好解释「为什么推给我」
	if streak := streakDays(cells, base, direction); streak >= InsightStreakDays {
		res.Kind = "streak"
		res.StreakDays = streak
		return res, true
	}
	// 离群：四分位距为 0 说明窗口内取值几乎恒定（如身高），此时任何微小偏离都会
	// 越过栅栏，属于噪声而非信号，直接跳过。
	iqr := win.p75 - win.p25
	if iqr <= 0 {
		return InsightItem{}, false
	}
	lower := win.p25 - insightFence*iqr
	upper := win.p75 + insightFence*iqr
	if value < lower || value > upper {
		res.Kind = "outlier"
		return res, true
	}
	return InsightItem{}, false
}

// streakDays 从最后一天往回数，连续「日期相邻且朝同一方向偏离常态达标」的天数。
func streakDays(cells []dayValue, base float64, direction string) int {
	n := 0
	want := shiftDayStr(cells[len(cells)-1].date, 0)
	for i := len(cells) - 1; i >= 0; i-- {
		if cells[i].date != want {
			break
		}
		if base == 0 {
			break
		}
		pct := (cells[i].value - base) / base * 100
		if math.Abs(pct) < InsightMinPct {
			break
		}
		if (direction == "high") != (pct > 0) {
			break
		}
		n++
		want = shiftDayStr(cells[i].date, -1)
	}
	return n
}

// CoverageItem 是一个指标在窗口内的数据覆盖情况。
type CoverageItem struct {
	Metric string `json:"metric"`
	// Days 窗口内有数据的天数。
	Days int `json:"days"`
	// LastDate 最后有数据的日期。
	LastDate string `json:"last_date"`
	// DaysSince 距请求日多少天；无数据时为 −1。
	DaysSince int `json:"days_since"`
}

// Coverage 是窗口内的数据覆盖度快照。
//
// 它回答的是「为什么这张卡是空的」——是今天还没同步，还是这个指标从来就没接过数据。
// 后者在排查期尤其重要：字典里登记了 40 个指标，实际有数据的可能只有十几个，
// 界面上却长得一模一样。
type Coverage struct {
	From       string         `json:"from"`
	To         string         `json:"to"`
	WindowDays int            `json:"window_days"`
	Rows       []CoverageItem `json:"rows"`
}

// CoverageOf 统计窗口内每个有数据指标的活跃天数与最后更新日。
//
// 只回「有数据的指标」：窗口内无数据的指标本来就不该出现在列表里，
// 前端拿字典做一次差集即可得出「从未接入」清单，比让服务端猜字典范围更准。
func (db *DB) CoverageOf(ctx context.Context, date string, windowDays int) (Coverage, error) {
	window := clampWindow(windowDays)
	anchor, err := parseDate(date)
	if err != nil {
		return Coverage{}, err
	}
	from := pgtype.Date{Time: anchor.Time.AddDate(0, 0, -(window - 1)), Valid: true}

	out := Coverage{
		From:       from.Time.Format("2006-01-02"),
		To:         anchor.Time.Format("2006-01-02"),
		WindowDays: window,
		Rows:       make([]CoverageItem, 0, 32),
	}
	rows, err := db.Pool.Query(ctx, `SELECT metric, count(*), max(local_date)
		FROM daily_metrics_day
		WHERE local_date BETWEEN $1 AND $2
		GROUP BY metric
		ORDER BY metric`, from, anchor)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	for rows.Next() {
		var m string
		var cnt int
		var ld pgtype.Date
		if err := rows.Scan(&m, &cnt, &ld); err != nil {
			return out, err
		}
		last := ld.Time.Format("2006-01-02")
		out.Rows = append(out.Rows, CoverageItem{
			Metric:    m,
			Days:      cnt,
			LastDate:  last,
			DaysSince: dayGap(last, out.To),
		})
	}
	return out, rows.Err()
}

// dayGap 计算 from 到 to 相差几天（同日为 0）；解析失败返回 −1。
func dayGap(from, to string) int {
	f, err := parseDate(from)
	if err != nil {
		return -1
	}
	t, err := parseDate(to)
	if err != nil {
		return -1
	}
	return int(t.Time.Sub(f.Time).Hours() / 24)
}
