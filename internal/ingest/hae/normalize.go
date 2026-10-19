package hae

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/ecg"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/domain/tz"
)

// Result 是归一化结果。
type Result struct {
	Samples  []sample.Sample
	Workouts []sample.Workout
	Sleep    []sample.SleepSession
	ECG      []sample.ECGRecord
	// Unknown 记录未在指标字典中登记的指标名（去重后）。
	Unknown []string
	// Skipped 因缺值/时间不可解析而丢弃的记录数。
	Skipped int
}

// Normalize 把 HAE 导出转换为内部模型。
// 时区口径：local_date 一律由记录自带偏移推导；缺失时回落到 home。
func Normalize(p *Payload, reg *metric.Registry, home *time.Location) Result {
	var res Result
	unknown := make(map[string]struct{})

	for _, m := range p.Data.Metrics {
		// 先收敛到字典的规范名再往下走：样本、同步水位、聚合队列三处都用这个名字。
		// 不收敛的话同一概念会分裂成两个 metric——字典里的卡片永远空图，
		// 真实数据堆在另一个名字下（血氧就是这样：导出叫 blood_oxygen_saturation，
		// 字典叫 oxygen_saturation）。
		name := metric.Canonical(m.Name)
		if name == "" {
			continue
		}
		if !reg.IsKnown(name) && name != "blood_pressure" {
			unknown[name] = struct{}{}
		}

		if name == "sleep_analysis" {
			res.normalizeSleep(m, home)
			continue
		}
		if name == "blood_pressure" {
			res.normalizeBloodPressure(m, home)
			continue
		}

		def := reg.MustGet(name)
		for _, pt := range m.Data {
			v, ok := pickValue(def.ValueField, pt)
			if !ok {
				// 取值字段没有，但可能带着一堆子类型键：类别型指标按子类型分键导出时
				// 根本没有 qty（实测性生活的日汇总是 {"Unspecified":1}），
				// 只按已知字段取值会把整条判成无值、静默丢掉。
				s, has := subtypeSum(pt)
				if !has {
					res.Skipped++
					continue
				}
				v = s
			}
			// 单位归一到字典口径（kJ → kcal 等）：值与其单位必须一起改。
			// 只改值不改单位会让展示层按字典单位渲染，把一个 kJ 数值标成 kcal。
			value, unit := metric.ConvertUnit(v, m.Units, def.Unit)
			s := sample.Sample{
				Metric: name,
				Source: string(pt.Source),
				Device: pt.Device,
				Value:  value,
				Unit:   unit,
			}
			if pt.Min.Set || pt.Avg.Set || pt.Max.Set {
				if pt.Min.Set {
					x, _ := metric.ConvertUnit(pt.Min.Value, m.Units, def.Unit)
					s.ValueMin = &x
				}
				if pt.Max.Set {
					x, _ := metric.ConvertUnit(pt.Max.Value, m.Units, def.Unit)
					s.ValueMax = &x
				}
			}
			if meta := buildMeta(pt); meta != nil {
				s.Meta = meta
			}
			start := pt.Date
			if start == "" {
				start = pt.StartDate
			}
			if !s.SetTime(start, pt.EndDate, home) {
				res.Skipped++
				continue
			}
			res.Samples = append(res.Samples, s)
		}
	}

	for _, w := range p.Data.Workouts {
		wo, ok := normalizeWorkout(w, home)
		if !ok {
			res.Skipped++
			continue
		}
		res.Workouts = append(res.Workouts, wo)
	}

	for _, e := range p.Data.ECG {
		rec, ok := normalizeECG(e, home)
		if !ok {
			res.Skipped++
			continue
		}
		res.ECG = append(res.ECG, rec)
	}

	for name := range unknown {
		res.Unknown = append(res.Unknown, name)
	}
	return res
}

