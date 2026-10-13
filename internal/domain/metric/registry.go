// Package metric 定义健康指标的聚合口径字典。
package metric

import "time"

// Kind 表示指标的语义类别。
type Kind string

const (
	// KindCumulative 累计量（步数、能量等），按天 sum。
	KindCumulative Kind = "cumulative"
	// KindInstant 瞬时量（心率、体重等），按天 mean。
	KindInstant Kind = "instant"
	// KindInterval 区间量（睡眠），按天 sum 时长 / last 起止时刻。
	KindInterval Kind = "interval"
	// KindCategorical 类别量，按天计数。
	KindCategorical Kind = "categorical"
)

// DayAgg 天内的聚合方式。
type DayAgg string

const (
	DayAggSum   DayAgg = "sum"
	DayAggMean  DayAgg = "mean"
	DayAggMax   DayAgg = "max"
	DayAggMin   DayAgg = "min"
	DayAggLast  DayAgg = "last"
	DayAggCount DayAgg = "count"
)

// CrossAgg 跨来源（iPhone / Apple Watch）的收敛方式。
type CrossAgg string

const (
	CrossAggMax    CrossAgg = "max"
	CrossAggMean   CrossAgg = "mean"
	CrossAggSum    CrossAgg = "sum"
	CrossAggLatest CrossAgg = "latest"
)

