// 指标「涨跌好坏」的方向表——决定概览卡上偏离常态该用什么颜色。
//
// 为什么需要这张表：MetricCard 原先一律「涨=红、跌=绿」，那是从股票界面借来的习语。
// 放到健康数据上语义是反的——步数、活动能量上涨是好事，静息心率、体重上涨才需要留意。
// 一律标红的结果是：跑得多的一天看起来像告警，称重的水波动荡也像告警，
// 看久了用户只能选择无视颜色，配色就彻底失去意义。
//
// 判定三档：
//   - up-good：涨为「好」（绿），跌需要留意
//   - down-good：跌为「好」——只用于「公认低了更好」的指标（静息心率、体重、血糖、血压）
//   - neutral：既不可以说好了也不可以说坏了（心率本身、体力消耗、呼吸流速这类）
//
// 归不进前两档的一律 neutral，用次级灰显示：宁可不给提示，也不暗示一个我们没有依据的
// 价值判断。比如「平均心率」受运动量影响极大，说它低了更好是错的。
//
// 与 metric-groups.ts 同属纯展示层配置，不进 API / 数据库契约。

export type MetricDirection = 'up-good' | 'down-good' | 'neutral';

/** 越高越好的指标。 */
const UP_GOOD: string[] = [
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
	'dietary_water',
	'mindful_minutes',
	'heart_rate_variability',
	'oxygen_saturation',
	'vo2_max',
	'peak_expiratory_flow_rate',
	'forced_vital_capacity',
	'sleep_analysis'
];

/** 越低越好的指标。 */
const DOWN_GOOD: string[] = [
	'resting_heart_rate',
	'walking_heart_rate_average',
	'atrial_fibrillation_burden',
	'respiratory_rate',
	'body_mass',
	'body_mass_index',
	'body_fat_percentage',
	'blood_glucose',
	'blood_pressure_systolic',
	'blood_pressure_diastolic',
	'caffeine',
	'headphone_audio_exposure',
	'environmental_audio_exposure'
];

const table = new Map<string, MetricDirection>();
for (const m of UP_GOOD) table.set(m, 'up-good');
for (const m of DOWN_GOOD) table.set(m, 'down-good');

/** 返回指标的方向；未登记的指标一律中性。 */
export function directionOf(metric: string): MetricDirection {
	return table.get(metric) ?? 'neutral';
}

/** 偏离常态的色彩档位：good 用品牌绿，bad 用告警红，neutral 用次级灰。 */
export type DeviationTone = 'good' | 'bad' | 'neutral';

/**
 * 由「相对常态的偏离幅度」推断该用什么颜色。
 * 幅度给不出来（常态缺失）或方向中性的指标一律中性色。
 */
export function toneOf(metric: string, pct: number | null): DeviationTone {
	if (pct === null || !Number.isFinite(pct)) return 'neutral';
	const dir = directionOf(metric);
	if (dir === 'neutral') return 'neutral';
	return dir === 'up-good' ? (pct > 0 ? 'good' : 'bad') : pct < 0 ? 'good' : 'bad';
}

export const TONE_CLASS: Record<DeviationTone, string> = {
	good: 'text-[#0f6e56]',
	bad: 'text-[#a32d2d]',
	neutral: 'text-[var(--color-ink-500)]'
};

/** 偏离方向的中文措辞。刻意只说「偏高 / 偏低」，不下「好 / 坏」结论。 */
export function deviationWord(pct: number): string {
	return pct >= 0 ? '偏高' : '偏低';
}
