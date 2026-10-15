// Package mcp 暴露健康数据的只读查询能力，供 AI 客户端自然语言调用。
package mcp

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/ecg"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// Deps 是 MCP 工具所需的依赖。
type Deps struct {
	DB       *postgres.DB
	Metrics  *metric.Registry
	Location *time.Location
	Timezone string

	// Redact 打开后，全部工具的返回值里的数值都换成占位符（日期与钟点保留）。
	// 用于把 MCP 接到云端 AI 客户端时：指标目录、口径、哪天有数据照常可问，
	// 但任何测量大小都出不了本机。由 ZHICHANG_MCP_REDACT=true 开启，默认关。
	Redact bool
}

// Result 是工具的文本输出。
type Result struct {
	Text string `json:"text"`
}

func (d *Deps) today() string {
	return time.Now().In(d.Location).Format("2006-01-02")
}

func (d *Deps) shift(days int) string {
	return time.Now().In(d.Location).AddDate(0, 0, days).Format("2006-01-02")
}

func (d *Deps) known(name string) bool {
	return d.Metrics.IsKnown(name)
}

func (d *Deps) resolve(raw string) ([]string, string) {
	if strings.TrimSpace(raw) == "" {
		out := []string{}
		for _, def := range d.Metrics.Enabled() {
			out = append(out, def.Metric)
		}
		return out, ""
	}
	var ok, bad []string
	for _, m := range strings.Split(raw, ",") {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if d.known(m) {
			ok = append(ok, m)
		} else {
			bad = append(bad, m)
		}
	}
	if len(bad) > 0 {
		return ok, "未登记的指标: " + strings.Join(bad, ", ")
	}
	return ok, ""
}

func (d *Deps) label(m string) string {
	if def, ok := d.Metrics.Get(m); ok {
		return fmt.Sprintf("%s(%s)", def.DisplayNameZH, m)
	}
	return m
}

func (d *Deps) unit(m string) string {
	if def, ok := d.Metrics.Get(m); ok {
		return def.PreferredUnit
	}
	return ""
}

// ListMetrics 输出指标目录。
func (d *Deps) ListMetrics(ctx context.Context) (string, error) {
	defs := d.Metrics.All()
	sort.Slice(defs, func(i, j int) bool { return defs[i].SortOrder < defs[j].SortOrder })
	var b strings.Builder
	b.WriteString(fmt.Sprintf("指标目录（时区 %s，共 %d 项）\n", d.Timezone, len(defs)))
	b.WriteString("| 指标 | 中文名 | 类别 | 日内口径 | 跨来源 | 单位 |\n|---|---|---|---|---|---|\n")
	for _, def := range defs {
		if !def.Enabled {
			continue
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
			def.Metric, def.DisplayNameZH, def.Kind, def.DayAgg, def.CrossSourceAgg, def.PreferredUnit))
	}
	return b.String(), nil
}

// DailySummary 输出指定日期的全貌。
func (d *Deps) DailySummary(ctx context.Context, date string) (string, error) {
	if date == "" {
		date = d.today()
	}
	items, err := d.DB.Summary(ctx, date, nil)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return fmt.Sprintf("%s 没有聚合数据（可能尚未同步或该日无记录）", date), nil
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := d.Metrics.MustGet(items[i].Metric), d.Metrics.MustGet(items[j].Metric)
		return a.SortOrder < b.SortOrder
	})
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s 概览（时区 %s）\n", date, d.Timezone))
	for _, it := range items {
		line := fmt.Sprintf("- %s: %s", d.label(it.Metric), fmtNum(it.Value, d.unit(it.Metric)))
		if it.PrevValue != nil {
			line += fmt.Sprintf("（前一日 %s", fmtNum(it.PrevValue, d.unit(it.Metric)))
			if it.DeltaPct != nil {
				line += fmt.Sprintf("，%+.1f%%", *it.DeltaPct)
			}
			line += "）"
		}
		b.WriteString(line + "\n")
	}
	rows, err := d.DB.SleepRange(ctx, date, date)
	if err == nil && len(rows) > 0 {
		s := rows[0]
		b.WriteString(fmt.Sprintf("- 睡眠: 总 %.1fh（深睡 %.1fh / 核心 %.1fh / REM %.1fh）\n",
			ptr(s.TotalSleepHr), ptr(s.DeepHr), ptr(s.CoreHr), ptr(s.RemHr)))
	}
	return b.String(), nil
}

