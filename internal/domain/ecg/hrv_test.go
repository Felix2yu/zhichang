package ecg

import (
	"math"
	"math/rand"
	"testing"
)

// synthECG 生成合成的单导联心电波形：每个 RR 间期一个 QRS 主波 + 一个 T 波，
// 叠加基线漂移与轻微噪声。RR 序列即事实来源，测试直接对照它。
//
// T 波放在 R 后 300ms（越过 250ms 不应期）、幅度 0.35：这是最难缠的一类
// 误检源，检测链必须靠阈值或 outlier 过滤把它排除掉。
func synthECG(fs float64, rrSec []float64, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	// 前置 0.3s 基线：微分器要看前 4k 个点，第一个 QRS 若贴在信号头上会被吃掉
	pre := int(0.3 * fs)
	out := make([]float64, 0, pre+len(rrSec)*int(fs))
	for i := 0; i < pre; i++ {
		out = append(out, rng.NormFloat64()*0.01)
	}
	for _, rr := range rrSec {
		beatLen := int(rr * fs)
		for j := 0; j < beatLen; j++ {
			t := float64(j) / fs
			// QRS：σ=35ms 的尖峰（足够宽，过 5–12Hz 带通仍有尖峰）
			qrs := 1.0 * math.Exp(-math.Pow(t-0.012, 2)/(2*0.035*0.035))
			// T 波：σ=60ms，R 后 300ms（越过 250ms 不应期，专测误检排除）
			tw := 0.35 * math.Exp(-math.Pow(t-0.30, 2)/(2*0.06*0.06))
			v := qrs + tw
			// 基线漂移：0.3Hz，幅度 0.15
			tt := float64(len(out)) / fs
			v += 0.15 * math.Sin(2*math.Pi*0.3*tt)
			v += rng.NormFloat64() * 0.01
			out = append(out, v)
		}
	}
	return out
}

// beatPositions 返回合成波形里每个 QRS 尖峰的真实下标（与 synthECG 同布局：
// 前置 0.3s 基线，随后每个 RR 条目一个心动周期，尖峰在周期内 12ms 处）。
func beatPositions(fs float64, rrSec []float64) []int {
	pos := []int{int(0.3*fs) + int(0.012*fs)}
	for _, rr := range rrSec {
		pos = append(pos, pos[len(pos)-1]+int(rr*fs))
	}
	return pos[:len(rrSec)]
}

func TestDetectRPeaksRegularRhythm(t *testing.T) {
	fs := 512.0
	rr := make([]float64, 36) // 36 个心动周期 = 36 次心跳
	for i := range rr {
		rr[i] = 0.8 // 75bpm
	}
	sig := synthECG(fs, rr, 42)

	got := DetectRPeaks(sig, fs)
	want := beatPositions(fs, rr)
	if len(got.Peaks) != len(want) {
		t.Fatalf("规则心律应检出 %d 个峰，实际 %d（拒绝 %d）", len(want), len(got.Peaks), got.Rejected)
	}
	// 精修后峰位应落在真实 R 尖 ±15ms 内
	for i, w := range want {
		if d := float64(got.Peaks[i]-w) / fs * 1000; math.Abs(d) > 15 {
			t.Fatalf("第 %d 个峰偏离 %.1fms（want=%d got=%d）", i, d, w, got.Peaks[i])
		}
	}
	if len(got.RR) != len(got.Peaks)-1 {
		t.Fatalf("RR 数应与峰数差一，实际 %d vs %d", len(got.RR), len(got.Peaks))
	}
}

