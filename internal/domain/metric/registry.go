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
	Metric         string    `json:"metric"`
	DisplayNameZH  string    `json:"display_name_zh"`
	Kind           Kind      `json:"kind"`
	DayAgg         DayAgg    `json:"day_agg"`
	CrossSourceAgg CrossAgg  `json:"cross_source_agg"`
	Unit           string    `json:"unit"`
	PreferredUnit  string    `json:"preferred_unit"`
	ValueField     string    `json:"value_field"`
	Chart          string    `json:"chart"`
	Enabled        bool      `json:"enabled"`
	SortOrder      int       `json:"sort_order"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
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

// Defaults 返回内置指标口径。启动时经 store.SeedMetrics 补齐到 metric_defs
// （ON CONFLICT DO NOTHING，用户手工改过的条目不动），再 LoadMetrics 读回内存字典。
//
// 一个概念只登记一条：上游的多种写法由 alias.go 的别名表在入库时收敛过来，
// 不在这里重复登记（重复登记会让趋势页出现两个同名 chip，其中一个永远无数据）。
func Defaults() []Def {
	return []Def{
		// 累计量：日内 sum，跨来源取各来源总量的 max（消除 iPhone / Watch 双计）
		def("step_count", "步数", KindCumulative, DayAggSum, CrossAggMax, "count", "qty", "bar", 10),
		def("active_energy", "活动能量", KindCumulative, DayAggSum, CrossAggMax, "kcal", "qty", "bar", 20),
		def("basal_energy_burned", "基础能量", KindCumulative, DayAggSum, CrossAggMax, "kcal", "qty", "bar", 30),
		def("flights_climbed", "爬楼层数", KindCumulative, DayAggSum, CrossAggMax, "count", "qty", "bar", 40),
		def("walking_running_distance", "步行+跑步距离", KindCumulative, DayAggSum, CrossAggMax, "km", "qty", "bar", 50),
		def("cycling_distance", "骑行距离", KindCumulative, DayAggSum, CrossAggMax, "km", "qty", "bar", 55),
		def("dietary_water", "饮水量", KindCumulative, DayAggSum, CrossAggMax, "mL", "qty", "bar", 60),
		def("caffeine", "咖啡因", KindCumulative, DayAggSum, CrossAggMax, "mg", "qty", "bar", 65),
		def("mindful_minutes", "正念时长", KindCumulative, DayAggSum, CrossAggMax, "min", "qty", "bar", 70),
		def("time_in_daylight", "日照时长", KindCumulative, DayAggSum, CrossAggMax, "min", "qty", "bar", 75),
		def("exercise_time", "锻炼时长", KindCumulative, DayAggSum, CrossAggMax, "min", "qty", "bar", 80),
		def("stand_time", "站立时长", KindCumulative, DayAggSum, CrossAggMax, "min", "qty", "bar", 90),
		def("stand_hour", "站立小时数", KindCumulative, DayAggSum, CrossAggMax, "count", "qty", "bar", 95),
		def("sleep_analysis", "睡眠时长", KindInterval, DayAggSum, CrossAggMax, "hr", "totalSleep", "stacked", 100),

		// 瞬时量：日内 mean，跨来源按样本数加权 mean
		def("heart_rate", "心率", KindInstant, DayAggMean, CrossAggMean, "bpm", "Avg", "line", 110),
		def("resting_heart_rate", "静息心率", KindInstant, DayAggMean, CrossAggMean, "bpm", "qty", "line", 120),
		def("heart_rate_variability", "心率变异性", KindInstant, DayAggMean, CrossAggMean, "ms", "qty", "line", 130),
		def("walking_heart_rate_average", "步行平均心率", KindInstant, DayAggMean, CrossAggMean, "bpm", "qty", "line", 140),
		def("cardio_recovery", "心率恢复", KindInstant, DayAggMean, CrossAggMean, "count/min", "qty", "line", 145),
		def("oxygen_saturation", "血氧饱和度", KindInstant, DayAggMean, CrossAggMean, "%", "qty", "line", 150),
		def("atrial_fibrillation_burden", "房颤负荷", KindInstant, DayAggMean, CrossAggMean, "%", "qty", "line", 155),
		// 呼吸频率的展示单位不带前导空格：字典的 unit 就是界面上徽标的字面量。
		def("respiratory_rate", "呼吸频率", KindInstant, DayAggMean, CrossAggMean, "breaths/min", "qty", "line", 160),
		def("body_temperature", "体温", KindInstant, DayAggMean, CrossAggMean, "degC", "qty", "line", 170),
		def("sleeping_wrist_temperature", "睡眠腕温", KindInstant, DayAggMean, CrossAggMean, "degC", "qty", "line", 175),
		def("blood_glucose", "血糖", KindInstant, DayAggMean, CrossAggMean, "mg/dL", "qty", "line", 180),
		def("body_mass", "体重", KindInstant, DayAggMean, CrossAggMean, "kg", "qty", "line", 190),
		def("body_mass_index", "BMI", KindInstant, DayAggMean, CrossAggMean, "count", "qty", "line", 200),
		def("body_fat_percentage", "体脂率", KindInstant, DayAggMean, CrossAggMean, "%", "qty", "line", 210),
		def("lean_body_mass", "去脂体重", KindInstant, DayAggMean, CrossAggMean, "kg", "qty", "line", 220),
		def("height", "身高", KindInstant, DayAggLast, CrossAggMax, "cm", "qty", "line", 230),
		def("blood_pressure_systolic", "收缩压", KindInstant, DayAggMean, CrossAggMean, "mmHg", "systolic", "line", 240),
		def("blood_pressure_diastolic", "舒张压", KindInstant, DayAggMean, CrossAggMean, "mmHg", "diastolic", "line", 250),
		def("vo2_max", "最大摄氧量", KindInstant, DayAggLast, CrossAggMean, "mL/min·kg", "qty", "line", 260),
		def("physical_effort", "体力消耗", KindInstant, DayAggMean, CrossAggMean, "kcal/hr·kg", "qty", "line", 265),
		def("walking_speed", "步行速度", KindInstant, DayAggMean, CrossAggMean, "km/hr", "qty", "line", 270),
		def("walking_step_length", "步幅", KindInstant, DayAggMean, CrossAggMean, "cm", "qty", "line", 280),
		def("walking_asymmetry_percentage", "步行不对称性", KindInstant, DayAggMean, CrossAggMean, "%", "qty", "line", 281),
		def("walking_double_support_percentage", "双脚支撑时间占比", KindInstant, DayAggMean, CrossAggMean, "%", "qty", "line", 282),
		def("six_minute_walking_test_distance", "六分钟步行距离", KindInstant, DayAggMean, CrossAggMean, "m", "qty", "line", 283),
		def("stair_speed_up", "上楼速度", KindInstant, DayAggMean, CrossAggMean, "m/s", "qty", "line", 285),
		def("stair_speed_down", "下楼速度", KindInstant, DayAggMean, CrossAggMean, "m/s", "qty", "line", 286),
		def("peak_expiratory_flow_rate", "最大呼气流速", KindInstant, DayAggMax, CrossAggMax, "L/min", "qty", "line", 290),
		def("forced_vital_capacity", "用力肺活量", KindInstant, DayAggMax, CrossAggMax, "L", "qty", "line", 300),
		def("environmental_audio_exposure", "环境音量", KindInstant, DayAggMean, CrossAggMean, "dBASPL", "qty", "line", 310),
		def("headphone_audio_exposure", "耳机音量", KindInstant, DayAggMean, CrossAggMean, "dBASPL", "qty", "line", 320),

		// 类别/事件量：日内计数
		def("handwashing", "洗手次数", KindCategorical, DayAggCount, CrossAggMax, "count", "qty", "bar", 400),
		def("toothbrushing", "刷牙次数", KindCategorical, DayAggCount, CrossAggMax, "count", "qty", "bar", 410),
		def("state_of_mind", "心理状态", KindCategorical, DayAggCount, CrossAggMax, "count", "qty", "bar", 420),
	}
}
