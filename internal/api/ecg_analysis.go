package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/ecg"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// 心电分析端点：把区间内的逐次测量聚合成面向普通用户的统计视图。
//
// 与 /api/ecg 的分工：那个端点给「记录列表」，只回元信息；
// 这个端点给「分析页」，回趋势、分布、对比和四个可解释指数。
// 它必须连波形一起算——所有指数都从波形实时算出（R 峰 → RR → HRV），
// 页面请求一次全量算完（120 条实测约 0.4s），不为它加预计算表：
// 自托管单用户的数据量不到那个临界点，多一张要维护一致性的表不划算。
//
// 用户看不到的任何原始参数（采样率、点数）都不进响应：
// 它们只以「约 30 秒」「39 次心跳」这种健康含义的形式出现。

// ecgAnalysisLimit 是单次分析的最大记录数。区间再长也只取最近这么多条，
// 由调用方在响应里如实说明（见 summary.analyzed）。
const ecgAnalysisLimit = 300

// ecgAnalysisPoint 是一次测量在分析页面上的一行。
type ecgAnalysisPoint struct {
	ID      int64    `json:"id"`
	Date    string   `json:"date"`  // 本地日期
	Clock   string   `json:"clock"` // 本地时刻 HH:MM
	Code    string   `json:"code"`
	Label   string   `json:"label"` // 归一措辞；认不出时回落上游原文
	Known   bool     `json:"known"` // 措辞是否我们的归一结论（false = 上游原话）
	AvgHR   *float64 `json:"avg_hr"`
	Beats   *int     `json:"beats"` // 检出心跳数；无波形时为 null
	HRV     *int     `json:"hrv"`   // 心率变异性指数 0-100
	Stress  *int     `json:"stress"`
	Fatigue *int     `json:"fatigue"`
	// Quality 信号质量：good/fair/poor/none（无波形）。
	// 它同时是指数的开关：poor 的记录不进任何指数统计。
	Quality string `json:"quality"`
	// Rhythm 节律：regular/mild/irregular（RR 变异系数分档）。
	Rhythm string `json:"rhythm"`
}

// ecgIndexValue 是一个 0-100 指数的区间汇总。
type ecgIndexValue struct {
	Score  *int   `json:"score"`  // 区间均值
	Latest *int   `json:"latest"` // 最近一次
	Level  string `json:"level"`  // 均值的档位；无数据时为空串
	Basis  string `json:"basis"`  // 「n 次有效测量」
}

// ecgRiskValue 是心脏疾病风险关注度。
type ecgRiskValue struct {
	Score   *int     `json:"score"`
	Level   string   `json:"level"`
	Drivers []string `json:"drivers"` // 触发项，原样展示给用户
	Basis   string   `json:"basis"`
}

// ecgAnalysisSummary 是区间摘要（印象卡与卡片脚注的数据源）。
type ecgAnalysisSummary struct {
	Count         int      `json:"count"`
	MeasuredDays  int      `json:"measured_days"`
	WithMetrics   int      `json:"with_metrics"` // 有有效指数（信号质量过关）的次数
	AvgHR         *float64 `json:"avg_hr"`
	MinHR         *float64 `json:"min_hr"`
	MaxHR         *float64 `json:"max_hr"`
	LatestDate    string   `json:"latest_date"`
	LatestClock   string   `json:"latest_clock"`
	DominantCode  string   `json:"dominant_code"`
	DominantLabel string   `json:"dominant_label"`
	DominantRatio float64  `json:"dominant_ratio"`
	AbnormalCount int      `json:"abnormal_count"` // 房颤/高心率/测不准/未识别合计
}

// ecgDistItem 是判定分布的一项（分布图数据源）。
type ecgDistItem struct {
	Code  string  `json:"code"`
	Label string  `json:"label"`
	Known bool    `json:"known"`
	Count int     `json:"count"`
	Ratio float64 `json:"ratio"`
}

// ecgWindowStats 是一个时间窗的统计量（对比视图的一行）。
type ecgWindowStats struct {
	Count      int      `json:"count"`
	AvgHR      *float64 `json:"avg_hr"`
	SinusRatio *float64 `json:"sinus_ratio"`
	HRV        *float64 `json:"hrv"`
	Stress     *float64 `json:"stress"`
	Fatigue    *float64 `json:"fatigue"`
	Risk       *float64 `json:"risk"`
}