func TestDetectRPeaksVariableRhythm(t *testing.T) {
	fs := 512.0
	rr := []float64{}
	for i := 0; i < 24; i++ {
		if i%2 == 0 {
			rr = append(rr, 0.70) // 呼吸性心律不齐的粗略模拟
		} else {
			rr = append(rr, 0.90)
		}
	}
	sig := synthECG(fs, rr, 7)

	got := DetectRPeaks(sig, fs)
	want := beatPositions(fs, rr)
	if len(got.Peaks) != len(want) {
		t.Fatalf("变异心律应检出 %d 个峰，实际 %d（拒绝 %d）", len(want), len(got.Peaks), got.Rejected)
	}
	for i, w := range want {
		if d := float64(got.Peaks[i]-w) / fs * 1000; math.Abs(d) > 15 {
			t.Fatalf("第 %d 个峰偏离 %.1fms", i, d)
		}
	}
	h := ComputeHRV(got.RR)
	// 事实来源：RR 均值 (0.7+0.9)/2 = 0.8s，总体标准差 0.1s
	if math.Abs(h.MeanRR-800) > 15 {
		t.Fatalf("平均 RR 应约 800ms，实际 %.1f", h.MeanRR)
	}
	if math.Abs(h.SDNN-100) > 20 {
		t.Fatalf("SDNN 应约 100ms，实际 %.1f", h.SDNN)
	}
	// RMSSD：相邻差恒为 0.2s → 200ms
	if math.Abs(h.RMSSD-200) > 20 {
		t.Fatalf("RMSSD 应约 200ms，实际 %.1f", h.RMSSD)
	}
	if h.PNN50 < 99 {
		t.Fatalf("相邻差全 >50ms，pNN50 应约 100，实际 %.1f", h.PNN50)
	}
	if math.Abs(h.MeanHR-75) > 2 {
		t.Fatalf("平均心率应约 75bpm，实际 %.1f", h.MeanHR)
	}
}

func TestDetectRPeaksHandlesDegenerateInput(t *testing.T) {
	fs := 512.0
	cases := map[string][]float64{
		"空":   {},
		"太短":  make([]float64, 10),
		"全零":  make([]float64, 2000),
		"纯噪声": nil,
		"单峰":  nil,
	}
	// 纯噪声：固定种子，验证不 panic 且峰数合理（不会把噪声全当成心跳）
	rng := rand.New(rand.NewSource(1))
	noise := make([]float64, 20000)
	for i := range noise {
		noise[i] = rng.NormFloat64()
	}
	cases["纯噪声"] = noise
	// 单峰：只有一个孤立尖峰
	single := make([]float64, 20000)
	for i := 1000; i < 1100; i++ {
		single[i] = 1
	}
	cases["单峰"] = single

	for name, sig := range cases {
		got := DetectRPeaks(sig, fs)
		if name == "全零" || name == "空" || name == "太短" {
			if len(got.Peaks) != 0 {
				t.Fatalf("%s 不应检出峰，实际 %d", name, len(got.Peaks))
			}
			continue
		}
		// 噪声/单峰：不 panic 即可，峰数不应离谱（单峰给不出成对 RR）
		if len(got.Peaks) > 40 {
			t.Fatalf("%s 的峰数离谱：%d", name, len(got.Peaks))
		}
		h := ComputeHRV(got.RR)
		if len(got.RR) > 0 && h.MeanHR <= 0 {
			t.Fatalf("%s 的 MeanHR 应 >0", name)
		}
	}
}

func TestStressIndex(t *testing.T) {
	// 心跳太少：直方图无意义
	if _, ok := StressIndex(repeatN(0.8, 11)); ok {
		t.Fatal("11 个 RR 不该给出压力指数")
	}
	// 完全等长：MxDMn=0，指数无定义
	if _, ok := StressIndex(repeatN(0.8, 30)); ok {
		t.Fatal("等长 RR 的压力指数应判无定义")
	}
	// 中等变异：应给出有限正值
	rr := []float64{}
	for i := 0; i < 30; i++ {
		if i%3 == 0 {
			rr = append(rr, 0.72)
		} else if i%3 == 1 {
			rr = append(rr, 0.80)
		} else {
			rr = append(rr, 0.86)
		}
	}
	si, ok := StressIndex(rr)
	if !ok || si <= 0 || math.IsInf(si, 0) || math.IsNaN(si) {
		t.Fatalf("中等变异应给出正常压力指数，实际 si=%v ok=%v", si, ok)
	}
}

