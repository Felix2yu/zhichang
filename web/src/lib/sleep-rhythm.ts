// 睡眠的周期性节律与结构分析（纯函数，不碰 DOM / 网络）。
//
// 这里回答两类问题：
//   1. 「节律」——我是不是每天都在差不多的时间睡？工作日和周末差多少？
//      按时钟时刻统计必须用**环形中位数**：23:50 与 00:10 只差 20 分钟，
//      直接对「分钟数」求平均会得到 12:00 这种荒谬结果（复用 sleep-score 的环形展开）。
//   2. 「结构」——睡够了吗、质量如何？用睡眠效率（在床时长里真正睡着的比例）
//      与各阶段占比来刻画，比单看「睡了几小时」更能反映质量。
//
// 数据来源全部是 /api/sleep 已经透出的字段，未新增后端接口。

import { bedtimeMinutes } from './sleep-score';

/** 一晚的睡眠输入。 */
export type RhythmNight = {
	local_date: string;
	total_sleep_hr?: number | null;
	asleep_hr?: number | null;
	in_bed_hr?: number | null;
	core_hr?: number | null;
	deep_hr?: number | null;
	rem_hr?: number | null;
	awake_hr?: number | null;
	/** UTC RFC3339 */
	sleep_start?: string | null;
	/** UTC RFC3339 */
	sleep_end?: string | null;
	/** UTC RFC3339 · 上床时刻；与 sleep_start 相减即入睡潜伏期 */
	in_bed_start?: string | null;
	/** UTC RFC3339 · 离床时刻 */
	in_bed_end?: string | null;
	tz_offset_min?: number | null;
};

export const WEEKDAYS = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'] as const;

/** 每个星期几的节律统计。样本不足时各项为 null，界面据此不画。 */
export type WeekdayRhythm = {
	/** 0=周一 … 6=周日 */
	index: number;
	label: string;
	nights: number;
	/** 该组的入睡时刻中位数（相对 18:00 的分钟，与 sleep-score 同口径） */
	bedtimeMin: number | null;
	/** 起床时刻中位数（本地当日 00:00 起的分钟） */
	wakeMin: number | null;
	/** 总睡眠时长中位数（小时） */
	sleepHr: number | null;
	/** 该组的就寝离散度（分钟），越大越不规律 */
	bedtimeSpreadMin: number | null;
};

/** 全区间的节律汇总。 */
export type RhythmSummary = {
	byWeekday: WeekdayRhythm[];
	/** 工作日（周一至周五）的就寝中位数 */
	weekdayBedtimeMin: number | null;
	/** 周末（周六、周日）的就寝中位数 */
	weekendBedtimeMin: number | null;
	/** 社交时差：周末就寝比工作日晚多少分钟（负数=周末睡得更早） */
	socialJetlagMin: number | null;
	/** 就寝时刻的总体离散度（分钟），反映规律程度 */
	bedtimeSpreadMin: number | null;
	/** 参与统计的夜晚数 */
	nights: number;
	/** 缺少入睡时刻、因而被排除出节律统计的夜晚数 */
	missingBedtime: number;
};

function median(xs: number[]): number | null {
	if (xs.length === 0) return null;
	const s = [...xs].sort((a, b) => a - b);
	const mid = s.length >> 1;
	return s.length % 2 ? s[mid] : (s[mid - 1] + s[mid]) / 2;
}

/**
 * 环形中位数：把时刻平移到基准附近再取中位数，消除跨零点失真。
 * 23:50(=350) 与 00:10(=370) 应得 360（00:00），而不是两者平均后绕远路。
 */
function circularMedian(mins: number[]): number | null {
	if (mins.length === 0) return null;
	// 以第一个样本为基准展开，跨度必然小于 12 小时
	const ref = mins[0];
	const unwrapped = mins.map((v) => v + 1440 * Math.round((ref - v) / 1440));
	const m = median(unwrapped);
	return m === null ? null : ((m % 1440) + 1440) % 1440;
}

/** 离散度：到中位数的平均绝对偏差（分钟），对偶发异常不敏感。 */
function spread(mins: number[], center: number): number | null {
	if (mins.length === 0 || center === null) return null;
	const ref = center;
	const devs = mins.map((v) => {
		const u = v + 1440 * Math.round((ref - v) / 1440);
		return Math.abs(u - ref);
	});
	return devs.reduce((a, b) => a + b, 0) / devs.length;
}

/** 'YYYY-MM-DD' → 0=周一 … 6=周日。 */
function weekdayIndex(localDate: string): number {
	const [y, m, d] = localDate.split('-').map(Number);
	if (!y || !m || !d) return -1;
	// Date.UTC 构造避免本地时区干扰；getUTCDay() 0=周日，转换成 0=周一
	return (new Date(Date.UTC(y, m - 1, d)).getUTCDay() + 6) % 7;
}

