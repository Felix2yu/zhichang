package ecg

// 心脏疾病风险关注度：把区间内的判定分布与 HRV 汇总成一个 0-100 的粗筛分数。
//
// 定位必须讲清楚：这是「关注度」而不是「患病概率」。Apple 心电测量每次
// 只有约 30 秒，间歇性房颤很可能在整个区间里一次都没被抓到；反过来，
// 一次「记录欠佳」也未必代表心脏有问题。它回答的问题是
// 「这些测量加起来，有没有值得注意的信号」，而不是「你有多大概率生病」。
//
// 规则全部透明（Drivers 逐条列出触发项，前端原样展示），权重是经验值：
// 判定异常来自设备官方算法，权重最高；HRV 偏低是辅助信号，权重最低。

import "strconv"

// RiskInput 是区间统计的输入。Ratio 一律由调用方算好，这里只做加权。
type RiskInput struct {
	Total        int  // 区间内测量总次数
	AFib         int  // 房颤提示次数
	HighHR       int  // 高心率次数
	Undetermined int  // 无法判定 + 记录欠佳次数
	HRVAvg       *int // 区间平均心率变异性指数（无有效波形时为 nil）
}

// RiskScore 返回 0-100 的关注度分数与触发项说明。
func RiskScore(in RiskInput) (int, []string) {
	if in.Total <= 0 {
		return 0, nil
	}
	score := 0.0
	var drivers []string
	ratio := func(n int) float64 { return float64(n) / float64(in.Total) }

	if in.AFib > 0 {
		// 55 起步：任何一次房颤提示都值得认真对待，占比越高越靠前。
		score += 55 + 35*ratio(in.AFib)
		drivers = append(drivers, plural(in.AFib, "次测量提示房颤"))
	}
	if in.HighHR > 0 {
		score += 12 * ratio(in.HighHR)
		drivers = append(drivers, plural(in.HighHR, "次测量心率过快"))
	}
	if in.Undetermined > 0 {
		// 权重低：测不出结论多半是采集问题，不是心脏问题。
		score += 8 * ratio(in.Undetermined)
		drivers = append(drivers, plural(in.Undetermined, "次测量无法判定或记录欠佳"))
	}
	if in.HRVAvg != nil {
		switch {
		case *in.HRVAvg < 30:
			score += 12
			drivers = append(drivers, "心率变异性指数均值偏低")
		case *in.HRVAvg < 50:
			score += 6
		}
	}
	if score > 100 {
		score = 100
	}
	if len(drivers) == 0 {
		drivers = append(drivers, "区间内未发现异常提示")
	}
	return int(score), drivers
}

// RiskLevel 关注度档位。
func RiskLevel(score int) string {
	switch {
	case score < 25:
		return "低"
	case score < 50:
		return "需关注"
	case score < 75:
		return "偏高"
	default:
		return "高"
	}
}

func plural(n int, unit string) string {
	return strconv.Itoa(n) + " " + unit
}