// Query 输出指定指标在区间内的天级序列。
func (d *Deps) Query(ctx context.Context, raw, from, to string) (string, error) {
	metrics, warn := d.resolve(raw)
	if len(metrics) == 0 {
		return "", fmt.Errorf("没有可用指标；%s", warn)
	}
	if from == "" {
		from = d.shift(-29)
	}
	if to == "" {
		to = d.today()
	}
	points, err := d.DB.DailySeries(ctx, metrics, from, to)
	if err != nil {
		return "", err
	}
	if len(points) == 0 {
		return fmt.Sprintf("%s ~ %s 没有数据", from, to), nil
	}
	byMetric := map[string][]postgres.DailyPoint{}
	for _, p := range points {
		byMetric[p.Metric] = append(byMetric[p.Metric], p)
	}
	var b strings.Builder
	if warn != "" {
		b.WriteString(warn + "\n")
	}
	for _, m := range metrics {
		list := byMetric[m]
		if len(list) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("\n%s（%s ~ %s，%d 天，单位 %s）\n", d.label(m), from, to, len(list), d.unit(m)))
		for _, p := range list {
			b.WriteString(fmt.Sprintf("  %s  %.2f  (来源 %d: %s)\n", p.Date, p.Value, p.SourceCount, strings.Join(p.Sources, "/")))
		}
	}
	return b.String(), nil
}

// Compare 对比两个等长区间。
func (d *Deps) Compare(ctx context.Context, raw string, aFrom, aTo, bFrom, bTo string) (string, error) {
	metrics, warn := d.resolve(raw)
	if len(metrics) == 0 {
		return "", fmt.Errorf("没有可用指标；%s", warn)
	}
	if aFrom == "" || aTo == "" {
		aTo, aFrom = d.today(), d.shift(-6)
	}
	if bFrom == "" || bTo == "" {
		bTo, bFrom = d.shift(-7), d.shift(-13)
	}
	agg := func(from, to string) (map[string]float64, map[string]int, error) {
		points, err := d.DB.DailySeries(ctx, metrics, from, to)
		if err != nil {
			return nil, nil, err
		}
		sums, days := map[string]float64{}, map[string]int{}
		for _, p := range points {
			sums[p.Metric] += p.Value
			days[p.Metric]++
		}
		return sums, days, nil
	}
	aSum, aDays, err := agg(aFrom, aTo)
	if err != nil {
		return "", err
	}
	bSum, bDays, err := agg(bFrom, bTo)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if warn != "" {
		b.WriteString(warn + "\n")
	}
	b.WriteString(fmt.Sprintf("区间 A %s~%s 与区间 B %s~%s 对比（日均）\n", aFrom, aTo, bFrom, bTo))
	b.WriteString("| 指标 | A 日均 | B 日均 | 变化 | 变化率 |\n|---|---|---|---|---|\n")
	for _, m := range metrics {
		ad, bd := aDays[m], bDays[m]
		if ad == 0 || bd == 0 {
			continue
		}
		av, bv := aSum[m]/float64(ad), bSum[m]/float64(bd)
		delta := av - bv
		pct := 0.0
		if bv != 0 {
			pct = delta / bv * 100
		}
		b.WriteString(fmt.Sprintf("| %s | %.2f | %.2f | %+.2f | %+.1f%% |\n", d.label(m), av, bv, delta, pct))
	}
	return b.String(), nil
}

