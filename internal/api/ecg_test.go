package api

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/ecg"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// 本文件锁 /api/ecg、/api/ecg/analysis 与 /api/ecg/waveform 的对外契约：
//   - 判定中文化在服务端完成（上游原文随手机语言变化，不能直接给前端）；
//   - 认不出判定时回落原文并明确标注来路；
//   - 列表绝不带波形（那是 120KB），波形单独一条接口；
//   - 分析端点回聚合视图：逐记录指数、区间摘要、分布、环比，全部本地算出；
//   - 空结果集是 []，不是 null。

func addECG(t *testing.T, db *postgres.DB, runID int64, recs ...sample.ECGRecord) {
	t.Helper()
	if _, err := db.WriteECG(context.Background(), runID, recs); err != nil {
		t.Fatalf("写心电失败: %v", err)
	}
}

func ecgRec(date string, hour, minute int, classification string) sample.ECGRecord {
	at := wallAt(date, hour, minute)
	rec := sample.ECGRecord{
		Source:             "移动心电图房颤提示软件",
		StartTime:          at.UTC(),
		EndTime:            at.UTC().Add(30 * time.Second),
		LocalDate:          date,
		TZOffsetMin:        480,
		Classification:     classification,
		ClassificationCode: ecg.Classify(classification),
		VoltageUnit:        "mcV",
	}
	hr := 78.0
	fs := 512
	rec.AverageHeartRate = &hr
	rec.SamplingFrequency = &fs
	return rec
}

// ecgRecWave 造一条带合成波形的记录：rr 秒一个心动周期、时长约 30 秒，
// 上游平均心率与波形自洽（60/rr），检测链应当能全部检出。
func ecgRecWave(date string, hour, minute int, classification string, rr float64) sample.ECGRecord {
	rec := ecgRec(date, hour, minute, classification)
	hr := 60 / rr
	rec.AverageHeartRate = &hr
	rec.Voltages = ecgWave(rr, int(30/rr))
	rec.VoltageCount = len(rec.Voltages)
	return rec
}

// ecgWave 生成合成心电波形：每个心动周期一个 QRS 尖峰（σ=35ms）。
// RR 带固定抖动——完全等距的波形 RMSSD 恒为 0、Baevsky 指数无定义，
// 压力/疲劳会恒为 null，契约就锁不住了。
func ecgWave(rr float64, beats int) []float64 {
	const fs = 512.0
	jitter := []float64{0.04, -0.02, 0.01, -0.03, 0.02, -0.01}
	out := []float64{}
	for b := 0; b < beats; b++ {
		step := rr + jitter[b%len(jitter)]
		for j := 0; j < int(step*fs); j++ {
			t := float64(j) / fs
			out = append(out, math.Exp(-math.Pow(t-0.012, 2)/(2*0.035*0.035)))
		}
	}
	return out
}

