// 波形分析：把一条已入库的电压波形变成普通用户看得懂的指标。
//
// 链条是 R 峰检测 → RR 间期 → 时域 HRV → 四个 0-100 的可解释指数。
// 每一步都刻意保持透明：前端会把「含义 / 怎么算 / 可信度」原样讲给用户，
// 所以这里不允许出现说不清来源的魔法系数——所有锚点与阈值都注明依据。
//
// 为什么不用现成库：本项目是 Go 单体，而成熟的心电/HRV 库几乎都在 Python
// （neurokit2、WFDB、pyHRV，见 docs/ecg-analysis.md 的评估）。为一个
// 自托管单用户应用引入 Python 运行时不划算；Pan-Tompkins 这类经典算法
// 用纯 Go 实现约三百行，依赖为零、隐私最好、单条 30 秒波形亚毫秒级。
// 代价是只做时域指标（30 秒单导联数据也本就做不了可靠的频域分析）。
package ecg

import (
	"math"
	"sort"
)

// DetectResult 是一次 R 峰检测的结果。
type DetectResult struct {
	// Peaks 是采用的 R 峰在波形里的下标（升序）。
	Peaks []int
	// RR 是与 Peaks 相邻配对的 RR 间期（秒），长度 = len(Peaks)-1。
	RR []float64
	// Rejected 是被 outlier 过滤剔掉的可疑峰数（漏检/误检的规模，
	// 用于评估信号质量，不展示给用户）。
	Rejected int
}

