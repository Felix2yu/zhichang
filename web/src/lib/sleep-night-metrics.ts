import type { NightMetricRow, NightSeries } from '#lib/api';

/**
 * 夜间生理指标的展示口径。
 * 后端白名单（NightPhysiologyMetrics）是这些指标能被按入睡窗口取出来的原因，
 * 这里只负责「取出来之后怎么称呼、什么单位」，加指标时两边各补一行。
 */
export type NightMetricMeta = {
	label: string;
	unit: string;
	digits: number;
	color: string;
};

export const NIGHT_METRIC_META: Record<string, NightMetricMeta> = {
	heart_rate: { label: '夜间心率', unit: 'bpm', digits: 0, color: '#993556' },
	heart_rate_variability: { label: '夜间 HRV', unit: 'ms', digits: 0, color: '#5a6b8c' },
	oxygen_saturation: { label: '夜间血氧', unit: '%', digits: 0, color: '#0f6e56' },
	respiratory_rate: { label: '夜间呼吸频率', unit: '次/分', digits: 0, color: '#185fa5' }
};

/** 卡片与曲线的默认主角：Apple 心率页打的也是这个 */
export const DEFAULT_NIGHT_METRIC = 'heart_rate';

export function nightMeta(metric: string): NightMetricMeta {
	return (
		NIGHT_METRIC_META[metric] ?? { label: metric, unit: '', digits: 0, color: '#5f5e5a' }
	);
}

/** key = `${metric}|${local_date}`，与 SleepRow.local_date（睡眠日）同口径，可直接对齐 */
export function nightKey(metric: string, localDate: string): string {
	return `${metric}|${localDate}`;
}

export function indexNightMetrics(rows: NightMetricRow[]): Map<string, NightMetricRow> {
	const m = new Map<string, NightMetricRow>();
	for (const r of rows) m.set(nightKey(r.metric, r.local_date), r);
	return m;
}

/** 某一晚的序列（分期时间线叠加曲线用）；没有点则不返回 */
export function seriesFor(phys: NightSeries[], metric: string): NightSeries | null {
	const s = phys.find((p) => p.metric === metric && p.points.length > 0);
	return s ?? null;
}

/** 这一晚出现了哪些有数据的夜间指标（按白名单顺序，供切换条只列真实存在的项） */
export function availableNightMetrics(phys: NightSeries[]): string[] {
	return Object.keys(NIGHT_METRIC_META).filter((k) => seriesFor(phys, k) !== null);
}

/**
 * 本夜轴分钟 → 墙钟 HH:MM（跨零点后自然显示为次日时刻）。
 * 与 SleepTimeline 的横轴同一把尺子：0 是睡眠日本地 00:00。
 */
export function nightClock(min: number): string {
	const m = ((Math.round(min) % 1440) + 1440) % 1440;
	return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}

/**
 * 把某晚的最低值时刻还原本地墙钟。
 * 偏移只用「打出最低值的那条样本」自带的（min_at_tz_offset_min）；
 * 缺偏移就宁可不显示——拿 0 当 UTC 会把钟点整体偏好几个小时。
 */
export function nightMinClock(row: NightMetricRow): string {
	if (!row.min_at || row.min_at_tz_offset_min == null) return '';
	const t = new Date(row.min_at);
	if (Number.isNaN(t.getTime())) return '';
	const local = new Date(t.getTime() + row.min_at_tz_offset_min * 60_000);
	// 只取钟点：跨零点后半夜的时刻取模后照样是对的
	return nightClock(local.getUTCHours() * 60 + local.getUTCMinutes());
}