// Latest 输出各指标的最新值与时间。
func (d *Deps) Latest(ctx context.Context, raw string) (string, error) {
	metrics, warn := d.resolve(raw)
	if len(metrics) == 0 {
		return "", fmt.Errorf("没有可用指标；%s", warn)
	}
	to := d.today()
	from := d.shift(-60)
	points, err := d.DB.DailySeries(ctx, metrics, from, to)
	if err != nil {
		return "", err
	}
	latest := map[string]postgres.DailyPoint{}
	for _, p := range points {
		if cur, ok := latest[p.Metric]; !ok || p.Date > cur.Date {
			latest[p.Metric] = p
		}
	}
	var b strings.Builder
	if warn != "" {
		b.WriteString(warn + "\n")
	}
	b.WriteString("最新值（按本地日期）\n")
	for _, m := range metrics {
		p, ok := latest[m]
		if !ok {
			b.WriteString(fmt.Sprintf("- %s: 近 60 天无数据\n", d.label(m)))
			continue
		}
		b.WriteString(fmt.Sprintf("- %s: %s (%s，来源 %s)\n", d.label(m), fmtNum(&p.Value, d.unit(m)), p.Date, strings.Join(p.Sources, "/")))
	}
	return b.String(), nil
}

// Sleep 输出睡眠记录（含按入睡时间窗关联的夜间心率）。
func (d *Deps) Sleep(ctx context.Context, from, to string) (string, error) {
	if from == "" {
		from = d.shift(-29)
	}
	if to == "" {
		to = d.today()
	}
	rows, err := d.DB.SleepRange(ctx, from, to)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return fmt.Sprintf("%s ~ %s 没有睡眠记录", from, to), nil
	}
	// 夜间心率按「入睡时间窗」关联，不按 local_date：跨零点的后半夜样本
	// 归属次日，按日期取会把最低值整段漏掉。
	hr := map[string]postgres.NightMetricRow{}
	if nm, err := d.DB.SleepNightMetrics(ctx, from, to, []string{"heart_rate"}); err == nil {
		for _, r := range nm {
			hr[r.LocalDate] = r
		}
	}
	var b strings.Builder
	total := 0.0
	b.WriteString(fmt.Sprintf("睡眠 %s ~ %s（%d 天）\n", from, to, len(rows)))
	b.WriteString("| 日期 | 总睡眠(h) | 深睡 | 核心 | REM | 在床 | 夜间最低心率 | 来源 |\n|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		total += ptr(r.TotalSleepHr)
		nightHr := "—"
		if v, ok := hr[r.LocalDate]; ok {
			nightHr = fmt.Sprintf("%.0f bpm", v.Min)
		}
		b.WriteString(fmt.Sprintf("| %s | %.1f | %.1f | %.1f | %.1f | %.1f | %s | %s |\n",
			r.LocalDate, ptr(r.TotalSleepHr), ptr(r.DeepHr), ptr(r.CoreHr), ptr(r.RemHr), ptr(r.InBedHr),
			nightHr, r.Source))
	}
	b.WriteString(fmt.Sprintf("\n日均总睡眠 %.1f 小时\n", total/float64(len(rows))))
	b.WriteString("逐晚分期时间线与夜间心率明细：用 health_sleep_detail 传睡眠日查询")
	return b.String(), nil
}

// stageLabels 是归一化阶段键的中文名（与前端 sleep-stages 同一套键）。
var stageLabels = map[string]string{
	"awake":  "清醒",
	"rem":    "REM",
	"core":   "核心",
	"deep":   "深睡",
	"asleep": "睡眠",
	"inbed":  "在床",
	"other":  "其他",
}

// nightClock 把「本夜轴分钟」（睡眠日本地 00:00 起算，跨零点顺延）写成钟点。
func nightClock(min float64) string {
	m := int(math.Round(min)) % 1440
	if m < 0 {
		m += 1440
	}
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// localClock 把 UTC RFC3339 时刻按记录自带偏移还原成本地钟点。
func localClock(rfc string, tzOffsetMin int) string {
	if rfc == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return "—"
	}
	return t.Add(time.Duration(tzOffsetMin) * time.Minute).UTC().Format("15:04")
}