/** UTC 时刻 → 本地当日 00:00 起的分钟数。 */
function localMinuteOf(ts: string, tzOffsetMin: number): number | null {
	const ms = Date.parse(ts);
	if (!Number.isFinite(ms)) return null;
	const d = new Date(ms + tzOffsetMin * 60_000);
	return d.getUTCHours() * 60 + d.getUTCMinutes();
}

/** 汇总周期性节律。 */
export function computeRhythm(nights: RhythmNight[]): RhythmSummary {
	const buckets: { bed: number[]; wake: number[]; sleep: number[] }[] = Array.from(
		{ length: 7 },
		() => ({ bed: [], wake: [], sleep: [] })
	);
	const allBed: number[] = [];
	let missingBedtime = 0;

	for (const n of nights) {
		const idx = weekdayIndex(n.local_date);
		if (idx < 0) continue;
		const tz = n.tz_offset_min ?? 0;
		if (n.sleep_start) {
			const b = bedtimeMinutes(n.sleep_start, tz);
			if (b !== null) {
				buckets[idx].bed.push(b);
				allBed.push(b);
			} else {
				missingBedtime++;
			}
		} else {
			missingBedtime++;
		}
		if (n.sleep_end) {
			const w = localMinuteOf(n.sleep_end, tz);
			if (w !== null) buckets[idx].wake.push(w);
		}
		if (n.total_sleep_hr != null && n.total_sleep_hr > 0) {
			buckets[idx].sleep.push(n.total_sleep_hr);
		}
	}

	const byWeekday: WeekdayRhythm[] = buckets.map((b, index) => {
		const bedMed = circularMedian(b.bed);
		return {
			index,
			label: WEEKDAYS[index],
			nights: b.bed.length,
			bedtimeMin: bedMed,
			wakeMin: median(b.wake),
			sleepHr: median(b.sleep),
			bedtimeSpreadMin: bedMed === null ? null : spread(b.bed, bedMed)
		};
	});

	// 工作日 = 索引 0..4，周末 = 5..6
	const collect = (idxs: number[]) => idxs.flatMap((i) => buckets[i].bed);
	const weekdayBed = circularMedian(collect([0, 1, 2, 3, 4]));
	const weekendBed = circularMedian(collect([5, 6]));

	// 社交时差要在同一时间轴上比较，否则 23:50 与 00:10 会被算成差 20 小时
	let jetlag: number | null = null;
	if (weekdayBed !== null && weekendBed !== null) {
		jetlag = weekendBed + 1440 * Math.round((weekdayBed - weekendBed) / 1440) - weekdayBed;
	}

	const overallBed = circularMedian(allBed);
	return {
		byWeekday,
		weekdayBedtimeMin: weekdayBed,
		weekendBedtimeMin: weekendBed,
		socialJetlagMin: jetlag,
		bedtimeSpreadMin: overallBed === null ? null : spread(allBed, overallBed),
		nights: nights.length,
		missingBedtime
	};
}

/** 一晚的结构指标。 */
export type NightStructure = {
	local_date: string;
	/** 睡眠效率 0~1：在床时长里真正睡着（睡着）的比例 */
	efficiency: number | null;
	/** 深睡占比 0~1（占总睡眠） */
	deepRatio: number | null;
	remRatio: number | null;
	coreRatio: number | null;
	/** 入睡潜伏期（分钟）：上床到真正睡着。缺 in_bed_start 或数据可疑时为 null */
	latencyMin: number | null;
	sleepHr: number | null;
	inBedHr: number | null;
};

/** 计算单晚结构。缺字段时为 null，不猜。 */
export function structureOf(n: RhythmNight): NightStructure {
	const sleepHr = n.total_sleep_hr != null && n.total_sleep_hr > 0 ? n.total_sleep_hr : null;
	const inBedHr = n.in_bed_hr != null && n.in_bed_hr > 0 ? n.in_bed_hr : null;
	// 睡着时长优先用 asleep_hr；缺失时退回总睡眠时长（HAE 的 asleep 字段常缺）
	const asleepHr =
		n.asleep_hr != null && n.asleep_hr > 0
			? n.asleep_hr
			: sleepHr !== null
				? sleepHr
				: null;
	const ratio = (v: number | null | undefined) =>
		sleepHr && v != null && v > 0 ? v / sleepHr : null;

	// 潜伏期 = 入睡 − 上床。两个时刻都是 UTC RFC3339，直接相减与时区无关。
	// 负值（上床晚于入睡）只可能是脏数据，宁可不给也不显示一个负分钟。
	let latencyMin: number | null = null;
	if (n.sleep_start && n.in_bed_start) {
		const ms = Date.parse(n.sleep_start) - Date.parse(n.in_bed_start);
		if (Number.isFinite(ms) && ms >= 0) latencyMin = Math.round(ms / 60000);
	}
	return {
		local_date: n.local_date,
		// 效率的分母是「在床时长」；没有 in_bed 时不算（而不是拿睡眠时长除以自己 = 100%）
		efficiency:
			inBedHr !== null && asleepHr !== null ? Math.min(1, asleepHr / inBedHr) : null,
		deepRatio: ratio(n.deep_hr),
		remRatio: ratio(n.rem_hr),
		coreRatio: ratio(n.core_hr),
		latencyMin,
		sleepHr,
		inBedHr
	};
}

