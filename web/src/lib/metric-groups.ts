// 身体指标的分组配置——对齐苹果健康「浏览」页的分类方式。
//
// 设计原则：
// 1. 每个分组内的指标在量级 / 量纲上相近，可共用一张图的自适应坐标轴，
//    避免原来「步数上万 vs 心率/体重几十」挤在同一坐标轴的可读性灾难。
// 2. 分组名与苹果健康保持一致（活动、心脏、身体测量、血压、睡眠、呼吸等），
//    因为本应用数据完全来自苹果健康同步，分类口径应与之对齐。
// 3. 分组为展示层概念，纯前端维护，不进入 API / 数据库契约。

export type MetricGroup = {
	/** 分组稳定标识 */
	id: string;
	/** 中文分类名（对齐苹果健康） */
	label: string;
	/** 分组图标（emoji，便于移动端快速辨识） */
	icon: string;
	/** 单位 / 成员提示，展示在图标题右侧 */
	hint: string;
	/** 该分组包含的指标 metric 列表 */
	metrics: string[];
};

// 顺序即页面展示顺序；「其他」兜底分组放最后。
export const METRIC_GROUPS: MetricGroup[] = [
	{
		id: 'activity',
		label: '活动',
		icon: '🏃',
		hint: '步数 · 能量 · 距离 · 楼层 · 锻炼',
		metrics: [
			'step_count',
			'active_energy',
			'basal_energy_burned',
			'flights_climbed',
			'walking_running_distance',
			'distance_walking_running',
			'exercise_time',
			'stand_time'
		]
	},
	{
		id: 'heart',
		label: '心脏',
		icon: '❤️',
		hint: '心率 · 血氧 · 呼吸频率',
		metrics: [
			'heart_rate',
			'resting_heart_rate',
			'heart_rate_variability',
			'walking_heart_rate_average',
			'oxygen_saturation',
			'respiratory_rate'
		]
	},
	{
		id: 'body',
		label: '身体测量',
		icon: '📏',
		hint: '体重 · BMI · 体脂 · 身高 · 体温',
		metrics: [
			'body_mass',
			'weight_&_body_mass',
			'body_mass_index',
			'body_fat_percentage',
			'lean_body_mass',
			'height',
			'body_temperature',
			'vo2_max'
		]
	},
	{
		id: 'bp',
		label: '血压',
		icon: '🩺',
		hint: '收缩压 · 舒张压',
		metrics: ['blood_pressure_systolic', 'blood_pressure_diastolic']
	},
	{
		id: 'glucose',
		label: '血糖',
		icon: '🩸',
		hint: '血糖',
		metrics: ['blood_glucose']
	},
	{
		id: 'sleep',
		label: '睡眠',
		icon: '💤',
		hint: '睡眠时长',
		metrics: ['sleep_analysis']
	},
	{
		id: 'respiratory',
		label: '呼吸',
		icon: '🫁',
		hint: '肺活量 · 呼气流速',
		metrics: ['peak_expiratory_flow_rate', 'forced_vital_capacity']
	},
	{
		id: 'hydration',
		label: '水分',
		icon: '💧',
		hint: '饮水量',
		metrics: ['dietary_water']
	},
	{
		id: 'mindful',
		label: '正念',
		icon: '🧘',
		hint: '正念时长',
		metrics: ['mindful_minutes']
	},
	{
		id: 'other',
		label: '其他',
		icon: '🧩',
		hint: '洗手 · 刷牙 · 心情 · 步速 · 步幅',
		metrics: [
			'handwashing',
			'toothbrushing',
			'state_of_mind',
			'walking_speed',
			'walking_step_length'
		]
	}
];

/** 返回某指标所属的分组；未知指标兜底归入「其他」。 */
export function groupOf(metric: string): MetricGroup {
	return METRIC_GROUPS.find((g) => g.metrics.includes(metric)) ?? METRIC_GROUPS[METRIC_GROUPS.length - 1];
}

// 单位 → 小数位。整数型量级（步数、千卡、分钟、小时、计数）取 0，
// 带小数的量（kg、bpm、℃、%、mg/dL、cm、L 等）取 1。
const UNIT_DIGITS: Record<string, number> = {
	count: 0,
	kcal: 0,
	km: 1,
	min: 0,
	hr: 1,
	bpm: 0,
	ms: 0,
	'%': 0,
	'degC': 1,
	'mg/dL': 0,
	kg: 1,
	mmHg: 0,
	'mL/min·kg': 1,
	'km/hr': 1,
	cm: 1,
	'L/min': 1,
	L: 1,
	mL: 0,
	'breaths/min': 0
};

/** 根据单位推断合适的展示小数位。 */
export function unitDigits(unit: string): number {
	return unit in UNIT_DIGITS ? UNIT_DIGITS[unit] : 0;
}
