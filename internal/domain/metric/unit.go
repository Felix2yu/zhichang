package metric

import "strings"

// unitFactors 是单位换算表：value(目标单位) = value(源单位) × factor。
//
// 为什么必须有这一层：HAE 会跟着 iOS 区域与版本换单位（活动能量从 kcal 换成 kJ、
// 距离从 km 换成 mi 等），而字典里的 Unit 是聚合与展示的唯一口径。
// 若不在入库时换算，值会原样进库却按字典单位展示——1091 kJ 会被标成「1091 kcal」，
// 虚高 4.18 倍，而且界面上完全看不出错。
//
// 这里只登记「数值等价」的换算。单位写法不同但数值相同的情况（count/min 与 bpm、
// ml/(kg·min) 与 mL/min·kg、count 与 次）不属于换算，是标签差异，改写了反而篡改原文。
var unitFactors = map[[2]string]float64{
	{"kj", "kcal"}: 1 / 4.184,
	{"kcal", "kj"}: 4.184,

	{"m", "km"}:  0.001,
	{"km", "m"}:  1000,
	{"mi", "km"}: 1.609344,
	{"km", "mi"}: 1 / 1.609344,

	{"lb", "kg"}: 0.45359237,
	{"kg", "lb"}: 1 / 0.45359237,
}

// ConvertUnit 把 value 从 from 单位换算到 to 单位，并返回应当随值一起记录的单位名。
//
// 三种结果：
//   - 两单位忽略大小写与首尾空白后相同：原值返回，单位取 to（统一写法，如 ML → mL）；
//   - 命中换算表：按因子换算后返回，单位取 to；
//   - 其余（未知单位、不可换算的组合、to 为空）：原值与原单位原样返回。
//     未知单位绝不猜——宁可显示成上游原样，也不能把一个没把握的系数乘上去。
func ConvertUnit(value float64, from, to string) (float64, string) {
	f, t := normUnit(from), normUnit(to)
	if t == "" {
		return value, from
	}
	if f == t {
		return value, to
	}
	if factor, ok := unitFactors[[2]string{f, t}]; ok {
		return value * factor, to
	}
	return value, from
}

// normUnit 归一化单位写法，仅用于查表与比较，不用于展示。
func normUnit(u string) string {
	return strings.ToLower(strings.TrimSpace(u))
}