// SleepDetail 输出某一晚的详情：时刻、分期占比、逐段时间线、窗口内的生理指标。
//
// 分期与生理指标都按入睡时间窗关联（见 SleepStages / SleepNightMetrics 的口径），
// 所以凌晨那半段既在时间线上，也在夜间心率的统计窗口里。
func (d *Deps) SleepDetail(ctx context.Context, date string) (string, error) {
	if date == "" {
		date = d.today()
	}
	st, err := d.DB.SleepStages(ctx, date)
	if err != nil {
		return "", err
	}
	sessions, err := d.DB.SleepRange(ctx, date, date)
	if err != nil {
		return "", err
	}
	nm, err := d.DB.SleepNightMetrics(ctx, date, date, nil)
	if err != nil {
		return "", err
	}

	// 同一晚可能有多来源会话：取时长最长的那条，信息最完整
	var sess *postgres.SleepRow
	for i := range sessions {
		if sess == nil || ptr(sessions[i].TotalSleepHr) > ptr(sess.TotalSleepHr) {
			sess = &sessions[i]
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("睡眠详情 %s\n", date))
	if st.SleepStart != nil && st.SleepEnd != nil {
		b.WriteString(fmt.Sprintf("- 入睡 %s → 起床 %s（tz %+d 分钟）\n",
			localClock(*st.SleepStart, st.TZOffsetMin), localClock(*st.SleepEnd, st.TZOffsetMin), st.TZOffsetMin))
	} else if sess != nil && sess.SleepStart != nil && sess.SleepEnd != nil {
		b.WriteString(fmt.Sprintf("- 入睡 %s → 起床 %s\n",
			localClock(*sess.SleepStart, sess.TZOffsetMin), localClock(*sess.SleepEnd, sess.TZOffsetMin)))
	} else {
		b.WriteString("- 这一晚没有入睡/起床时刻记录\n")
	}
	if sess != nil {
		b.WriteString(fmt.Sprintf("- 时长：总睡眠 %.1fh · 深睡 %.1fh · 核心 %.1fh · REM %.1fh · 在床 %.1fh\n",
			ptr(sess.TotalSleepHr), ptr(sess.DeepHr), ptr(sess.CoreHr), ptr(sess.RemHr), ptr(sess.InBedHr)))
		if sess.InBedStart != nil && sess.SleepStart != nil {
			if inBed, e1 := time.Parse(time.RFC3339, *sess.InBedStart); e1 == nil {
				if asleep, e2 := time.Parse(time.RFC3339, *sess.SleepStart); e2 == nil {
					b.WriteString(fmt.Sprintf("- 入睡潜伏期 %.0f 分钟\n", asleep.Sub(inBed).Minutes()))
				}
			}
		}
		if sess.WasoHr != nil {
			n := ""
			if sess.AwakeCount != nil {
				n = fmt.Sprintf(" · %d 次", *sess.AwakeCount)
			}
			b.WriteString(fmt.Sprintf("- 入睡后清醒 %.0f 分钟%s（不含入睡前那段）\n", ptr(sess.WasoHr)*60, n))
		} else if sess.AwakeHr != nil {
			b.WriteString(fmt.Sprintf("- 整晚清醒 %.0f 分钟（导出未含分段，无法区分入睡前/后）\n", ptr(sess.AwakeHr)*60))
		}
	}

	if len(st.Segments) == 0 {
		b.WriteString("- 分期时间线：无（导出端需在 sleep_analysis 里带上分段）\n")
	} else {
		b.WriteString(fmt.Sprintf("- 分期时间线（%d 段，来源 %s）：\n", len(st.Segments), st.Source))
		for _, s := range st.Segments {
			label := stageLabels[s.Stage]
			if label == "" {
				label = s.Stage
			}
			b.WriteString(fmt.Sprintf("  %s–%s %s（%.0f 分）\n",
				nightClock(float64(s.LocalMin)), nightClock(float64(s.LocalMin)+s.DurationMin), label, s.DurationMin))
		}
	}

	if len(nm) == 0 {
		b.WriteString("- 夜间生理指标：入睡窗口内没有样本")
	} else {
		b.WriteString("- 夜间生理指标（入睡窗口内）：")
		for i, r := range nm {
			if i > 0 {
				b.WriteString("；")
			}
			unit := r.Unit
			if unit == "count/min" {
				unit = "bpm"
			}
			tz := 0
			if r.MinAtTZOffsetMin != nil {
				tz = *r.MinAtTZOffsetMin
			}
			minAt := ""
			if r.MinAt != "" {
				minAt = fmt.Sprintf("，%s 出现", localClock(r.MinAt, tz))
			}
			b.WriteString(fmt.Sprintf("%s 最低 %.0f%s · 平均 %.0f%s · %d 点 · 来源 %s%s",
				metricLabel(d, r.Metric), r.Min, " "+unit, r.Avg, " "+unit, r.SampleCount, r.Source, minAt))
		}
	}
	return b.String(), nil
}

// metricLabel 取指标字典里的中文名，取不到就原样返回。
func metricLabel(d *Deps, metric string) string {
	if def, ok := d.Metrics.Get(metric); ok && def.DisplayNameZH != "" {
		return def.DisplayNameZH
	}
	return metric
}

// Workouts 输出运动记录。
func (d *Deps) Workouts(ctx context.Context, from, to string) (string, error) {
	if from == "" {
		from = d.shift(-29)
	}
	if to == "" {
		to = d.today()
	}
	rows, err := d.DB.WorkoutRange(ctx, from, to)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return fmt.Sprintf("%s ~ %s 没有运动记录", from, to), nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("运动 %s ~ %s（%d 次）\n", from, to, len(rows)))
	b.WriteString("| 日期 | 项目 | 时长(min) | 消耗 | 距离 | 来源 |\n|---|---|---|---|---|---|\n")
	for _, r := range rows {
		energy, dist := "—", "—"
		if r.TotalEnergy != nil {
			energy = fmt.Sprintf("%.0f %s", *r.TotalEnergy, r.EnergyUnit)
		}
		if r.TotalDistance != nil {
			dist = fmt.Sprintf("%.2f %s", *r.TotalDistance, r.DistanceUnit)
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %.0f | %s | %s | %s |\n", r.LocalDate, r.Name, r.DurationMin, energy, dist, r.Source))
	}
	return b.String(), nil
}

// ECG 输出心电记录与判定结论。
//
// 刻意不返回波形：一次测量 15360 个点，塞进上下文既放不下也没有解读价值。
// 这里给的是「哪天测了、Apple 判定是什么、当时平均心率多少」——
// 波形留给网页看。
func (d *Deps) ECG(ctx context.Context, from, to string) (string, error) {
	if from == "" {
		from = d.shift(-89)
	}
	if to == "" {
		to = d.today()
	}
	rows, err := d.DB.ECGRange(ctx, from, to)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return fmt.Sprintf("%s ~ %s 没有心电记录", from, to), nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("心电图 %s ~ %s（%d 次）\n", from, to, len(rows)))
	b.WriteString("| 日期 | 时刻 | 判定 | 平均心率 | 采样 | 点数 |\n|---|---|---|---|---|---|\n")
	for _, r := range rows {
		label := ecg.DisplayZH(ecg.Code(r.ClassificationCode))
		if label == "" {
			// 归一没认出来时展示上游原文，别拿自己的措辞盖掉它
			label = r.Classification
			if label == "" {
				label = "（上游未给判定）"
			}
		}
		hr, fs := "—", "—"
		if r.AverageHeartRate != nil {
			hr = fmt.Sprintf("%.0f bpm", *r.AverageHeartRate)
		}
		if r.SamplingFrequency != nil {
			fs = fmt.Sprintf("%d Hz", *r.SamplingFrequency)
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %d |\n",
			r.LocalDate, hhmm(r.Start, r.TZOffsetMin), label, hr, fs, r.VoltageCount))
	}
	return b.String(), nil
}