func TestECGListContract(t *testing.T) {
	h, db := newRouter(t, "")
	runID := newRun(t, db)

	withWave := ecgRec(dateInLoc(0), 23, 16, "窦性心律")
	withWave.Voltages = []float64{-63.28, 0, 66.42}
	withWave.VoltageCount = len(withWave.Voltages)

	bare := ecgRec(dateInLoc(-3), 22, 45, "Sinus Rhythm") // 英文原文，归一到同一个码
	odd := ecgRec(dateInLoc(-6), 21, 48, "室性早搏")          // 认不出的判定
	addECG(t, db, runID, withWave, bare, odd)

	r := get(t, h, "/api/ecg?from="+dateInLoc(-30)+"&to="+dateInLoc(0))
	if r.status != 200 {
		t.Fatalf("状态码 = %d body=%s", r.status, r.body)
	}
	recs := r.arr(t, "records")
	if len(recs) != 3 {
		t.Fatalf("应 3 条记录，实际 %d（body=%s）", len(recs), r.body)
	}

	first := recs[0].(map[string]any)
	// 上游是简中原文，服务端给出统一措辞
	if first["classification"] != "窦性心律" {
		t.Fatalf("原文应原样透传，实际 %v", first["classification"])
	}
	if first["classification_label"] != "窦性心律" {
		t.Fatalf("判定标签错误: %v", first["classification_label"])
	}
	if first["classification_known"] != true {
		t.Fatalf("简中判定应算已知: %v", first)
	}
	if first["voltage_count"] != float64(3) {
		t.Fatalf("点数错误: %v", first["voltage_count"])
	}
	if first["duration_sec"] != float64(30) {
		t.Fatalf("时长错误: %v", first["duration_sec"])
	}
	// 列表不能把波形带上：那是 120KB，列表页没有画它们的地方
	if _, ok := first["voltages"]; ok {
		t.Fatalf("列表不该包含波形: %v", first)
	}

	// 英文原文与简中原文必须归一到同一个标签——这是「换手机语言不分裂」的保证
	second := recs[1].(map[string]any)
	if second["classification"] != "Sinus Rhythm" {
		t.Fatalf("英文原文应原样保留，实际 %v", second["classification"])
	}
	if second["classification_label"] != first["classification_label"] {
		t.Fatalf("同一心律的两种语言写法应给同一标签: %v vs %v",
			first["classification_label"], second["classification_label"])
	}

	// 认不出的判定回落展示原文，并标明这是上游原话而不是我们的归一结论
	third := recs[2].(map[string]any)
	if third["classification_known"] != false {
		t.Fatalf("认不出的判定不该声称已知: %v", third)
	}
	if third["classification_label"] != "室性早搏" {
		t.Fatalf("认不出时应回落原文，实际 %v", third["classification_label"])
	}
}

func TestECGListEmptyIsArray(t *testing.T) {
	h, _ := newRouter(t, "")
	r := get(t, h, "/api/ecg?from=2000-01-01&to=2000-01-31")
	if r.status != 200 {
		t.Fatalf("状态码 = %d", r.status)
	}
	if got := r.arr(t, "records"); len(got) != 0 {
		t.Fatalf("空区间应给空数组，实际 %v", got)
	}
}

func TestECGListRejectsBadDate(t *testing.T) {
	h, _ := newRouter(t, "")
	get(t, h, "/api/ecg?from=2026-13-99&to=2026-10-31").wantErr(t, 400)
}

func TestECGWaveformContract(t *testing.T) {
	h, db := newRouter(t, "")
	runID := newRun(t, db)

	rec := ecgRec(dateInLoc(0), 23, 16, "窦性心律")
	rec.Voltages = []float64{-63.284713745117188, -14.61724853515625, 0, 407.3128662109375}
	rec.VoltageCount = len(rec.Voltages)
	addECG(t, db, runID, rec)

	rows, err := db.ECGRange(context.Background(), dateInLoc(0), dateInLoc(0))
	if err != nil || len(rows) != 1 {
		t.Fatalf("读取记录失败: rows=%d err=%v", len(rows), err)
	}
	id := rows[0].ID

	r := get(t, h, "/api/ecg/waveform?id="+strconv.FormatInt(id, 10))
	if r.status != 200 {
		t.Fatalf("状态码 = %d body=%s", r.status, r.body)
	}
	vs := r.arr(t, "voltages")
	if len(vs) != 4 {
		t.Fatalf("波形点数应为 4，实际 %d", len(vs))
	}
	// 浮点必须原样往返，不能被 float32 截断
	if vs[0] != -63.284713745117188 {
		t.Fatalf("首个电压值被截断了: %v", vs[0])
	}
	if vs[3] != 407.3128662109375 {
		t.Fatalf("末个电压值被截断了: %v", vs[3])
	}
	if r.str(t, "unit") != "mcV" {
		t.Fatalf("单位应原样给出 mcV，实际 %q", r.str(t, "unit"))
	}
	// R 峰下标：服务端用与分析完全相同的算法检出，前端据此在波形上标心跳
	peaks := r.arr(t, "peaks")
	if peaks == nil {
		t.Fatalf("波形响应应包含 peaks 数组")
	}
	if r.num(t, "duration_sec") != 4.0/512.0 {
		t.Fatalf("时长应由点数/采样率推出，实际 %v", r.fields["duration_sec"])
	}

	// 记录不存在：404 而不是空波形——两者是不同的事
	get(t, h, "/api/ecg/waveform?id=999999").wantErr(t, 404)
	// id 非法：400
	get(t, h, "/api/ecg/waveform?id=abc").wantErr(t, 400)
	get(t, h, "/api/ecg/waveform").wantErr(t, 400)
	get(t, h, "/api/ecg/waveform?id=0").wantErr(t, 400)
}