// Def 是一个指标的完整口径定义。
type Def struct {
	Metric          string    `json:"metric"`
	DisplayNameZH   string    `json:"display_name_zh"`
	Kind            Kind      `json:"kind"`
	DayAgg          DayAgg    `json:"day_agg"`
	CrossSourceAgg  CrossAgg  `json:"cross_source_agg"`
	Unit            string    `json:"unit"`
	PreferredUnit   string    `json:"preferred_unit"`
	ValueField      string    `json:"value_field"`
	Chart           string    `json:"chart"`
	Enabled         bool      `json:"enabled"`
	SortOrder       int       `json:"sort_order"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

// Registry 是内存态的指标字典。
type Registry struct {
	defs map[string]Def
}

// New 构造字典。
func New(defs []Def) *Registry {
	r := &Registry{defs: make(map[string]Def, len(defs))}
	for _, d := range defs {
		r.defs[d.Metric] = d
	}
	return r
}

// Get 查询指标定义，第二个返回值表示是否存在。
func (r *Registry) Get(name string) (Def, bool) {
	d, ok := r.defs[name]
	return d, ok
}

// MustGet 查询指标定义，未知指标返回零值。
func (r *Registry) MustGet(name string) Def {
	return r.defs[name]
}

// All 返回全部定义。
func (r *Registry) All() []Def {
	out := make([]Def, 0, len(r.defs))
	for _, d := range r.defs {
		out = append(out, d)
	}
	return out
}

// Enabled 返回启用中的定义。
func (r *Registry) Enabled() []Def {
	out := make([]Def, 0, len(r.defs))
	for _, d := range r.defs {
		if d.Enabled {
			out = append(out, d)
		}
	}
	return out
}

// IsKnown 判断指标是否已在字典中登记。
func (r *Registry) IsKnown(name string) bool {
	_, ok := r.defs[name]
	return ok
}

func def(metric, zh string, kind Kind, day DayAgg, cross CrossAgg, unit, field, chart string, order int) Def {
	return Def{
		Metric:         metric,
		DisplayNameZH:  zh,
		Kind:           kind,
		DayAgg:         day,
		CrossSourceAgg: cross,
		Unit:           unit,
		PreferredUnit:  unit,
		ValueField:     field,
		Chart:          chart,
		Enabled:        true,
		SortOrder:      order,
	}
}

// Defaults 返回内置指标口径（与 migrations/0002_seed_metrics.sql 保持一致）。
func Defaults() []Def {
	return []Def{
		// 累计量：日内 sum，跨来源取各来源总量的 max（消除 iPhone / Watch 双计）
		def("step_count", "步数", KindCumulative, DayAggSum, CrossAggMax, "count", "qty", "bar", 10),
		def("active_energy", "活动能量", KindCumulative, DayAggSum, CrossAggMax, "kcal", "qty", "bar", 20),
		def("basal_energy_burned", "基础能量", KindCumulative, DayAggSum, CrossAggMax, "kcal", "qty", "bar", 30),
		def("flights_climbed", "爬楼层数", KindCumulative, DayAggSum, CrossAggMax, "count", "qty", "bar", 40),
		def("walking_running_distance", "步行+跑步距离", KindCumulative, DayAggSum, CrossAggMax, "km", "qty", "bar", 50),
		def("distance_walking_running", "步行+跑步距离", KindCumulative, DayAggSum, CrossAggMax, "km", "qty", "bar", 51),
		def("dietary_water", "饮水量", KindCumulative, DayAggSum, CrossAggMax, "mL", "qty", "bar", 60),
		def("mindful_minutes", "正念时长", KindCumulative, DayAggSum, CrossAggMax, "min", "qty", "bar", 70),
		def("exercise_time", "锻炼时长", KindCumulative, DayAggSum, CrossAggMax, "min", "qty", "bar", 80),
		def("stand_time", "站立时长", KindCumulative, DayAggSum, CrossAggMax, "min", "qty", "bar", 90),
		def("sleep_analysis", "睡眠时长", KindInterval, DayAggSum, CrossAggMax, "hr", "totalSleep", "stacked", 100),

		// 瞬时量：日内 mean，跨来源按样本数加权 mean
		def("heart_rate", "心率", KindInstant, DayAggMean, CrossAggMean, "bpm", "Avg", "line", 110),
		def("resting_heart_rate", "静息心率", KindInstant, DayAggMean, CrossAggMean, "bpm", "qty", "line", 120),
		def("heart_rate_variability", "心率变异性", KindInstant, DayAggMean, CrossAggMean, "ms", "qty", "line", 130),
		def("walking_heart_rate_average", "步行平均心率", KindInstant, DayAggMean, CrossAggMean, "bpm", "qty", "line", 140),
		def("oxygen_saturation", "血氧饱和度", KindInstant, DayAggMean, CrossAggMean, "%", "qty", "line", 150),
		def("respiratory_rate", "呼吸频率", KindInstant, DayAggMean, CrossAggMean, " breaths/min", "qty", "line", 160),
		def("body_temperature", "体温", KindInstant, DayAggMean, CrossAggMean, "degC", "qty", "line", 170),
		def("blood_glucose", "血糖", KindInstant, DayAggMean, CrossAggMean, "mg/dL", "qty", "line", 180),
		def("body_mass", "体重", KindInstant, DayAggMean, CrossAggMean, "kg", "qty", "line", 190),
		def("weight_&_body_mass", "体重", KindInstant, DayAggMean, CrossAggMean, "kg", "qty", "line", 191),
		def("body_mass_index", "BMI", KindInstant, DayAggMean, CrossAggMean, "count", "qty", "line", 200),
		def("body_fat_percentage", "体脂率", KindInstant, DayAggMean, CrossAggMean, "%", "qty", "line", 210),
		def("lean_body_mass", "去脂体重", KindInstant, DayAggMean, CrossAggMean, "kg", "qty", "line", 220),
		def("height", "身高", KindInstant, DayAggLast, CrossAggMax, "cm", "qty", "line", 230),
		def("blood_pressure_systolic", "收缩压", KindInstant, DayAggMean, CrossAggMean, "mmHg", "systolic", "line", 240),
		def("blood_pressure_diastolic", "舒张压", KindInstant, DayAggMean, CrossAggMean, "mmHg", "diastolic", "line", 250),
		def("vo2_max", "最大摄氧量", KindInstant, DayAggLast, CrossAggMean, "mL/min·kg", "qty", "line", 260),
		def("walking_speed", "步行速度", KindInstant, DayAggMean, CrossAggMean, "km/hr", "qty", "line", 270),
		def("walking_step_length", "步幅", KindInstant, DayAggMean, CrossAggMean, "cm", "qty", "line", 280),
		def("peak_expiratory_flow_rate", "最大呼气流速", KindInstant, DayAggMax, CrossAggMax, "L/min", "qty", "line", 290),
		def("forced_vital_capacity", "用力肺活量", KindInstant, DayAggMax, CrossAggMax, "L", "qty", "line", 300),

		// 类别/事件量：日内计数
		def("handwashing", "洗手次数", KindCategorical, DayAggCount, CrossAggMax, "count", "qty", "bar", 400),
		def("toothbrushing", "刷牙次数", KindCategorical, DayAggCount, CrossAggMax, "count", "qty", "bar", 410),
		def("state_of_mind", "心理状态", KindCategorical, DayAggCount, CrossAggMax, "count", "qty", "bar", 420),
	}
}