// ecgDelta 是本周期减去上一周期的差（nil = 有一侧没数据，不比）。
type ecgDelta struct {
	AvgHR      *float64 `json:"avg_hr"`
	SinusRatio *float64 `json:"sinus_ratio"`
	HRV        *float64 `json:"hrv"`
	Stress     *float64 `json:"stress"`
	Fatigue    *float64 `json:"fatigue"`
	Risk       *float64 `json:"risk"`
}

// ecgComparison 是与上一个等长周期的对比。
type ecgComparison struct {
	WindowDays int             `json:"window_days"`
	Current    *ecgWindowStats `json:"current"`
	Previous   *ecgWindowStats `json:"previous"`
	Delta      *ecgDelta       `json:"delta"`
}

func (d *Dashboard) ecgAnalysis(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := d.defaultRange(q.Get("from"), q.Get("to"), 90)
	ctx := r.Context()

	rows, err := d.db.ECGRangeFull(ctx, from, to, ecgAnalysisLimit)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	points := analyzeECGRecords(rows)

	resp := map[string]any{
		"from":         from,
		"to":           to,
		"records":      points,
		"summary":      summarizeECG(points),
		"indices":      ecgIndices(points),
		"distribution": ecgDistribution(points),
		"comparison":   d.ecgComparison(ctx, from, to, points),
	}
	WriteJSON(w, http.StatusOK, resp)
}

// analyzeECGRecords 并行计算逐记录指标。
//
// 每条记录的处理是纯 CPU（检测 + HRV 约 3ms），区间可能有几百条。
// worker pool 的上限取 8：再高对这个小任务没有意义，还能省点调度开销。
func analyzeECGRecords(rows []postgres.ECGRecordFull) []ecgAnalysisPoint {
	out := make([]ecgAnalysisPoint, len(rows))
	if len(rows) == 0 {
		return out
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	if workers > len(rows) {
		workers = len(rows)
	}
	var wg sync.WaitGroup
	ch := make(chan int)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range ch {
				out[idx] = analyzeOneECG(rows[idx])
			}
		}()
	}
	for i := range rows {
		ch <- i
	}
	close(ch)
	wg.Wait()
	return out
}

// analyzeOneECG 计算一条记录的展示指标。
//
// 指数与信号质量联动：quality=poor 的记录（检不出足够心跳、或与上游
// 心率对不上）一律不出指数——坏数据算出来的指数比没有指数更坏。
func analyzeOneECG(row postgres.ECGRecordFull) ecgAnalysisPoint {
	p := ecgAnalysisPoint{
		ID:    row.ID,
		Date:  row.LocalDate,
		Clock: ecgClock(row.Start, row.TZOffsetMin),
		Code:  row.ClassificationCode,
		Known: ecg.Known(ecg.Code(row.ClassificationCode)),
	}
	if label := ecg.DisplayZH(ecg.Code(row.ClassificationCode)); label != "" {
		p.Label = label
	} else {
		p.Label = row.Classification
	}
	hr := row.AverageHeartRate

	if len(row.Voltages) > 0 && row.SamplingFrequency != nil && *row.SamplingFrequency > 0 {
		fs := float64(*row.SamplingFrequency)
		det := ecg.DetectRPeaks(row.Voltages, fs)
		h := ecg.ComputeHRV(det.RR)
		if h.Beats <= 0 {
			// 有波形但一个心跳都检不出：不是「没有数据」，是「数据不可用」
			p.Quality = "poor"
			p.AvgHR = hr
			return p
		}
		beats := h.Beats
		p.Beats = &beats

		// 信号质量两条判据：
		//  1. 自洽性——采用心跳数 / 按时长与平均 RR 推算的期望心跳数。
		//     被 outlier 过滤剔得越多，比值越低。
		//  2. 交叉核对——检测心率与 Apple 自己算的平均心率是否一致
		//     （上游没给心率时只做第 1 条）。整批漏峰会让心率对不上。
		dur := row.DurationSec
		if dur <= 0 {
			dur = float64(len(row.Voltages)) / fs
		}
		expected := dur / (h.MeanRR / 1000)
		ratio := float64(h.Beats) / expected
		hrOK := true
		if hr != nil && *hr > 0 && h.MeanHR > 0 {
			hrOK = math.Abs(h.MeanHR-(*hr))/(*hr) <= 0.15
		}
		switch {
		case h.Beats < 8 || !hrOK:
			p.Quality = "poor"
		case ratio >= 0.85:
			p.Quality = "good"
		case ratio >= 0.7:
			p.Quality = "fair"
		default:
			p.Quality = "poor"
		}

		if p.Quality != "poor" {
			hrvScore := ecg.ScoreHRV(h.RMSSD)
			p.HRV = &hrvScore
			if si, ok := ecg.StressIndex(det.RR); ok {
				stressScore := ecg.ScoreStress(si)
				p.Stress = &stressScore
				fatigueScore := ecg.ScoreFatigue(hrvScore, stressScore, h.MeanHR)
				p.Fatigue = &fatigueScore
			}
		}
		switch {
		case h.CV <= 0.05:
			p.Rhythm = "regular"
		case h.CV <= 0.12:
			p.Rhythm = "mild"
		default:
			p.Rhythm = "irregular"
		}
		// 上游没给平均心率（或给了 0）时，用检测值补上——
		// 列表里不该出现莫名其妙的空档，更不该把 0 当成真实心率
		if (hr == nil || *hr <= 0) && h.MeanHR > 0 {
			v := h.MeanHR
			hr = &v
		}
	} else {
		p.Quality = "none"
	}
	p.AvgHR = hr
	return p
}