// normalizeSleep 处理睡眠：聚合形态入 sleep_sessions，分段形态入 samples。
func (r *Result) normalizeSleep(m Metric, home *time.Location) {
	for _, pt := range m.Data {
		if pt.TotalSleep.Set || pt.Asleep.Set || pt.SleepStart != "" {
			sess := sample.SleepSession{Source: string(pt.Source)}
			ptr := func(f Float) *float64 {
				if !f.Set {
					return nil
				}
				v := f.Value
				return &v
			}
			sess.TotalSleepHr = ptr(pt.TotalSleep)
			sess.AsleepHr = ptr(pt.Asleep)
			sess.CoreHr = ptr(pt.Core)
			sess.DeepHr = ptr(pt.Deep)
			sess.RemHr = ptr(pt.REM)
			sess.AwakeHr = ptr(pt.Awake)
			sess.InBedHr = ptr(pt.InBed)

			for _, pair := range []struct {
				raw string
				dst **time.Time
			}{
				{pt.SleepStart, &sess.SleepStart},
				{pt.SleepEnd, &sess.SleepEnd},
				{pt.InBedStart, &sess.InBedStart},
				{pt.InBedEnd, &sess.InBedEnd},
			} {
				if pair.raw == "" {
					continue
				}
				if t, _, ok := tz.ParseWithFallback(pair.raw, home); ok {
					tt := t
					*pair.dst = &tt
				}
			}

			// 卧床时长：上游的 inBed 数值在真实导出里恒为 0（288/288 条），而
			// inBedStart/inBedEnd 每条都有值。把 0 原样落库会让界面把「卧床 0 小时」
			// 当成一次真实观测画出来；这里按起止差值补算。起止时刻也没有时才保持 NULL——
			// 没有时刻就没有可算的依据，宁缺勿造。
			if (sess.InBedHr == nil || *sess.InBedHr <= 0) && sess.InBedStart != nil && sess.InBedEnd != nil {
				if d := sess.InBedEnd.Sub(*sess.InBedStart).Hours(); d > 0 && d <= 24 {
					sess.InBedHr = &d
				}
			}

			// 睡眠日归属：按「本夜」——入睡时刻（无则上床时刻）在 18:00 之前算前一晚。
			// 两者都缺时只能退回 HAE 的日桶：那是「哪天」而不是「几点睡」（当地午夜），
			// 按钟点判定会把整晚挪错一天，所以这条换算只对带真实时刻的记录生效。
			anchorRaw := pt.SleepStart
			if anchorRaw == "" {
				anchorRaw = pt.InBedStart
			}
			hasClock := anchorRaw != ""
			if anchorRaw == "" {
				anchorRaw = pt.Date
			}
			anchor, offMin, ok := tz.ParseWithFallback(anchorRaw, home)
			if !ok {
				r.Skipped++
				continue
			}
			sess.TZOffsetMin = tz.OffsetOrDefault(offMin, anchor, home)
			sess.LocalDate = tz.LocalDate(anchor, offMin, home)
			if hasClock {
				sess.LocalDate = tz.NightDate(anchor, offMin, home)
			}
			r.Sleep = append(r.Sleep, sess)
			continue
		}

		// 分段形态：以 qty（小时）为值，阶段写进 meta
		if !pt.Qty.Set || pt.StartDate == "" {
			r.Skipped++
			continue
		}
		s := sample.Sample{Metric: "sleep_analysis", Source: string(pt.Source), Device: pt.Device, Value: pt.Qty.Value, Unit: m.Units}
		if pt.Value != "" {
			s.Meta = map[string]any{"stage": pt.Value}
		}
		if !s.SetTime(pt.StartDate, pt.EndDate, home) {
			r.Skipped++
			continue
		}
		r.Samples = append(r.Samples, s)
	}
}

// normalizeBloodPressure 把血压拆成收缩压 / 舒张压两条样本。
func (r *Result) normalizeBloodPressure(m Metric, home *time.Location) {
	for _, pt := range m.Data {
		base := sample.Sample{Source: string(pt.Source), Device: pt.Device, Unit: m.Units}
		if pt.Systolic.Set {
			s := base
			s.Metric, s.Value = "blood_pressure_systolic", pt.Systolic.Value
			if s.SetTime(pt.Date, "", home) {
				r.Samples = append(r.Samples, s)
			} else {
				r.Skipped++
			}
		}
		if pt.Diastolic.Set {
			s := base
			s.Metric, s.Value = "blood_pressure_diastolic", pt.Diastolic.Value
			if s.SetTime(pt.Date, "", home) {
				r.Samples = append(r.Samples, s)
			} else {
				r.Skipped++
			}
		}
	}
}

// 运动记录的单位口径：运动页固定按 kcal / km 展示，
// 因此能量与距离在入库时就换到这两个单位，展示层不必再猜上游给的是什么单位。
const (
	workoutEnergyUnit   = "kcal"
	workoutDistanceUnit = "km"
	secondsPerMinute    = 60
)

