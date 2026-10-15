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
//
// ⚠️ 这里同时是趋势页指标选择器的准入名单：页面按 group.metrics 过滤字典，
// 没写进任何分组的指标在界面上完全没有入口（图表画不出来、也搜不到）。
// 字典里新增指标时必须同步加到这里。
//
// 一个概念只列一条：上游的旧名（blood_oxygen_saturation、apple_stand_time、
// distance_walking_running 等）由后端 alias.go 在入库时收敛到规范名，
// 不在这里重复列出——重复会让选择器出现两个同名 chip，其中一个永远没数据。

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
		desc: '步数、能量、距离、楼层、站立与日照',
		tint: '#0f6e56',
		metrics: [
			'step_count',
			'active_energy',
			'basal_energy_burned',
			'flights_climbed',
			'walking_running_distance',
			'cycling_distance',
			'exercise_time',
			'stand_time',
			'stand_hour',
			'time_in_daylight',
			'physical_effort'
		]
	},
	{
		id: 'heart',
		label: '心脏',
		desc: '心率、心率变异性、血氧、呼吸频率与房颤',
		tint: '#a3475c',
		metrics: [
			'heart_rate',
			'resting_heart_rate',
			'heart_rate_variability',
			'walking_heart_rate_average',
			'cardio_recovery',
			'oxygen_saturation',
			'atrial_fibrillation_burden',
			'respiratory_rate'
		]
	},
	{
		id: 'body',
		label: '身体测量',
		desc: '体重、BMI、体脂、身高与体温',
		tint: '#2f6fb0',
		metrics: [
			'body_mass',
			'body_mass_index',
			'body_fat_percentage',
			'lean_body_mass',
			'height',
			'body_temperature',
			'sleeping_wrist_temperature',
			'vo2_max'
		]
	},
	{
		id: 'mobility',
		label: '行动能力',
		desc: '步速、步幅、上下楼速度与步行稳定性',
		tint: '#6d7a3f',
		metrics: [
			'walking_speed',
			'running_speed',
			'running_power',
			'walking_step_length',
			'stair_speed_up',
			'stair_speed_down',
			'walking_asymmetry_percentage',
			'walking_double_support_percentage',
			'six_minute_walking_test_distance'
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
		id: 'respiratory',
		label: '呼吸',
		desc: '肺活量与呼气流速',
		tint: '#3f7d6b',
		metrics: ['peak_expiratory_flow_rate', 'forced_vital_capacity']
	},
	{
		id: 'sleep',
		label: '睡眠',
		desc: '每日睡眠时长',
		tint: '#5a6b8c',
		metrics: ['sleep_analysis']
	},
	{
		id: 'hearing',
		label: '听力',
		desc: '环境音量与耳机音量',
		tint: '#8c5a7a',
		metrics: ['environmental_audio_exposure', 'headphone_audio_exposure']
	},
	{
		id: 'hydration',
		label: '饮食',
		desc: '饮水量与咖啡因摄入',
		tint: '#3f7fa6',
		metrics: ['dietary_water', 'caffeine']
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
		desc: '洗手、刷牙、心理状态与私密事件',
		tint: '#7b7f87',
		metrics: ['handwashing', 'toothbrushing', 'state_of_mind', 'sexual_activity']
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
	'breaths/min': 0,
	// 以下为后补指标的展示单位：取值区间窄、量级小，多留一位才有区分度
	'count/min': 0,
	'mg': 0,
	m: 0,
	'm/s': 2,
	W: 0,
	s: 0,
	'dBASPL': 0,
	'kcal/hr·kg': 2
};

/** 根据单位推断合适的展示小数位。 */
export function unitDigits(unit: string): number {
	return unit in UNIT_DIGITS ? UNIT_DIGITS[unit] : 0;
}
