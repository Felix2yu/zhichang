// 趋势图的时间粒度与分桶。
//
// 从 sleep-buckets.ts 抽出并推广：睡眠页已经解决了「365 天画 365 根逐晚柱」的问题，
// 趋势页面对的是同一个问题（365 天画 365 根日柱），阈值必须与睡眠页一致，
// 否则同一个「近一年」在两个页面上的形状不一样，会被当成数据错了。
//
// 口径要点（最容易搞错的一点）：
//   瞬时量 → 桶内均值；累计量 → 桶内**日均**。两者的算式其实都是 sum ÷ 有数据的天数，
//   差的是**语义与标注**：一周的步数桶若仍标成「步数」，用户会把它读成 7 倍的日常值。
//   所以每个桶都带出 n（桶内有效天数），供坐标轴名与工具提示说明「这是 N 天的日均」。
//
// 本模块为纯展示层逻辑，不进入 API / 数据库契约。

/** 图表的时间粒度。 */
export type Grain = 'day' | 'week' | 'month';

export const GRAIN_LABEL: Record<Grain, string> = {
  day: '逐日',
  week: '按周',
  month: '按月'
};

/** 累计量分桶后的轴名后缀：不写这半个字，周桶会被当成 7 倍的日值。 */
export const DAILY_AVG_SUFFIX = '日均';

/**
 * 范围越长，一根柱子代表的天数越多。
 * 阈值与 sleep-buckets.ts 的 grainFor 保持同一套（45 / 180）。
 */
export function grainFor(days: number): Grain {
  if (days <= 45) return 'day';
  if (days <= 180) return 'week';
  return 'month';
}

/** 分桶结果。 */
export type Bucket = {
  /** 分桶键：日粒度是 YYYY-MM-DD，周粒度是周一日期，月粒度是 YYYY-MM */
  key: string;
  /** 桶内均值（累计量读作「桶内日均」） */
  value: number;
  /** 桶内实际有数据的天数；空桶不产出，所以恒 >= 1 */
  n: number;
};

/** 分桶键：周取「周一起始日」，月取 YYYY-MM，日粒度就是日期本身。 */
export function bucketKey(date: string, grain: Grain): string {
  if (grain === 'day') return date;
  const [y, m, d] = date.split('-').map(Number);
  if (!y || !m || !d) return date;
  const dt = new Date(Date.UTC(y, m - 1, d));
  if (Number.isNaN(dt.getTime())) return date;
  if (grain === 'month') return `${y}-${String(m).padStart(2, '0')}`;
  // 周一为一周之始：getUTCDay() 周日 = 0，所以要平移 6 而不是 7
  const shift = (dt.getUTCDay() + 6) % 7;
  dt.setUTCDate(dt.getUTCDate() - shift);
  return `${dt.getUTCFullYear()}-${String(dt.getUTCMonth() + 1).padStart(2, '0')}-${String(dt.getUTCDate()).padStart(2, '0')}`;
}

/**
 * 按粒度聚合。输入日期无需排序（内部升序归一）。
 *
 * 只统计有值的点：某天缺数据就少一个样本，而不是当成 0 拉低均值；
 * 整桶一个样本都没有则不出这一桶（不补空柱，避免在无数据的月份上插一根 0）。
 */
export function bucketSeries(points: { date: string; value: number }[], grain: Grain): Bucket[] {
  const valid = points
    .filter((p) => p?.date && Number.isFinite(p.value))
    .sort((a, b) => (a.date < b.date ? -1 : a.date > b.date ? 1 : 0));
  if (valid.length === 0) return [];
  if (grain === 'day') {
    return valid.map((p) => ({ key: p.date, value: p.value, n: 1 }));
  }
  const order: Bucket[] = [];
  const byKey = new Map<string, Bucket>();
  for (const p of valid) {
    const key = bucketKey(p.date, grain);
    let b = byKey.get(key);
    if (!b) {
      b = { key, value: 0, n: 0 };
      byKey.set(key, b);
      order.push(b);
    }
    b.value += p.value;
    b.n += 1;
  }
  for (const b of order) b.value = b.value / b.n;
  return order;
}

/** 桶键的展示标签：日粒度 05-12，周粒度 05-12（该周周一），月粒度 2026-05。 */
export function bucketLabel(key: string, grain: Grain): string {
  if (grain === 'month') {
    const [y, m] = key.split('-');
    return m ? `${y}-${m}` : key;
  }
  return key.length >= 10 ? key.slice(5) : key;
}
