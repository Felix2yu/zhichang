package hae

import (
	"math"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/domain/tz"
)

// Result 是归一化结果。
type Result struct {
	Samples  []sample.Sample
	Workouts []sample.Workout
	Sleep    []sample.SleepSession
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
				res.Skipped++
				continue
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

			// 睡眠日归属：以 sleepStart（无则 inBedStart / date）的本地日期为准
			anchorRaw := pt.SleepStart
			if anchorRaw == "" {
				anchorRaw = pt.InBedStart
			}
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
	return wo, true
}

// workoutDurationMin 把 duration 字段换算成分钟。
//
// HAE 老版本按分钟给（duration=30 即 30 分钟），新版本改成了秒
// （实测 duration 与 end-start 的秒数误差 <0.1%），而两种形态都没有单位标记。
// 这里用「与起止差值的吻合度」判定：取隐含时长更接近 end-start 的那种解释。
// 无法判定时（缺 end、跨度为 0 或负）回落到分钟，保持老行为不变。
//
// 判错的代价很大——把秒当分钟会让 15 分钟的步行显示成 890 分钟，
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
	if len(meta) == 0 {
		return nil
	}
	return meta
}