// 记录在、波形没存（上游这次没导出电压数据）时，接口给空数组而不是 404，
// 前端据此显示「这条记录没有波形数据」。
func TestECGWaveformEmptyWhenNotStored(t *testing.T) {
	h, db := newRouter(t, "")
	runID := newRun(t, db)
	addECG(t, db, runID, ecgRec(dateInLoc(0), 23, 16, "窦性心律"))

	rows, err := db.ECGRange(context.Background(), dateInLoc(0), dateInLoc(0))
	if err != nil || len(rows) != 1 {
		t.Fatalf("读取记录失败: %v", err)
	}
	r := get(t, h, "/api/ecg/waveform?id="+strconv.FormatInt(rows[0].ID, 10))
	if r.status != 200 {
		t.Fatalf("状态码 = %d body=%s", r.status, r.body)
	}
	if got := r.arr(t, "voltages"); len(got) != 0 {
		t.Fatalf("没有波形时应给空数组，实际 %v", got)
	}
}

// 心电是事件数据，绝不能混进指标接口：混进去会让天级聚合凭空多出数据。
func TestECGNotExposedAsMetric(t *testing.T) {
	h, _ := newRouter(t, "")
	m := get(t, h, "/api/metrics")
	for _, item := range m.arr(t, "metrics") {
		if def := item.(map[string]any); def["metric"] == "ecg" || def["metric"] == "ecg_records" {
			t.Fatalf("心电不该出现在指标字典里: %v", def)
		}
	}
	get(t, h, "/api/samples?metric=ecg").wantErr(t, 400)
}

// —— /api/ecg/analysis 的契约 ——

