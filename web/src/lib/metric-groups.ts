// 身体指标的分类配置——对齐苹果健康「浏览」页的分类方式。
//
// 定位：分组只是「版式分区」，用于组织趋势页的阅读层级与指标筛选器，
// **不**表示把同一分组内的指标画进同一张图。趋势页里每个指标各自独立成图，
// 拥有自己的坐标轴与单位标注。
//
// 之所以按苹果健康分类：本应用数据全部来自苹果健康同步，分类口径需与之对齐，
// 用户的心智模型也来自苹果健康。
//
// 分组为纯展示层概念，不进入 API / 数据库契约。

export type MetricGroup = {
	/** 分组稳定标识 */
	id: string;
	/** 分类名（对齐苹果健康） */
	label: string;
	/** 一句话分类描述，用于分区表头 */
	desc: string;
	/** 该分类的统一强调色：用于分区标记与组内图表主色（低饱和，与全局调性一致） */
	tint: string;
	/** 该分组包含的指标 metric 列表 */
	metrics: string[];
};

// 顺序即页面展示顺序；「其他」兜底分组放最后。
export const METRIC_GROUPS: MetricGroup[] = [
	{
		id: 'activity',
		label: '活动',
		desc: '步数、能量、距离、楼层与锻炼时长',
		tint: '#0f6e56',
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
		desc: '心率、心率变异性、血氧与呼吸频率',
		tint: '#a3475c',
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
		desc: '体重、BMI、体脂、身高、体温与最大摄氧量',
		tint: '#2f6fb0',
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
		desc: '收缩压与舒张压',
		tint: '#25507f',
		metrics: ['blood_pressure_systolic', 'blood_pressure_diastolic']
	},
	{
		id: 'glucose',
		label: '血糖',
		desc: '血糖浓度',
		tint: '#b07a2a',
		metrics: ['blood_glucose']
	},
	{
		id: 'sleep',
		label: '睡眠',
		desc: '每日睡眠时长',
		tint: '#5a6b8c',
		metrics: ['sleep_analysis']
	},
	{
		id: 'respiratory',
		label: '呼吸',
		desc: '肺活量与呼气流速',
		tint: '#3f7d6b',
		metrics: ['peak_expiratory_flow_rate', 'forced_vital_capacity']
	},
	{
		id: 'hydration',
		label: '水分',
		desc: '每日饮水量',
		tint: '#3f7fa6',
		metrics: ['dietary_water']
	},
	{
		id: 'mindful',
		label: '正念',
		desc: '正念练习时长',
		tint: '#7a6a9c',
		metrics: ['mindful_minutes']
	},
	{
		id: 'other',
		label: '其他',
		desc: '洗手、刷牙、心理状态、步行速度与步幅',
		tint: '#7b7f87',
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
// 带小数的量（kg、℃、%、mg/dL、cm、L 等）取 1。
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