func normalizeWorkout(w Workout, home *time.Location) (sample.Workout, bool) {
	st, offMin, ok := tz.ParseWithFallback(w.Start, home)
	if !ok {
		return sample.Workout{}, false
	}
	wo := sample.Workout{
		Name:        w.Name,
		Source:      string(w.Source),
		StartTime:   st,
		TZOffsetMin: offMin,
		LocalDate:   tz.LocalDate(st, offMin, home),
	}
	if et, _, ok := tz.ParseWithFallback(w.End, home); ok {
		wo.EndTime = et
	} else {
		wo.EndTime = st
	}
	wo.TZOffsetMin = tz.OffsetOrDefault(offMin, st, home)

	span := wo.EndTime.Sub(wo.StartTime)
	if w.Duration.Set {
		wo.DurationMin = workoutDurationMin(w.Duration.Value, span)
	} else {
		wo.DurationMin = span.Minutes()
	}
	if w.TotalEnergy.Qty.Set {
		v, u := metric.ConvertUnit(w.TotalEnergy.Qty.Value, w.TotalEnergy.Units, workoutEnergyUnit)
		wo.TotalEnergy = &v
		wo.EnergyUnit = u
	}
	// 距离字段新旧两版不同名：旧版 totalDistance、新版 distance，取有值的那个。
	dist := w.TotalDistance
	if !dist.Qty.Set {
		dist = w.Distance
	}
	if dist.Qty.Set {
		v, u := metric.ConvertUnit(dist.Qty.Value, dist.Units, workoutDistanceUnit)
		wo.TotalDistance = &v
		wo.DistanceUnit = u
	}
	// 主动能量单独存一份：Apple 运动记录里的「消耗」是主动能量，
	// totalEnergy 含基础代谢，只留总能量会让运动页比健康 App 高出约三成。
	if w.ActiveEnergy.Qty.Set {
		v, u := metric.ConvertUnit(w.ActiveEnergy.Qty.Value, w.ActiveEnergy.Units, workoutEnergyUnit)
		wo.ActiveEnergy = &v
		wo.ActiveUnit = u
	}
	wo.AvgHR = qtyOrNil(w.AvgHeartRate)
	wo.MaxHR = qtyOrNil(w.MaxHeartRate)
	wo.EffortScore = qtyOrNil(w.EffortScore)
	wo.HR = seriesPoints(w.HeartRateData, st, home)
	wo.Recovery = seriesPoints(w.Recovery, st, home)
	if len(w.Splits) > 0 && string(w.Splits) != "null" {
		wo.Splits = append([]byte(nil), w.Splits...)
	}
	wo.Route = normalizeRoute(w.Route, home)
	wo.Meta = workoutMeta(w)
	return wo, true
}

// normalizeRoute 把导出内嵌的 route[] 换成「相对第一个点的秒数 + 坐标」。
//
// 三条判据与 GPX 导入侧（gpx.readPoint）逐字对齐：同一批运动喂同一个折线渲染器，
// 两套判据不一致会让界面画出两种结果，而这两种结果看起来都「像是数据」。
//   - 缺经纬度、或坐标为 (0,0) 的点丢弃：那不是赤道与本初子午线的交点，而是没定位成功，
//     留着会把 bbox 与折线同时画错。
//   - 任一点的时间认不出来 → 整条轨迹不要：时轴按索引推，中间少一个时间，
//     后面的点全部错位，画出一条时间轴错的折线比不画更糟。
//   - 缺海拔按 0 存：海拔不进时轴，缺它只影响着色，不影响位置本身。
func normalizeRoute(pts RouteSeries, home *time.Location) *sample.WorkoutRoute {
	if len(pts) == 0 {
		return nil
	}
	type kept struct {
		at       time.Time
		lat, lon float64
		ele      float64
	}
	keep := make([]kept, 0, len(pts))
	for _, p := range pts {
		if !p.Latitude.Set || !p.Longitude.Set {
			continue
		}
		if p.Latitude.Value == 0 && p.Longitude.Value == 0 {
			continue
		}
		at, ok := routeTime(string(p.Timestamp), home)
		if !ok {
			return nil
		}
		var ele float64
		if p.Altitude.Set {
			ele = p.Altitude.Value
		}
		keep = append(keep, kept{at: at, lat: p.Latitude.Value, lon: p.Longitude.Value, ele: ele})
	}
	if len(keep) == 0 {
		return nil
	}
	start := keep[0].at
	out := &sample.WorkoutRoute{
		StartTime: start.UTC(),
		EndTime:   keep[len(keep)-1].at.UTC(),
		Points:    make([]sample.WorkoutRoutePoint, len(keep)),
	}
	for i, k := range keep {
		out.Points[i] = sample.WorkoutRoutePoint{
			AtSec: int32(k.at.Sub(start).Seconds()),
			Lat:   k.lat, Lon: k.lon, Ele: k.ele,
		}
	}
	return out
}