func TestECGAnalysisContract(t *testing.T) {
	h, db := newRouter(t, "")
	runID := newRun(t, db)

	// 当前窗口（-3..0）：两条带波形、一条不带
	a := ecgRecWave(dateInLoc(-2), 23, 10, "窦性心律", 0.80) // 75bpm
	b := ecgRecWave(dateInLoc(0), 22, 30, "窦性心律", 0.70)  // 86bpm
	bare := ecgRec(dateInLoc(-1), 21, 0, "窦性心律")         // 无波形
	// 上一窗口（-6..-4）：一条带波形，供环比
	prev := ecgRecWave(dateInLoc(-6), 23, 0, "窦性心律", 0.90) // 67bpm
	// 平直波形：有电压但检不出心跳——必须判 poor 且不出指数
	flat := ecgRec(dateInLoc(-1), 20, 0, "窦性心律")
	flat.Voltages = make([]float64, 512*30)
	flat.VoltageCount = len(flat.Voltages)
	addECG(t, db, runID, a, b, bare, prev, flat)

	r := get(t, h, "/api/ecg/analysis?from="+dateInLoc(-3)+"&to="+dateInLoc(0))
	if r.status != 200 {
		t.Fatalf("状态码 = %d body=%s", r.status, r.body)
	}
	if r.str(t, "from") != dateInLoc(-3) || r.str(t, "to") != dateInLoc(0) {
		t.Fatalf("区间回显错误: %v", r.fields)
	}

	recs := r.arr(t, "records")
	if len(recs) != 4 {
		t.Fatalf("应 4 条记录，实际 %d", len(recs))
	}
	// 升序：分析端点给趋势图用，前端不该再翻一次
	for i := 1; i < len(recs); i++ {
		prevDate := recs[i-1].(map[string]any)["date"].(string)
		curDate := recs[i].(map[string]any)["date"].(string)
		if curDate < prevDate {
			t.Fatalf("记录应按日期升序，实际 %v", recs)
		}
	}

	// 带波形的记录：必须算出心跳与三个指数
	first := recs[0].(map[string]any)
	if first["quality"] != "good" && first["quality"] != "fair" {
		t.Fatalf("合成波形应检出心跳，实际 quality=%v beats=%v", first["quality"], first["beats"])
	}
	if first["hrv"] == nil || first["stress"] == nil || first["fatigue"] == nil {
		t.Fatalf("有效记录应有三个指数，实际 %v", first)
	}
	if beats, ok := first["beats"].(float64); !ok || beats < 8 {
		t.Fatalf("beats 应 >=8，实际 %v", first["beats"])
	}
	if first["label"] != "窦性心律" || first["known"] != true {
		t.Fatalf("判定应中文化且标记已知，实际 %v", first)
	}

	// 平直波形：有波形但不可用——quality=poor 且不产出任何指数
	for _, it := range recs {
		m := it.(map[string]any)
		if m["clock"] == "20:00" {
			if m["quality"] != "poor" {
				t.Fatalf("检不出心跳应判 poor，实际 %v", m["quality"])
			}
			if m["hrv"] != nil || m["stress"] != nil || m["fatigue"] != nil {
				t.Fatalf("poor 记录不该出指数，实际 %v", m)
			}
		}
		// 无波形的记录：quality=none
		if m["clock"] == "21:00" {
			if m["quality"] != "none" || m["beats"] != nil {
				t.Fatalf("无波形应 quality=none 且 beats=null，实际 %v", m)
			}
			if m["hrv"] != nil {
				t.Fatalf("无波形不该出指数，实际 %v", m)
			}
		}
	}

	// 摘要（4 条记录分布在 3 个不同日期上）
	s := r.fields["summary"].(map[string]any)
	if s["count"] != float64(4) || s["measured_days"] != float64(3) {
		t.Fatalf("摘要计数错误: %v", s)
	}
	if s["with_metrics"] != float64(2) {
		t.Fatalf("有效测量应 2 次，实际 %v", s["with_metrics"])
	}
	if s["dominant_label"] != "窦性心律" || s["dominant_ratio"] != 1.0 {
		t.Fatalf("众数判定错误: %v", s)
	}
	if s["abnormal_count"] != float64(0) {
		t.Fatalf("全窦性不应有异常计数，实际 %v", s["abnormal_count"])
	}
	if s["latest_date"] != dateInLoc(0) || s["latest_clock"] != "22:30" {
		t.Fatalf("最近一次错误: %v", s)
	}
	if avg, ok := s["avg_hr"].(float64); !ok || avg < 60 || avg > 90 {
		t.Fatalf("平均心率应在合理范围，实际 %v", s["avg_hr"])
	}

	// 指数卡片
	idx := r.fields["indices"].(map[string]any)
	hrv := idx["hrv"].(map[string]any)
	if hrv["score"] == nil || hrv["latest"] == nil {
		t.Fatalf("HRV 应有均值与最近值，实际 %v", hrv)
	}
	if hrv["basis"] != "2 次有效测量" {
		t.Fatalf("HRV basis 错误: %v", hrv["basis"])
	}
	if lv := hrv["level"].(string); lv == "" {
		t.Fatalf("HRV 应有档位，实际 %v", hrv)
	}
	risk := idx["risk"].(map[string]any)
	// 合成波形 HRV 一般，风险分只应来自「HRV 偏低」这一条小额加分
	if score := risk["score"].(float64); score < 0 || score > 12 {
		t.Fatalf("全窦性应判低关注度，实际 %v", risk)
	}
	if risk["level"] != "低" {
		t.Fatalf("全窦性应判低: %v", risk)
	}
	drivers := risk["drivers"].([]any)
	if len(drivers) != 1 || drivers[0] != "区间内未发现异常提示" {
		t.Fatalf("无异常时触发项应如实说明，实际 %v", drivers)
	}

	// 分布
	dist := r.arr(t, "distribution")
	if len(dist) != 1 {
		t.Fatalf("应只有一种判定，实际 %v", dist)
	}
	d0 := dist[0].(map[string]any)
	if d0["code"] != "sinus_rhythm" || d0["count"] != float64(4) || d0["ratio"] != 1.0 {
		t.Fatalf("分布项错误: %v", d0)
	}

	// 环比：上一窗口有 1 条，delta 应非空
	cmp := r.fields["comparison"].(map[string]any)
	if cmp["window_days"] != float64(4) {
		t.Fatalf("对比窗口应为 4 天，实际 %v", cmp["window_days"])
	}
	if cur := cmp["current"].(map[string]any); cur["count"] != float64(4) {
		t.Fatalf("当前窗口计数错误: %v", cur)
	}
	if prevS := cmp["previous"].(map[string]any); prevS["count"] != float64(1) {
		t.Fatalf("上一窗口计数错误: %v", cmp["previous"])
	}
	if delta := cmp["delta"].(map[string]any); delta["avg_hr"] == nil {
		t.Fatalf("两侧都有数据时 delta 不应为空: %v", delta)
	}
}

