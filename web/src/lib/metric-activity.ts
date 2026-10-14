// 指标「活跃度」评估——决定趋势页卡片的排列顺序（对齐苹果健康的展示心智）。
//
// 动机：不是每个指标每天都有新数据。步数、心率这类设备自动回传的指标天天有，
// 而体重、血糖、肺活量往往是「偶尔测一次」。如果一律按固定顺序排列，那些
// 已经很久没有新数据的卡片会长期占着首屏位置，而真正在更新的指标被挤到后面。
//
// 评估只看两件事：
//   1. recency  —— 最近一次更新距今多少天（**主导项**，指数衰减）
//   2. coverage —— 历史更新的密度，即有数据的天数占窗口的比例（**次要项**）
// 最终得分 = recency × consistency，落在 0..1，越大越靠前。
//
// 关键约束：评估窗口与页面上选的时间范围（7/30/90/365 天）**完全解耦**，
// 固定为 ACTIVITY_WINDOW_DAYS。原因有二：
//   a) 否则用户一切换范围排序就跟着乱跳，位置不稳定；
//   b) 「最近 7 天」的窗口里根本看不出一张卡片是否已经三个月没更新了。
//
// 本模块为纯展示层逻辑，不进入 API / 数据库契约。

/** 活跃度评估窗口（天）。与展示范围解耦，保证排序稳定。 */
export const ACTIVITY_WINDOW_DAYS = 90;

/** 时间衰减尺度（天）：距今 N 天未更新时 recency = exp(-N/14)。
 *  14 天 → 0.37，30 天 → 0.12，60 天 → 0.014，90 天 → 0.0016。 */
const RECENCY_TAU = 14;

/** 达到该天数即提示「更新不新鲜」（低于此值不打扰）。 */
export const STALE_DAYS = 3;
/** 明显陈旧：提示加强。 */
export const VERY_STALE_DAYS = 14;

/** 无数据时的得分。 */
const MIN_SCORE = 0;

export type ActivityStats = {
	/** 综合活跃度 0..1，越大越靠前 */
	score: number;
	/** 距最近一次有数据的天数；窗口内完全没有数据时为 null */
	daysSince: number | null;
	/** 窗口内有数据的天数 */
	activeDays: number;
	/** 更新密度 = activeDays / 窗口天数，0..1 */
	coverage: number;
};

/** 无数据指标的占位统计。 */
export const NO_ACTIVITY: ActivityStats = { score: MIN_SCORE, daysSince: null, activeDays: 0, coverage: 0 };

/** 'YYYY-MM-DD' → 自纪元起的整数天序号（按 UTC 解析，规避时区/夏令时干扰）。 */
function dayNumber(date: string): number {
	const [y, m, d] = date.split('-').map(Number);
	if (!y || !m || !d) return Number.NaN;
	return Date.UTC(y, m - 1, d) / 86_400_000;
}

/** to - from 的天数差（同日为 0）。 */
export function daysBetween(from: string, to: string): number {
	return Math.round(dayNumber(to) - dayNumber(from));
}

/**
 * 由「窗口内的日级数据点」算出活跃度。
 * @param points 该指标在评估窗口内的日级点（乱序亦可）
 * @param today 今天的本地日期 'YYYY-MM-DD'
 * @param windowDays 评估窗口天数
 */
export function computeActivity(
	points: { date: string; value: number }[],
	today: string,
	windowDays = ACTIVITY_WINDOW_DAYS
): ActivityStats {
	let last = '';
	const seen = new Set<string>();
	for (const p of points) {
		if (!p?.date || !Number.isFinite(p.value)) continue;
		seen.add(p.date);
		if (p.date > last) last = p.date;
	}
	if (!last) return { ...NO_ACTIVITY };

	// 数据日期理论上不会晚于今天，但同步时钟偏差会导致跨天，钳到 0 以免 recency > 1。
	const daysSince = Math.max(0, daysBetween(last, today));
	const activeDays = seen.size;
	const coverage = windowDays > 0 ? Math.min(1, activeDays / windowDays) : 0;

	const recency = Math.exp(-daysSince / RECENCY_TAU);
	// 用 √coverage 压缩：低密度但「按自身节奏正常更新」的指标（如每周称一次体重）
	// 不至于被日更指标彻底压到底部，同时保证 0 覆盖时只剩基础权重。
	const consistency = 0.45 + 0.55 * Math.sqrt(coverage);

	return {
		// 量化到 3 位小数，避免浮点尾数在同一天的多次渲染间造成排序抖动
		score: Math.round(recency * consistency * 1000) / 1000,
		daysSince,
		activeDays,
		coverage
	};
}

/** 可参与活跃度排序的条目。 */
export type Rankable = {
	metric: string;
	/** 指标字典中的固定顺序，作为同分时的稳定兜底 */
	order: number;
	activity: ActivityStats;
};

/**
 * 按活跃度降序排列（返回新数组，不改动入参）。
 * 同分时依次回落：registry 的 sort_order → 指标 id，保证结果确定、不闪烁。
 */
export function rankByActivity<T extends Rankable>(items: T[]): T[] {
	return [...items].sort(
		(a, b) =>
			b.activity.score - a.activity.score ||
			a.order - b.order ||
			a.metric.localeCompare(b.metric)
	);
}

/** 一组卡片的平均活跃度——用于分区之间的排序。 */
export function meanScore(items: Rankable[]): number {
	if (items.length === 0) return 0;
	const sum = items.reduce((s, it) => s + it.activity.score, 0);
	return Math.round((sum / items.length) * 1000) / 1000;
}

/**
 * 陈旧度提示文案；未达到 STALE_DAYS 或窗口内无数据时返回 null
 * （无数据由卡片自身的空状态表达，无需重复提示）。
 */
export function staleLabel(a: ActivityStats): string | null {
	if (a.daysSince === null || a.daysSince < STALE_DAYS) return null;
	return `${a.daysSince} 天前更新`;
}

/** 是否为「明显陈旧」，用于加强提示样式。 */
export function isVeryStale(a: ActivityStats): boolean {
	return a.daysSince !== null && a.daysSince >= VERY_STALE_DAYS;
}