func repeatN(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestScoreAnchors(t *testing.T) {
	// HRV：锚点与截断
	if ScoreHRV(10) != 0 || ScoreHRV(90) != 100 {
		t.Fatalf("HRV 锚点错误: %d %d", ScoreHRV(10), ScoreHRV(90))
	}
	if ScoreHRV(5) != 0 || ScoreHRV(200) != 100 {
		t.Fatalf("HRV 应截断到 [0,100]: %d %d", ScoreHRV(5), ScoreHRV(200))
	}
	if ScoreHRV(50) != 50 {
		t.Fatalf("HRV 中点应 50，实际 %d", ScoreHRV(50))
	}
	// 压力：锚点
	if ScoreStress(40) != 0 || ScoreStress(400) != 100 {
		t.Fatalf("压力锚点错误: %d %d", ScoreStress(40), ScoreStress(400))
	}
	if ScoreStress(120) != 30 || ScoreStress(250) != 65 {
		t.Fatalf("压力中间锚点错误: %d %d", ScoreStress(120), ScoreStress(250))
	}
	// 疲劳：单调性与范围
	calm := ScoreFatigue(90, 10, 55)
	worn := ScoreFatigue(20, 90, 100)
	if calm >= worn {
		t.Fatalf("疲劳指数应随恢复变差而升高: %d vs %d", calm, worn)
	}
	if calm < 0 || worn > 100 {
		t.Fatalf("疲劳指数越界: %d %d", calm, worn)
	}
}

func TestLevels(t *testing.T) {
	if HRVLevel(10) != "偏低" || HRVLevel(45) != "一般" || HRVLevel(65) != "良好" || HRVLevel(90) != "优秀" {
		t.Fatal("HRV 档位错误")
	}
	if IndexLevel(10) != "低" || IndexLevel(45) != "中" || IndexLevel(65) != "偏高" || IndexLevel(90) != "高" {
		t.Fatal("指数档位错误")
	}
	if RiskLevel(10) != "低" || RiskLevel(30) != "需关注" || RiskLevel(60) != "偏高" || RiskLevel(90) != "高" {
		t.Fatal("风险档位错误")
	}
}

func TestRiskScore(t *testing.T) {
	// 全窦性 + HRV 良好 → 低
	score, drivers := RiskScore(RiskInput{Total: 100, HRVAvg: intp(70)})
	if score >= 25 {
		t.Fatalf("全窦性且 HRV 良好应判低关注度，实际 %d（%v）", score, drivers)
	}
	if len(drivers) != 1 || drivers[0] != "区间内未发现异常提示" {
		t.Fatalf("无异常时 drivers 应如实说明，实际 %v", drivers)
	}
	// 一次房颤 → 立即拉到 55 以上
	score, drivers = RiskScore(RiskInput{Total: 100, AFib: 1})
	if score < 55 {
		t.Fatalf("一次房颤提示应显著抬升关注度，实际 %d", score)
	}
	if drivers[0] != "1 次测量提示房颤" {
		t.Fatalf("房颤应进入触发项，实际 %v", drivers)
	}
	// 房颤占比过半 → 逼近上限（55 + 35×0.6 = 76，已入「偏高」档）
	score, _ = RiskScore(RiskInput{Total: 10, AFib: 6})
	if score < 75 {
		t.Fatalf("多次房颤应接近上限，实际 %d", score)
	}
	// HRV 偏低单独也能加分，但不超过 12
	score, drivers = RiskScore(RiskInput{Total: 50, HRVAvg: intp(20)})
	if score != 12 {
		t.Fatalf("仅 HRV 偏低应加 12 分，实际 %d（%v）", score, drivers)
	}
	// 空区间
	if s, _ := RiskScore(RiskInput{}); s != 0 {
		t.Fatalf("空区间应给 0，实际 %d", s)
	}
}

func intp(v int) *int { return &v }