// routeTime 解释一个轨迹点的时间。
//
// 形态收两种：日期字符串（实测导出就是这样）与裸的 unix 秒。后者不是假设——
// HAE 在别处（心电逐点时间）用的就是 unix 秒，而逐点时间正是它换过形态的字段。
func routeTime(s string, home *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, _, ok := tz.ParseWithFallback(s, home); ok {
		return t, true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		return time.Unix(int64(f), 0).UTC(), true
	}
	return time.Time{}, false
}

// qtyOrNil 取带单位数量里的数值，缺失返回 nil（落库为 NULL，而不是 0）。
func qtyOrNil(q Quantity) *float64 {
	if !q.Qty.Set {
		return nil
	}
	v := q.Qty.Value
	return &v
}

// seriesPoints 把逐点采样换成「相对运动起点的秒数 + 值」，按输入顺序。
//
// 解析不了时间或没有数值的点直接跳过：这里存的是显式秒数（不像心电那样按索引推时轴），
// 所以少一个点只少一个点，不会让后面的点全部错位。
//
// 早于运动起点的点**保留**（相对秒数为负）：实测上游的第一点心率常比运动开始时刻早几十秒
// （手表先开始记心率、后开始计时），按「不能为负」丢掉就是白丢数据——一年 69 个点。
// 读出来的一侧用 start_time + at_sec 还原绝对时刻，负偏移天然成立。
func seriesPoints(pts Series, start time.Time, home *time.Location) []sample.WorkoutPoint {
	if len(pts) == 0 {
		return nil
	}
	out := make([]sample.WorkoutPoint, 0, len(pts))
	for _, p := range pts {
		if !p.Avg.Set || p.Date == "" {
			continue
		}
		t, _, ok := tz.ParseWithFallback(p.Date, home)
		if !ok {
			continue
		}
		out = append(out, sample.WorkoutPoint{AtSec: int32(t.Sub(start).Seconds()), Value: p.Avg.Value})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// workoutMeta 收的是「只用于核对与展示、不参与聚合」的上下文。
//
// 速度类字段连上游单位一起原样存：实测 HAE 给 avgSpeed 的单位串写的是 "km"
// 而值是 km/hr 量级。单位口径存疑时保留原文比猜一个换算更危险——
// 换算错了没人看得出来，原样留着至少核对时对得上。
func workoutMeta(w Workout) map[string]any {
	m := map[string]any{}
	put := func(k string, q Quantity) {
		if !q.Qty.Set {
			return
		}
		m[k] = q.Qty.Value
		if q.Units != "" {
			m[k+"_unit"] = q.Units
		}
	}
	put("basal_energy", w.BasalEnergy.Quantity())
	put("flights", w.Flights)
	put("elevation_up", w.ElevationUp)
	put("elevation_down", w.ElevationDown)
	put("temperature", w.Temperature)
	put("humidity", w.Humidity)
	put("avg_speed", w.AvgSpeed)
	put("max_speed", w.MaxSpeed)
	if w.Indoor.Set {
		m["indoor"] = w.Indoor.Value
	}
	if w.Place != "" {
		m["location"] = string(w.Place)
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// workoutDurationMin 把 duration 字段换算成分钟。
//
// HAE 老版本按分钟给（duration=30 即 30 分钟），新版本改成了秒
// （实测 duration 与 end-start 的秒数误差 <0.1%），而两种形态都没有单位标记。
// 这里用「与起止差值的吻合度」判定：取隐含时长更接近 end-start 的那种解释。
// 无法判定时（缺 end、跨度为 0 或负）回落到分钟，保持老行为不变。
//
// 判错的代价很大——把秒当分钟会让 20 分钟的锻炼显示成 1234 分钟，
// 所以要靠这个可解释的判据，而不是写死某一版的单位。
func workoutDurationMin(raw float64, span time.Duration) float64 {
	spanSec := span.Seconds()
	if spanSec <= 0 {
		return raw
	}
	asSeconds := math.Abs(raw - spanSec)
	asMinutes := math.Abs(raw*secondsPerMinute - spanSec)
	if asSeconds <= asMinutes {
		return raw / secondsPerMinute
	}
	return raw
}

// normalizeECG 归一一条心电测量。
//
// 判定文本一律交给 ecg.Classify，绝不在这里按整串比较：上游给的是本地化文本
// （简中「窦性心律」/ 英文 "Sinus Rhythm"），按整串比会在换手机语言时静默失效。
func normalizeECG(e ECG, home *time.Location) (sample.ECGRecord, bool) {
	st, offMin, ok := tz.ParseWithFallback(e.Start, home)
	if !ok {
		return sample.ECGRecord{}, false
	}
	out := sample.ECGRecord{
		Source:             string(e.Source),
		StartTime:          st,
		TZOffsetMin:        tz.OffsetOrDefault(offMin, st, home),
		LocalDate:          tz.LocalDate(st, offMin, home),
		Classification:     strings.TrimSpace(e.Classification),
		ClassificationCode: ecg.Classify(e.Classification),
	}
	if et, _, ok := tz.ParseWithFallback(e.End, home); ok && !et.Before(st) {
		out.EndTime = et
	} else {
		out.EndTime = st
	}
	if e.AverageHeartRate.Set {
		v := e.AverageHeartRate.Value
		out.AverageHeartRate = &v
	}
	// 采样率为 0 或负时视为缺失：它是时轴的分母，拿 0 去推时轴会直接除零。
	if e.SamplingFrequency.Set && e.SamplingFrequency.Value > 0 {
		v := int(e.SamplingFrequency.Value)
		out.SamplingFrequency = &v
	}
	out.VoltageUnit, out.Voltages = collectVoltages(e.VoltageMeasurements)
	out.VoltageCount = len(out.Voltages)
	return out, true
}

// collectVoltages 取出电压序列与单位。
//
// 两条硬约定：
//   - 单位取第一个有单位的点，且**不换算**。逐点重复写单位只是上游的冗余写法，
//     真出现分歧说明数据本身有问题，此时猜一个换算系数比保留原样更危险。
//   - 只要有一个点缺电压，整段波形就都不入库（记录照常保留）。因为时轴是按
//     索引推的（第 i 点 = start + i/fs），中间少一个点，后面的点全部错位，
//     画出来是一条时间轴错的波形——宁可不画，也不能画错。
func collectVoltages(pts []VoltageMeasurement) (string, []float64) {
	if len(pts) == 0 {
		return "", nil
	}
	unit := ""
	out := make([]float64, 0, len(pts))
	for _, p := range pts {
		if unit == "" {
			unit = p.Units
		}
		if !p.Voltage.Set {
			return unit, nil
		}
		out = append(out, p.Voltage.Value)
	}
	return unit, out
}

// pickValue 按字典声明的字段取值。
func pickValue(field string, pt Point) (float64, bool) {
	switch field {
	case "qty":
		return pt.Qty.Value, pt.Qty.Set
	case "Avg":
		if pt.Avg.Set {
			return pt.Avg.Value, true
		}
		return pt.Qty.Value, pt.Qty.Set
	case "Min":
		return pt.Min.Value, pt.Min.Set
	case "Max":
		return pt.Max.Value, pt.Max.Set
	case "totalSleep":
		return pt.TotalSleep.Value, pt.TotalSleep.Set
	case "systolic":
		return pt.Systolic.Value, pt.Systolic.Set
	case "diastolic":
		return pt.Diastolic.Value, pt.Diastolic.Set
	default:
		return pt.Qty.Value, pt.Qty.Set
	}
}

// subtypeSum 把未知的数字子类型键求和，作为该点的值。
//
// 求和而不是取第一个非零键：一次日汇总可能同时给多个子类型记数
// （{"Protection Used":0,"Unspecified":1}），而「这天发生了几次」的口径只能是总数。
// 全 0 也是有效观测（那天没发生），不当成缺失。
func subtypeSum(pt Point) (float64, bool) {
	if len(pt.Subtypes) == 0 {
		return 0, false
	}
	var sum float64
	for _, n := range pt.Subtypes {
		sum += n
	}
	return sum, true
}

func buildMeta(pt Point) map[string]any {
	meta := map[string]any{}
	if pt.MealTime != "" {
		meta["meal_time"] = pt.MealTime
	}
	if pt.Reason != "" {
		meta["reason"] = pt.Reason
	}
	if pt.Value != "" {
		meta["value"] = pt.Value
	}
	// 子类型明细留着：合并成总数之后，「这 3 次分别是什么」只剩这一份记录能回答
	if len(pt.Subtypes) > 0 {
		meta["subtypes"] = pt.Subtypes
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}