// DetectRPeaks 在电压波形上检测 R 峰（Pan-Tompkins 风格，适配任意采样率）。
//
// 阶段与经典实现一致：带通 → 微分 → 平方 → 移动窗积分 → 自适应阈值 → 峰检出，
// 最后加一道 RR 间期 outlier 过滤。两处针对本项目的改编：
//
//   - 带通用滑动平均近似 5–12 Hz 带通，而不是 Pan-Tompkins 论文里
//     为 200 Hz 手调的 IIR 系数——上游采样率是 512 Hz，直接套 200 Hz 的
//     系数等于把截止频率整体放大 2.5 倍。滑动平均的第一零点在 fs/N，
//     低通取 N≈fs/25（-3dB 约 11 Hz）、高通减去 N≈fs/5 的均值（零点 5 Hz），
//     两条都做中心对齐，不引入群延迟——峰位整体平移会让所有 RR 一起错。
//   - 阈值用分位数初始化后做 PT 式的双阈值学习，再接 outlier 过滤。
//     T 波、肌颤误检会产生过短 RR，漏检会产生过长 RR，两者都落在
//     [0.62, 1.8]×中位 RR 之外，这一道兜底比单纯调阈值更稳。
//
// 输入不合法（空波形、点数不足以覆盖一个分析窗）时返回空结果，绝不 panic。
func DetectRPeaks(voltages []float64, fs float64) DetectResult {
	var out DetectResult
	n := len(voltages)
	if n < 64 || fs <= 0 {
		return out
	}

	// —— 阶段 1：带通 ——
	bp := bandpass(voltages, fs)

	// —— 阶段 2：微分（Pan-Tompkins 五点式，跨度按采样率折算到约 20ms）——
	k := int(math.Round(fs / 200))
	if k < 1 {
		k = 1
	}
	d := make([]float64, n)
	norm := 1.0 / (2 * float64(k))
	for i := 4 * k; i < n; i++ {
		d[i] = norm * (2*bp[i] + bp[i-k] - bp[i-3*k] - 2*bp[i-4*k])
	}

	// —— 阶段 3+4：平方后做 150ms 移动窗积分 ——
	for i := range d {
		d[i] *= d[i]
	}
	integ := causalMA(d, int(math.Round(0.15*fs)))

	// —— 阶段 5：候选峰（局部极大 + 250ms 不应期）——
	cand := candidates(integ, fs)
	if len(cand) < 2 {
		return out
	}

	// 阈值取两者较大：98 分位的 35%，以及积分波形中位数的 3 倍。
	// 后者是抗噪底线——纯噪声的 98 分位只有中位数的约 2.7 倍，
	// 单看 35% 这条会把噪声峰当心跳；真实 QRS 的积分值远高于底噪的 3 倍，
	// 所以这条底线对干净波形无感，对坏记录直接「测不出」。
	thr := math.Max(0.35*percentile98(integ), 3*median(integ))
	if thr <= 0 {
		// 平直信号：积分后没有任何起伏，无可检测内容
		return out
	}
	peaks := make([]int, 0, len(cand))
	for _, c := range cand {
		if integ[c] >= thr {
			peaks = append(peaks, c)
		}
	}
	if len(peaks) < 2 {
		return out
	}

	// 补检：同一条记录里 R 幅度可能相差数倍（实测 Apple 单导联最强峰约为
	// 最弱峰的 5 倍，电极接触不稳时尤甚），固定阈值会整批漏掉弱侧峰，
	// 表现为「心率减半、HRV 虚高」。只在 >1.6×中位 RR 的间隙内以更低阈值
	// 找局部极大——是 targeted searchback，噪声峰没有可乘之机。
	peaks = fillMissed(peaks, integ, fs, math.Max(0.12*percentile98(integ), 1.5*median(integ)))

	// 峰位精修：积分窗是因果的，检测点系统性落后真实 R 峰约半个窗
	// （150ms 窗 ≈ 75ms）。RR 间距不受影响（所有峰平移量相同），
	// 但波形图上给用户标红点时必须落在真正的 R 尖上。
	// 在带通信号上回找附近的最大 deflection——带通是中心对齐的，不引入平移。
	peaks = refinePeaks(peaks, bp, fs)
	if len(peaks) < 2 {
		return out
	}

	// —— 阶段 6：RR 间期 outlier 过滤（迭代到稳定）——
	rr := make([]float64, len(peaks)-1)
	for i := 1; i < len(peaks); i++ {
		rr[i-1] = float64(peaks[i]-peaks[i-1]) / fs
	}
	keep := make([]bool, len(rr))
	for i := range keep {
		keep[i] = true
	}
	for round := 0; round < 4; round++ {
		live := make([]float64, 0, len(rr))
		for i, ok := range keep {
			if ok {
				live = append(live, rr[i])
			}
		}
		if len(live) < 2 {
			break
		}
		med := median(live)
		changed := false
		for i, ok := range keep {
			if !ok {
				continue
			}
			// 0.62×中位 ≈ 160bpm 的下限之外视为过短（误检）；
			// 1.8×中位 ≈ 33bpm 之外视为过长（漏检）。
			if rr[i] < 0.62*med || rr[i] > 1.8*med {
				keep[i] = false
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	// 采用集重建：一条 RR 存活，才谈得上两端的峰都是真峰。
	// 取所有存活 RR 的端点并集——这比「按峰逐个判断」干净：
	// 误检峰会制造一条过短 RR 把自己标出来，漏检会留下一条过长 RR，
	// 两种脏数据只污染自己那条间期，不拖累相邻的真间期。
	valid := make(map[int]bool, len(peaks))
	for i, ok := range keep {
		if !ok {
			continue
		}
		valid[peaks[i]] = true
		valid[peaks[i+1]] = true
	}
	out.Peaks = make([]int, 0, len(valid))
	for p := range valid {
		out.Peaks = append(out.Peaks, p)
	}
	sort.Ints(out.Peaks)
	for i, ok := range keep {
		if ok {
			out.RR = append(out.RR, rr[i])
		} else {
			out.Rejected++
		}
	}
	return out
}

// fillMissed 在过长的 RR 间隙里补检被漏掉的弱峰。
//
// 只做「间隙内找最高局部极大」这一件事：阈值降到 thrLow，但搜索范围被
// 限制在 >1.6×中位 RR 的间隙内部（两端各留 100ms 不应期），
// 所以它补的是「明显该有心跳却没有」的位置，不会无差别放低阈值。
// 一个间隙里可能漏了不止一拍，故迭代到没有新插入为止。
func fillMissed(peaks []int, integ []float64, fs float64, thrLow float64) []int {
	if len(peaks) < 2 || thrLow <= 0 {
		return peaks
	}
	out := append([]int{}, peaks...)
	for round := 0; round < 3; round++ {
		rr := rrSamples(out, fs)
		if len(rr) < 2 {
			break
		}
		med := median(rr)
		guard := int(0.1 * fs)
		var insertions []int
		for i := 0; i+1 < len(out); i++ {
			if float64(out[i+1]-out[i]) < 1.6*med {
				continue
			}
			lo, hi := out[i]+guard, out[i+1]-guard
			best, bestV := -1, 0.0
			for j := lo; j < hi; j++ {
				if j <= 0 || j+1 >= len(integ) {
					continue
				}
				if integ[j] < thrLow || integ[j] < integ[j-1] || integ[j] < integ[j+1] {
					continue
				}
				if integ[j] > bestV {
					best, bestV = j, integ[j]
				}
			}
			if best > 0 {
				insertions = append(insertions, best)
			}
		}
		if len(insertions) == 0 {
			break
		}
		sort.Ints(insertions)
		merged := make([]int, 0, len(out)+len(insertions))
		next := 0
		for _, p := range out {
			for next < len(insertions) && insertions[next] < p {
				merged = append(merged, insertions[next])
				next++
			}
			merged = append(merged, p)
		}
		for ; next < len(insertions); next++ {
			merged = append(merged, insertions[next])
		}
		out = merged
	}
	return out
}

// rrSamples 返回相邻峰的采样点间距（用于与中位数比较，避免换算毫秒再除回去）。
func rrSamples(peaks []int, fs float64) []float64 {
	_ = fs
	out := make([]float64, 0, len(peaks))
	for i := 1; i < len(peaks); i++ {
		out = append(out, float64(peaks[i]-peaks[i-1]))
	}
	return out
}

// refinePeaks 把积分波形的检测点精修到带通信号的真实 R 峰位置。
// 搜索窗取检测点前 180ms 到后 50ms：积分峰落后真实 R 约半个积分窗，
// 向前找 |bp| 的最大值即可。取绝对值而不是原值：万一导联接反
// （R 朝下），精修同样有效。
func refinePeaks(peaks []int, bp []float64, fs float64) []int {
	back := int(0.18 * fs)
	fwd := int(0.05 * fs)
	out := make([]int, 0, len(peaks))
	last := -1
	for _, p := range peaks {
		lo, hi := p-back, p+fwd
		if lo < 0 {
			lo = 0
		}
		if hi > len(bp) {
			hi = len(bp)
		}
		best, bestAbs := p, -1.0
		for i := lo; i < hi; i++ {
			if a := math.Abs(bp[i]); a > bestAbs {
				best, bestAbs = i, a
			}
		}
		// 精修可能把两个相邻检测点并到同一个尖峰上，保持 100ms 间隔
		if last >= 0 && best-last < int(0.1*fs) {
			continue
		}
		out = append(out, best)
		last = best
	}
	return out
}

// bandpass 用两条中心对齐的滑动平均近似 5–12 Hz 带通。
func bandpass(x []float64, fs float64) []float64 {
	lpN := int(math.Round(fs / 25))
	if lpN < 1 {
		lpN = 1
	}
	hpN := int(math.Round(fs / 5))
	if hpN < 2 {
		hpN = 2
	}
	lp := centeredMA(x, lpN)
	hp := centeredMA(lp, hpN)
	out := make([]float64, len(x))
	for i := range out {
		out[i] = lp[i] - hp[i]
	}
	return out
}

// centeredMA 中心对齐滑动平均：窗口越界时收缩，避免边缘补零制造假峰。
func centeredMA(x []float64, w int) []float64 {
	n := len(x)
	out := make([]float64, n)
	if w <= 1 {
		copy(out, x)
		return out
	}
	pre := make([]float64, n+1)
	for i, v := range x {
		pre[i+1] = pre[i] + v
	}
	half := w / 2
	for i := 0; i < n; i++ {
		lo, hi := i-half, i+w-half
		if lo < 0 {
			lo = 0
		}
		if hi > n {
			hi = n
		}
		out[i] = (pre[hi] - pre[lo]) / float64(hi-lo)
	}
	return out
}

// causalMA 因果滑动平均（积分窗用）：整体延迟半个窗，对所有峰一视同仁，
// 不改变 RR 间距。
func causalMA(x []float64, w int) []float64 {
	n := len(x)
	out := make([]float64, n)
	if w <= 1 {
		copy(out, x)
		return out
	}
	pre := make([]float64, n+1)
	for i, v := range x {
		pre[i+1] = pre[i] + v
	}
	for i := 0; i < n; i++ {
		lo := i + 1 - w
		if lo < 0 {
			lo = 0
		}
		out[i] = (pre[i+1] - pre[lo]) / float64(i+1-lo)
	}
	return out
}

// candidates 取积分波形的局部极大，并按 250ms 不应期归并
// （生理上限约 240bpm，更近的「峰」只能是同一次心跳的余波）。
func candidates(w []float64, fs float64) []int {
	n := len(w)
	refractory := int(math.Round(0.25 * fs))
	if refractory < 1 {
		refractory = 1
	}
	var out []int
	for i := 1; i < n-1; i++ {
		if w[i] < w[i-1] || w[i] < w[i+1] {
			continue
		}
		if len(out) > 0 && i-out[len(out)-1] < refractory {
			// 不应期内保留更高的那个
			if w[i] > w[out[len(out)-1]] {
				out[len(out)-1] = i
			}
			continue
		}
		out = append(out, i)
	}
	return out
}

// percentile98 取 98 分位（升序拷贝后定位，不做全排序也不改输入）。
func percentile98(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := make([]float64, len(x))
	copy(s, x)
	sort.Float64s(s)
	idx := int(math.Ceil(0.98*float64(len(s)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

func median(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := make([]float64, len(x))
	copy(s, x)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

// HRV 是一次测量的时域心率变异性指标。时间单位毫秒。
//
// 只做时域：30 秒单导联记录做频域分析（LF/HF）方差极大，
// 算出来反而是噪音，不如不算（详见 docs/ecg-analysis.md）。
type HRV struct {
	Beats  int     // 心跳数（= 间期数 + 1）
	MeanRR float64 // 平均 RR（ms）
	SDNN   float64 // RR 标准差（ms）
	RMSSD  float64 // 相邻 RR 差值的均方根（ms）
	PNN50  float64 // 相邻 RR 差值 >50ms 的占比（%）
	MeanHR float64 // 平均心率（bpm，= 60/MeanRR）
	MinHR  float64 // 最慢心率（bpm）
	MaxHR  float64 // 最快心率（bpm）
	CV     float64 // 变异系数 SDNN/MeanRR，衡量节律整齐度
}

// ComputeHRV 由 RR 间期（秒）计算时域 HRV。间期不足 1 个时返回零值。
//
// 入参是「已经过 outlier 过滤的间期」而不是峰列：误检/漏检的脏峰
// 只应通过那条坏间期被剔除，若在这里从峰重算，等于把过滤结果作废。
func ComputeHRV(rr []float64) HRV {
	var h HRV
	if len(rr) == 0 {
		return h
	}
	ms := make([]float64, len(rr))
	for i, v := range rr {
		ms[i] = v * 1000
	}
	h.Beats = len(rr) + 1
	var sum, sumSq float64
	for _, v := range ms {
		sum += v
		sumSq += v * v
	}
	h.MeanRR = sum / float64(len(ms))
	if h.MeanRR > 0 {
		h.MeanHR = 60000 / h.MeanRR
		h.MinHR = 60000 / maxRR(ms)
		h.MaxHR = 60000 / minRR(ms)
		variance := sumSq/float64(len(ms)) - h.MeanRR*h.MeanRR
		if variance < 0 {
			variance = 0 // 浮点误差可能压出极小的负值
		}
		h.SDNN = math.Sqrt(variance)
		h.CV = h.SDNN / h.MeanRR
	}
	// RMSSD 与 pNN50 只看相邻差值
	var sq float64
	var over int
	for i := 1; i < len(ms); i++ {
		d := ms[i] - ms[i-1]
		if d < 0 {
			d = -d
		}
		sq += d * d
		if d > 50 {
			over++
		}
	}
	if len(ms) > 1 {
		h.RMSSD = math.Sqrt(sq / float64(len(ms)-1))
		h.PNN50 = float64(over) / float64(len(ms)-1) * 100
	}
	return h
}

func maxRR(rr []float64) float64 {
	m := rr[0]
	for _, v := range rr[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func minRR(rr []float64) float64 {
	m := rr[0]
	for _, v := range rr[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// StressIndex 计算 Baevsky 自主神经压力指数 SI = AMo / (2·Mo·MxDMn)。
//
//   - Mo    ：RR 直方图（bin 宽 50ms）的众数 bin 中心，单位秒；
//   - AMo   ：众数 bin 里的 RR 占比（%）；
//   - MxDMn ：最长与最短 RR 之差，单位秒。
//
// 心跳数不足 12 个、或 RR 几乎无差异（MxDMn<50ms）时直方图没有意义，
// 返回 ok=false——调用方应显示「数据不足」而不是编一个数。
func StressIndex(rr []float64) (si float64, ok bool) {
	if len(rr) < 12 {
		return 0, false
	}
	const binW = 0.05
	minV, maxV := rr[0], rr[0]
	for _, v := range rr[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	mxdm := maxV - minV
	if mxdm < binW {
		return 0, false
	}
	bins := make(map[int]int)
	for _, v := range rr {
		bins[int(math.Floor((v-minV)/binW))]++
	}
	modeBin, modeCount := 0, 0
	for b, c := range bins {
		if c > modeCount || (c == modeCount && b < modeBin) {
			modeBin, modeCount = b, c
		}
	}
	mo := minV + (float64(modeBin)+0.5)*binW
	amo := float64(modeCount) / float64(len(rr)) * 100
	if mo <= 0 {
		return 0, false
	}
	return amo / (2 * mo * mxdm), true
}

// 下面四个函数把生理量映射到 0-100 的展示指数。映射一律用锚点线性插值，
// 锚点都有出处；指数只为「跨指标可比 + 一眼看懂」，不是临床量表。

// ScoreHRV 把 RMSSD（毫秒）映射为 0-100 的心率变异性指数。
// 锚点 10ms→0、90ms→100：30 秒短程测量的 RMSSD 常见 15–60ms，
// 长期低于 10ms 提示自主神经调节明显减弱，80ms 以上多见于规律锻炼者。
// 注意：30 秒值与 24 小时长程值不可直接比较，趋势比绝对值更有意义。
func ScoreHRV(rmssdMs float64) int {
	return scaleAnchors(rmssdMs, [][2]float64{{10, 0}, {90, 100}})
}

// ScoreStress 把 Baevsky SI 映射为 0-100 的精神压力指数。
// 锚点 40→0（深度放松）、120→30、250→65、400→100（高度应激）：
// 静息成人 SI 多在 50–150，150–300 为过渡区，持续高于 300 提示交感占优。
func ScoreStress(si float64) int {
	return scaleAnchors(si, [][2]float64{{40, 0}, {120, 30}, {250, 65}, {400, 100}})
}

// ScoreFatigue 疲劳指数 = 0.45·(100-HRV) + 0.30·压力 + 0.25·心率负荷。
// 心率负荷锚点 55bpm→0、100bpm→100（静息心率越高，恢复负担越重）。
//
// 这是没有临床金标准的经验复合指标：HRV 低、压力高、静息心率高三者
// 都指向「恢复不足」，加权求和只为给出一个可追踪的单值。
// 只看个人趋势，不看绝对值，更不与别人比。
func ScoreFatigue(hrvScore, stressScore int, meanHR float64) int {
	hrLoad := scaleAnchors(meanHR, [][2]float64{{55, 0}, {100, 100}})
	v := 0.45*float64(100-clampScore(hrvScore)) +
		0.30*float64(clampScore(stressScore)) +
		0.25*float64(hrLoad)
	return clampScore(int(math.Round(v)))
}

// scaleAnchors 在锚点间做线性插值并夹到 [0,100]；首锚之前与末锚之后走水平外推。
func scaleAnchors(v float64, anchors [][2]float64) int {
	if len(anchors) == 0 {
		return 0
	}
	if v <= anchors[0][0] {
		return clampScore(int(math.Round(anchors[0][1])))
	}
	last := anchors[len(anchors)-1]
	if v >= last[0] {
		return clampScore(int(math.Round(last[1])))
	}
	for i := 1; i < len(anchors); i++ {
		a, b := anchors[i-1], anchors[i]
		if v <= b[0] {
			t := (v - a[0]) / (b[0] - a[0])
			return clampScore(int(math.Round(a[1] + t*(b[1]-a[1]))))
		}
	}
	return clampScore(int(math.Round(last[1])))
}

func clampScore(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// 档位文案。放在领域层而不是前端：口径属于指标语义的一部分，
// 前后端各写一份必然漂移。

// HRVLevel 心率变异性指数档位（越高越好）。
func HRVLevel(score int) string {
	switch {
	case score < 30:
		return "偏低"
	case score < 60:
		return "一般"
	case score < 80:
		return "良好"
	default:
		return "优秀"
	}
}

// IndexLevel 压力/疲劳类指数档位（越低越好）。
func IndexLevel(score int) string {
	switch {
	case score < 30:
		return "低"
	case score < 60:
		return "中"
	case score < 80:
		return "偏高"
	default:
		return "高"
	}
}