// ecgClock 按记录自带的时区偏移还原本地时刻（与前端 fmtTime 同口径）。
func ecgClock(iso string, tzOffsetMin int) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	d := t.Add(time.Duration(tzOffsetMin) * time.Minute).UTC()
	return fmt.Sprintf("%02d:%02d", d.Hour(), d.Minute())
}

// summarizeECG 汇总区间摘要。
func summarizeECG(points []ecgAnalysisPoint) ecgAnalysisSummary {
	s := ecgAnalysisSummary{Count: len(points)}
	if len(points) == 0 {
		return s
	}
	days := make(map[string]bool, len(points))
	hrs := make([]float64, 0, len(points))
	counts := make(map[string]int, len(points))
	for _, p := range points {
		days[p.Date] = true
		if p.AvgHR != nil {
			hrs = append(hrs, *p.AvgHR)
		}
		counts[p.Code]++
		if p.HRV != nil {
			s.WithMetrics++
		}
		switch ecg.Code(p.Code) {
		case ecg.CodeAtrialFibrillation, ecg.CodeHighHeartRate,
			ecg.CodeInconclusive, ecg.CodeInconclusiveLowHeartRate,
			ecg.CodeInconclusiveHighHeartRate, ecg.CodeInconclusivePoorRecording,
			ecg.CodeUnrecognized:
			s.AbnormalCount++
		}
	}
	s.MeasuredDays = len(days)
	if len(hrs) > 0 {
		avg, lo, hi := hrs[0], hrs[0], hrs[0]
		for _, v := range hrs[1:] {
			avg += v
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		avg /= float64(len(hrs))
		s.AvgHR, s.MinHR, s.MaxHR = &avg, &lo, &hi
	}
	// 众数判定：次数相同时取码字典序，保证输出稳定（不随 map 遍历漂移）
	best, bestN := "", 0
	for code, n := range counts {
		if n > bestN || (n == bestN && (best == "" || code < best)) {
			best, bestN = code, n
		}
	}
	s.DominantCode = best
	s.DominantLabel = ecg.DisplayZH(ecg.Code(best))
	if s.DominantLabel == "" {
		s.DominantLabel = "未识别"
	}
	s.DominantRatio = float64(bestN) / float64(len(points))
	last := points[len(points)-1] // 升序，末位即最近
	s.LatestDate, s.LatestClock = last.Date, last.Clock
	return s
}

// ecgIndices 汇总四个指数卡片。
func ecgIndices(points []ecgAnalysisPoint) map[string]any {
	collect := func(get func(*ecgAnalysisPoint) *int) (vals []int, latest *int) {
		for i := range points {
			if v := get(&points[i]); v != nil {
				vals = append(vals, *v)
				latest = v
			}
		}
		return vals, latest
	}
	mean := func(vals []int) *int {
		if len(vals) == 0 {
			return nil
		}
		sum := 0
		for _, v := range vals {
			sum += v
		}
		avg := int(math.Round(float64(sum) / float64(len(vals))))
		return &avg
	}
	index := func(level func(int) string, vals []int, latest *int) ecgIndexValue {
		avg := mean(vals)
		v := ecgIndexValue{Score: avg, Latest: latest}
		if avg != nil {
			v.Level = level(*avg)
			v.Basis = fmt.Sprintf("%d 次有效测量", len(vals))
		} else {
			v.Basis = "暂无有效测量"
		}
		return v
	}

	hrvVals, hrvLatest := collect(func(p *ecgAnalysisPoint) *int { return p.HRV })
	stressVals, stressLatest := collect(func(p *ecgAnalysisPoint) *int { return p.Stress })
	fatigueVals, fatigueLatest := collect(func(p *ecgAnalysisPoint) *int { return p.Fatigue })

	// 风险关注度：规则引擎吃整个区间的判定分布（见 ecg.RiskScore）
	counts := make(map[ecg.Code]int, len(points))
	for i := range points {
		counts[ecg.Code(points[i].Code)]++
	}
	riskScore, drivers := ecg.RiskScore(ecg.RiskInput{
		Total:  len(points),
		AFib:   counts[ecg.CodeAtrialFibrillation],
		HighHR: counts[ecg.CodeHighHeartRate],
		Undetermined: counts[ecg.CodeInconclusive] + counts[ecg.CodeInconclusiveLowHeartRate] +
			counts[ecg.CodeInconclusiveHighHeartRate] + counts[ecg.CodeInconclusivePoorRecording],
		HRVAvg: mean(hrvVals),
	})
	risk := ecgRiskValue{Score: &riskScore, Level: ecg.RiskLevel(riskScore), Drivers: drivers}
	if len(drivers) == 0 {
		risk.Drivers = []string{}
	}
	if len(points) > 0 {
		risk.Basis = fmt.Sprintf("基于 %d 次测量的透明规则", len(points))
	} else {
		risk.Basis = "暂无测量"
	}

	return map[string]any{
		"hrv":     index(ecg.HRVLevel, hrvVals, hrvLatest),
		"stress":  index(ecg.IndexLevel, stressVals, stressLatest),
		"fatigue": index(ecg.IndexLevel, fatigueVals, fatigueLatest),
		"risk":    risk,
	}
}

// ecgDistribution 按归一码统计判定分布，次数降序。
func ecgDistribution(points []ecgAnalysisPoint) []ecgDistItem {
	counts := make(map[string]int, len(points))
	for i := range points {
		counts[points[i].Code]++
	}
	out := make([]ecgDistItem, 0, len(counts))
	for code, n := range counts {
		label := ecg.DisplayZH(ecg.Code(code))
		if label == "" {
			label = "未识别"
		}
		out = append(out, ecgDistItem{
			Code:  code,
			Label: label,
			Known: ecg.Known(ecg.Code(code)),
			Count: n,
			Ratio: float64(n) / float64(len(points)),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// ecgComparison 计算与上一个等长周期的对比。
//
// 查询失败不拖垮主响应：对比是附属信息，没了它就显示「暂无对比」。
func (d *Dashboard) ecgComparison(ctx context.Context, from, to string, points []ecgAnalysisPoint) ecgComparison {
	span := daysBetween(from, to)
	cmp := ecgComparison{WindowDays: span}
	cur := ecgWindowStatsOf(points)
	cmp.Current = &cur

	prevFrom := shiftDate(from, -span)
	prevTo := shiftDate(from, -1)
	if prevFrom == "" || prevTo == "" {
		return cmp
	}
	rows, err := d.db.ECGRangeFull(ctx, prevFrom, prevTo, ecgAnalysisLimit)
	if err != nil {
		return cmp
	}
	prev := ecgWindowStatsOf(analyzeECGRecords(rows))
	if prev.Count == 0 {
		return cmp
	}
	cmp.Previous = &prev
	cmp.Delta = ecgDeltaOf(cur, prev)
	return cmp
}

// ecgWindowStatsOf 汇总一个时间窗的统计量。
func ecgWindowStatsOf(points []ecgAnalysisPoint) ecgWindowStats {
	st := ecgWindowStats{Count: len(points)}
	if len(points) == 0 {
		return st
	}
	var hrs []float64
	sinus := 0
	var hrv, stress, fatigue []float64
	for i := range points {
		p := &points[i]
		if p.AvgHR != nil {
			hrs = append(hrs, *p.AvgHR)
		}
		if ecg.Code(p.Code) == ecg.CodeSinusRhythm {
			sinus++
		}
		if p.HRV != nil {
			hrv = append(hrv, float64(*p.HRV))
		}
		if p.Stress != nil {
			stress = append(stress, float64(*p.Stress))
		}
		if p.Fatigue != nil {
			fatigue = append(fatigue, float64(*p.Fatigue))
		}
	}
	st.AvgHR = meanOrNil(hrs)
	st.SinusRatio = ptrFloat(float64(sinus) / float64(len(points)))
	st.HRV = meanOrNil(hrv)
	st.Stress = meanOrNil(stress)
	st.Fatigue = meanOrNil(fatigue)
	// 风险同样按窗口分布算，与主卡片的规则完全一致
	counts := make(map[ecg.Code]int, len(points))
	for i := range points {
		counts[ecg.Code(points[i].Code)]++
	}
	var hrvAvg *int
	if v := meanOrNil(hrv); v != nil {
		n := int(*v)
		hrvAvg = &n
	}
	score, _ := ecg.RiskScore(ecg.RiskInput{
		Total:  len(points),
		AFib:   counts[ecg.CodeAtrialFibrillation],
		HighHR: counts[ecg.CodeHighHeartRate],
		Undetermined: counts[ecg.CodeInconclusive] + counts[ecg.CodeInconclusiveLowHeartRate] +
			counts[ecg.CodeInconclusiveHighHeartRate] + counts[ecg.CodeInconclusivePoorRecording],
		HRVAvg: hrvAvg,
	})
	st.Risk = ptrFloat(float64(score))
	return st
}

func ecgDeltaOf(cur, prev ecgWindowStats) *ecgDelta {
	d := &ecgDelta{
		AvgHR:      subOrNil(cur.AvgHR, prev.AvgHR),
		SinusRatio: subOrNil(cur.SinusRatio, prev.SinusRatio),
		HRV:        subOrNil(cur.HRV, prev.HRV),
		Stress:     subOrNil(cur.Stress, prev.Stress),
		Fatigue:    subOrNil(cur.Fatigue, prev.Fatigue),
		Risk:       subOrNil(cur.Risk, prev.Risk),
	}
	if d.AvgHR == nil && d.SinusRatio == nil && d.HRV == nil &&
		d.Stress == nil && d.Fatigue == nil && d.Risk == nil {
		return nil
	}
	return d
}

func subOrNil(a, b *float64) *float64 {
	if a == nil || b == nil {
		return nil
	}
	v := *a - *b
	return &v
}

func meanOrNil(vals []float64) *float64 {
	if len(vals) == 0 {
		return nil
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	m := sum / float64(len(vals))
	return &m
}

func ptrFloat(v float64) *float64 { return &v }

// daysBetween 返回闭区间 [from, to] 的天数；解析失败按 1 天。
func daysBetween(from, to string) int {
	fd, err := time.Parse("2006-01-02", from)
	if err != nil {
		return 1
	}
	td, err := time.Parse("2006-01-02", to)
	if err != nil || td.Before(fd) {
		return 1
	}
	return int(td.Sub(fd)/(24*time.Hour)) + 1
}

// shiftDate 把 YYYY-MM-DD 平移 days 天；解析失败返回空串（调用方跳过对比）。
func shiftDate(date string, days int) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}