// 房颤提示必须立刻抬高关注度并进入触发项——这是风险卡片的存在意义。
func TestECGAnalysisRiskWithAFib(t *testing.T) {
	h, db := newRouter(t, "")
	runID := newRun(t, db)
	sinus := ecgRecWave(dateInLoc(-2), 23, 10, "窦性心律", 0.80)
	afib := ecgRecWave(dateInLoc(-1), 23, 10, "房颤", 0.80)
	addECG(t, db, runID, sinus, afib)

	r := get(t, h, "/api/ecg/analysis?from="+dateInLoc(-3)+"&to="+dateInLoc(0))
	if r.status != 200 {
		t.Fatalf("状态码 = %d body=%s", r.status, r.body)
	}
	risk := r.fields["indices"].(map[string]any)["risk"].(map[string]any)
	if score := risk["score"].(float64); score < 55 {
		t.Fatalf("一次房颤应显著抬升关注度，实际 %v", score)
	}
	if risk["level"] == "低" {
		t.Fatalf("有房颤时不该判低: %v", risk)
	}
	found := false
	for _, d := range risk["drivers"].([]any) {
		if s, _ := d.(string); len(s) > 0 && (s == "1次测量提示房颤" || s == "1 次测量提示房颤") {
			found = true
		}
	}
	if !found {
		t.Fatalf("触发项应包含房颤，实际 %v", risk["drivers"])
	}
	// 摘要的异常计数与分布也要反映出来
	s := r.fields["summary"].(map[string]any)
	if s["abnormal_count"] != float64(1) {
		t.Fatalf("异常计数应 1，实际 %v", s["abnormal_count"])
	}
}

func TestECGAnalysisEmptyIsArray(t *testing.T) {
	h, _ := newRouter(t, "")
	r := get(t, h, "/api/ecg/analysis?from=2000-01-01&to=2000-01-31")
	if r.status != 200 {
		t.Fatalf("状态码 = %d", r.status)
	}
	if got := r.arr(t, "records"); len(got) != 0 {
		t.Fatalf("空区间应给空数组，实际 %v", got)
	}
	if got := r.arr(t, "distribution"); len(got) != 0 {
		t.Fatalf("空区间分布应为空数组，实际 %v", got)
	}
	idx := r.fields["indices"].(map[string]any)
	hrv := idx["hrv"].(map[string]any)
	if hrv["score"] != nil {
		t.Fatalf("无数据时 HRV 均值应为 null，实际 %v", hrv["score"])
	}
	if hrv["basis"] != "暂无有效测量" {
		t.Fatalf("basis 应说明暂无数据，实际 %v", hrv["basis"])
	}
	cmp := r.fields["comparison"].(map[string]any)
	if cmp["previous"] != nil || cmp["delta"] != nil {
		t.Fatalf("无上一窗口时对比应为 null，实际 %v", cmp)
	}
}

func TestECGAnalysisRejectsBadDate(t *testing.T) {
	h, _ := newRouter(t, "")
	get(t, h, "/api/ecg/analysis?from=2026-13-99&to=2026-10-31").wantErr(t, 400)
}