// hhmm 把 UTC 时刻按记录自带的偏移还原成本地钟点。
func hhmm(iso string, tzOffsetMin int) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.Add(time.Duration(tzOffsetMin) * time.Minute).Format("15:04")
}

// Coverage 输出数据覆盖度与新鲜度。
func (d *Deps) Coverage(ctx context.Context, days int) (string, error) {
	if days <= 0 {
		days = 30
	}
	st, err := d.DB.SyncStatusOf(ctx, days)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if st.LastRun != nil {
		b.WriteString(fmt.Sprintf("最近同步: %s（%s，收到 %d / 入库 %d / 重复 %d，状态 %s）\n",
			st.LastRun.ReceivedAt, st.LastRun.Transport, st.LastRun.SampleCount, st.LastRun.Inserted, st.LastRun.Duplicates, st.LastRun.Status))
	} else {
		b.WriteString("最近同步: 尚无记录\n")
	}
	if len(st.MissingDays) == 0 {
		b.WriteString(fmt.Sprintf("近 %d 天无缺失日\n", days))
	} else {
		b.WriteString(fmt.Sprintf("近 %d 天缺失 %d 天: %s\n", days, len(st.MissingDays), strings.Join(st.MissingDays, ", ")))
	}
	if len(st.Watermarks) > 0 {
		b.WriteString("\n同步水位（automation × metric）\n")
		for _, w := range st.Watermarks {
			b.WriteString(fmt.Sprintf("- %s / %s: %s (%s)\n", w.AutomationID, w.Metric, w.LastLocalDate, w.LastStartTime))
		}
	}
	return b.String(), nil
}