/** 区间内的结构均值（只统计有值的夜晚，并返回参与统计的样本数）。 */
export type StructureSummary = {
	efficiency: number | null;
	deepRatio: number | null;
	remRatio: number | null;
	coreRatio: number | null;
	sleepHr: number | null;
	/** 入睡潜伏期取中位数（个别失眠夜不应拉高整体印象），单位分钟 */
	latencyMin: number | null;
	/** 每项的样本数，界面据此说明「基于几晚」 */
	samples: {
		efficiency: number;
		deepRatio: number;
		remRatio: number;
		sleepHr: number;
		latency: number;
	};
};

export function summarizeStructure(nights: RhythmNight[]): StructureSummary {
	const all = nights.map(structureOf);
	const avg = (pick: (s: NightStructure) => number | null) => {
		const xs = all.map(pick).filter((v): v is number => v !== null);
		return xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : null;
	};
	const count = (pick: (s: NightStructure) => number | null) =>
		all.filter((s) => pick(s) !== null).length;
	const sleepHr = avg((s) => s.sleepHr);
	return {
		efficiency: avg((s) => s.efficiency),
		deepRatio: avg((s) => s.deepRatio),
		remRatio: avg((s) => s.remRatio),
		coreRatio: avg((s) => s.coreRatio),
		sleepHr,
		// 潜伏期用中位数：它经常长尾（偶尔失眠 40 分钟），均值会被少数夜晚带偏
		latencyMin: medianOf(all.map((s) => s.latencyMin)),
		samples: {
			efficiency: count((s) => s.efficiency),
			deepRatio: count((s) => s.deepRatio),
			remRatio: count((s) => s.remRatio),
			sleepHr: count((s) => s.sleepHr),
			latency: count((s) => s.latencyMin)
		}
	};
}

function medianOf(xs: (number | null)[]): number | null {
	const v = xs.filter((x): x is number => x !== null).sort((a, b) => a - b);
	if (v.length === 0) return null;
	const mid = v.length >> 1;
	return v.length % 2 ? v[mid] : (v[mid - 1] + v[mid]) / 2;
}

/** 记录覆盖情况：连续记录与断档。 */
export type Coverage = {
	/** 从最近一晚往前数的连续记录天数 */
	current: number;
	/** 区间内最长的连续记录天数 */
	longest: number;
	/** 有记录的天数 */
	total: number;
	/** 断档日期（只统计首末记录之间的空洞，不把「还没发生的夜晚」算成断档） */
	missing: string[];
};

/**
 * 统计记录覆盖。dates 为睡眠日集合（内部去重排序）。
 * 断档的口径：只看首条与末条记录之间的空洞——今天的觉还没睡，
 * 把「今天没记录」报成断档是误报。
 */
export function coverageStats(dates: string[]): Coverage {
	const unique = [...new Set(dates.filter(Boolean))].sort();
	if (unique.length === 0) {
		return { current: 0, longest: 0, total: 0, missing: [] };
	}
	const DAY = 86400000;
	const toMs = (d: string) => Date.parse(`${d}T00:00:00Z`);
	const missing: string[] = [];
	// 连续段长度：相邻日期差 1 天则延续，否则重开一段
	let longest = 1;
	let run = 1;
	for (let i = 1; i < unique.length; i++) {
		const gapDays = Math.round((toMs(unique[i]) - toMs(unique[i - 1])) / DAY);
		for (let k = 1; k < gapDays; k++) {
			const d = new Date(toMs(unique[i - 1]) + k * DAY);
			missing.push(d.toISOString().slice(0, 10));
		}
		run = gapDays === 1 ? run + 1 : 1;
		if (run > longest) longest = run;
	}
	// 当前连续：从末条往前数
	let current = 1;
	for (let i = unique.length - 1; i > 0; i--) {
		const gapDays = Math.round((toMs(unique[i]) - toMs(unique[i - 1])) / DAY);
		if (gapDays !== 1) break;
		current++;
	}
	return { current, longest, total: unique.length, missing };
}

/** 效率分档，用于给出可读的评价。 */
export function efficiencyBand(v: number | null): { label: string; color: string } | null {
	if (v === null) return null;
	const pct = v * 100;
	if (pct >= 95) return { label: '很好', color: '#0f6e56' };
	if (pct >= 90) return { label: '良好', color: '#2f8f6f' };
	if (pct >= 80) return { label: '一般', color: '#8a6d1f' };
	return { label: '偏低', color: '#a32d2d' };
}

/** 分钟 → 钟点 'HH:MM'（输入为本地当日 00:00 起的分钟）。 */
export function wakeClock(min: number | null): string {
	if (min === null) return '—';
	const m = ((Math.round(min) % 1440) + 1440) % 1440;
	return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}