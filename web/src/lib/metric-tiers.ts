// 指标的「档位」与「节奏」——决定一张卡画什么、怎么标注。
//
// 动机：指标字典里 51 个指标的更新频率差了三个数量级。步数一天数十条、
// 体重几个月一次、肺活量一年两次。如果所有指标共用一张 150px 的折线图，
// 那么稀疏指标画出来的三个点会被时间轴挤成相邻的柱，
// 「两个月没测」这个最重要的事实反而看不出来。
//
// 所以先按**区间内实际点数 n** 定档，再让每个档位用各自的形态表达：
//   密集 → 画趋势；稀疏 → 画间隔；只有一次 → 画记录；没有 → 不占版面。
//
// 节奏（多久测一次）由**相邻测量的中位间隔**算，与区间跨度无关——
// 用户把区间从 90 天切到 30 天，体重仍然是「每 42 天一次」。
//
// 本模块为纯展示层逻辑，不进入 API / 数据库契约。

/** 图形档位。 */
export type Tier = 'continuous' | 'points' | 'step' | 'single' | 'empty';

/** 档位切换的点数阈值。 */
export const TIER_THRESHOLD = {
  /** >= 该点数走连续趋势图（可再按周/月聚合） */
  continuous: 24,
  /** >= 该点数走点线图；低于它走阶梯图 */
  points: 6,
  /** >= 该点数才有得比；1 个点走单次面板 */
  step: 2
} as const;

/** 按区间内实际点数判定档位。 */
export function tierOf(n: number): Tier {
  if (!Number.isFinite(n) || n <= 0) return 'empty';
  if (n === 1) return 'single';
  if (n < TIER_THRESHOLD.points) return 'step';
  if (n < TIER_THRESHOLD.continuous) return 'points';
  return 'continuous';
}

/**
 * 点数够但节奏慢到这个间隔（天）以上，就不该画成连续趋势线。
 * 与节奏分档的「周级」上界对齐：每周及更慢的都按稀疏处理。
 */
const SPARSE_GAP_DAYS = 9;

/**
 * 综合点数与节奏定档。
 *
 * 只看点数会出错：6 个月测一次、共 6 个点的指标，点数上够「点线图」，
 * 但这 6 个点其实跨了大半年——连成折线会读成连续变化，而真实信息是
 * 「每次测完这个值保持了多久」。所以节奏慢的一律按稀疏档画阶梯。
 */
export function tierOfPoints(n: number, dates: string[]): Tier {
  const t = tierOf(n);
  if (t !== 'points') return t;
  const g = medianGap(dates);
  if (g !== null && g > SPARSE_GAP_DAYS) return 'step';
  return t;
}

/** 档位的图区高度：稀疏档把空间让给日期与间隔标注，不需要 150px。 */
export const TIER_CHART_HEIGHT: Record<Tier, number> = {
  continuous: 150,
  points: 150,
  step: 104,
  single: 0,
  empty: 0
};

/** 测量节奏。 */
export type Cadence = 'daily' | 'weekly' | 'monthly' | 'occasional';

export const CADENCE_LABEL: Record<Cadence, string> = {
  daily: '每天',
  weekly: '每周',
  monthly: '每月',
  occasional: '偶尔'
};

/** 节奏判定的中位间隔阈值（天）。 */
const CADENCE_MAX_GAP: Record<Cadence, number> = {
  daily: 2,
  weekly: 9,
  monthly: 40,
  occasional: Number.POSITIVE_INFINITY
};

/** 'YYYY-MM-DD' → 自纪元起的整数天序号（按 UTC 解析，规避时区/夏令时干扰）。 */
function dayNumber(date: string): number {
  const [y, m, d] = date.split('-').map(Number);
  if (!y || !m || !d) return Number.NaN;
  return Date.UTC(y, m - 1, d) / 86_400_000;
}

/** 相邻两个日期相差几天（同日 0）；解析失败返回 null。 */
export function gapDays(from: string, to: string): number | null {
  const a = dayNumber(from);
  const b = dayNumber(to);
  if (!Number.isFinite(a) || !Number.isFinite(b)) return null;
  return Math.round(b - a);
}

/** 相邻测量的间隔天数序列（升序日期输入，返回长度 = n-1）。 */
export function gapsOf(dates: string[]): number[] {
  const ds = [...new Set(dates)].sort();
  const out: number[] = [];
  for (let i = 1; i < ds.length; i++) {
    const g = gapDays(ds[i - 1], ds[i]);
    if (g !== null && g >= 0) out.push(g);
  }
  return out;
}

/** 中位间隔天数；不足两次测量时返回 null。 */
export function medianGap(dates: string[]): number | null {
  const gaps = gapsOf(dates);
  if (gaps.length === 0) return null;
  const s = [...gaps].sort((a, b) => a - b);
  const mid = s.length >> 1;
  return s.length % 2 ? s[mid] : Math.round((s[mid - 1] + s[mid]) / 2);
}

/** 按中位间隔判定节奏；不足两次测量时返回 null。 */
export function cadenceOf(dates: string[]): Cadence | null {
  const g = medianGap(dates);
  if (g === null) return null;
  if (g <= CADENCE_MAX_GAP.daily) return 'daily';
  if (g <= CADENCE_MAX_GAP.weekly) return 'weekly';
  if (g <= CADENCE_MAX_GAP.monthly) return 'monthly';
  return 'occasional';
}

/**
 * 节奏的中文说法。中位间隔是最有信息量的那句话，所以直接念数字，
 * 不套「每周」这种粗糙的分档——「每 42 天一次」比「每月」准确得多。
 */
export function cadenceText(dates: string[]): string | null {
  const g = medianGap(dates);
  if (g === null) return null;
  if (g <= 1) return '每天';
  return `每 ${g} 天一次`;
}

/** 该区间内是否覆盖了整整一年以上的历史，用于判断能否画年度密度条。 */
export function hasEnoughForStrip(n: number): boolean {
  return n >= 2;
}
