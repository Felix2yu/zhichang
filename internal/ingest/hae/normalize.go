package hae

import (
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
		name := m.Name
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
			s := sample.Sample{
				Metric: name,
				Source: pt.Source,
				Device: pt.Device,
				Value:  v,
				Unit:   m.Units,
			}
			if pt.Min.Set || pt.Avg.Set || pt.Max.Set {
				if pt.Min.Set {
					x := pt.Min.Value
					s.ValueMin = &x
				}
				if pt.Max.Set {
					x := pt.Max.Value
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
			sess := sample.SleepSession{Source: pt.Source}
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
		s := sample.Sample{Metric: "sleep_analysis", Source: pt.Source, Device: pt.Device, Value: pt.Qty.Value, Unit: m.Units}
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
		base := sample.Sample{Source: pt.Source, Device: pt.Device, Unit: m.Units}
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

func normalizeWorkout(w Workout, home *time.Location) (sample.Workout, bool) {
	st, offMin, ok := tz.ParseWithFallback(w.Start, home)
	if !ok {
		return sample.Workout{}, false
	}
	wo := sample.Workout{
		Name:        w.Name,
		Source:      w.Source,
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

	if w.Duration.Set {
		wo.DurationMin = w.Duration.Value
	} else {
		wo.DurationMin = wo.EndTime.Sub(wo.StartTime).Minutes()
	}
	if w.TotalEnergy.Qty.Set {
		v := w.TotalEnergy.Qty.Value
		wo.TotalEnergy = &v
		wo.EnergyUnit = w.TotalEnergy.Units
	}
	if w.TotalDistance.Qty.Set {
		v := w.TotalDistance.Qty.Value
		wo.TotalDistance = &v
		wo.DistanceUnit = w.TotalDistance.Units
	}
	return wo, true
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