// Correlation 计算两个指标的日级相关性（Pearson）。
func (d *Deps) Correlation(ctx context.Context, a, b, from, to string) (string, error) {
	if !d.known(a) || !d.known(b) {
		return "", fmt.Errorf("指标未登记: %s / %s", a, b)
	}
	if from == "" {
		from = d.shift(-89)
	}
	if to == "" {
		to = d.today()
	}
	points, err := d.DB.DailySeries(ctx, []string{a, b}, from, to)
	if err != nil {
		return "", err
	}
	xs := map[string]float64{}
	ys := map[string]float64{}
	for _, p := range points {
		if p.Metric == a {
			xs[p.Date] = p.Value
		} else {
			ys[p.Date] = p.Value
		}
	}
	var keys []string
	for k := range xs {
		if _, ok := ys[k]; ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	n := len(keys)
	if n < 3 {
		return fmt.Sprintf("可对齐的日期不足（%d 天），无法计算相关性", n), nil
	}
	var sx, sy, sxx, syy, sxy float64
	for _, k := range keys {
		x, y := xs[k], ys[k]
		sx += x
		sy += y
		sxx += x * x
		syy += y * y
		sxy += x * y
	}
	fn := float64(n)
	cov := sxy/fn - (sx/fn)*(sy/fn)
	va := sxx/fn - (sx/fn)*(sx/fn)
	vb := syy/fn - (sy/fn)*(sy/fn)
	if va <= 0 || vb <= 0 {
		return fmt.Sprintf("%s 与 %s 在 %s~%s 内方差为 0，无法计算相关性", d.label(a), d.label(b), from, to), nil
	}
	r := cov / math.Sqrt(va*vb)
	return fmt.Sprintf("%s 与 %s（%s~%s，%d 天对齐）皮尔逊相关系数 r = %.3f\n参考：|r|>0.5 视为中等以上相关",
		d.label(a), d.label(b), from, to, n, r), nil
}

func ptr(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func fmtNum(v *float64, unit string) string {
	if v == nil {
		return "—"
	}
	s := fmt.Sprintf("%.2f", *v)
	if unit != "" {
		s += " " + unit
	}
	return s
}
