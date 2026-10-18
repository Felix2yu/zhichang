/** 图表的时间粒度。 */
export type Grain = 'night' | 'week' | 'month';

/**
 * 范围越长，一根柱子代表的夜晚越多。
 * 365 天画 365 根逐晚柱会挤成一片，看不出趋势——Apple 的「6 个月」也是按周出柱。
 */
export function grainFor(days: number): Grain {
  if (days <= 45) return 'night';
  if (days <= 180) return 'week';
  return 'month';
}

export const GRAIN_LABEL: Record<Grain, string> = {
  night: '逐晚',
  week: '按周均值',
  month: '按月均值'
};

/** 分桶键：周取「周一起始日」，月取 YYYY-MM，逐晚就是日期本身。 */
function bucketKey(date: string, grain: Grain): string {
  if (grain === 'night') return date;
  const [y, m, d] = date.split('-').map(Number);
  const dt = new Date(y, (m ?? 1) - 1, d ?? 1);
  if (Number.isNaN(dt.getTime())) return date;
  if (grain === 'month') {
    return `${dt.getFullYear()}-${String(dt.getMonth() + 1).padStart(2, '0')}`;
  }
  // 周一为一周之始（getDay(): 周日=0）
  const shift = (dt.getDay() + 6) % 7;
  dt.setDate(dt.getDate() - shift);
  return `${dt.getFullYear()}-${String(dt.getMonth() + 1).padStart(2, '0')}-${String(dt.getDate()).padStart(2, '0')}`;
}

/**
 * 按粒度求均值。dates 需升序（与 rows 同序），values[i] 与 dates[i] 一一对应。
 *
 * 只统计有值的夜晚：某晚缺数据就少一个样本，而不是当成 0 拉低均值；
 * 整桶一个样本都没有则不出这一桶（不补空柱）。
 */
export function bucketMean(
  dates: string[],
  values: (number | null | undefined)[],
  grain: Grain
): { date: string; value: number }[] {
  type acc = { key: string; sum: number; n: number };
  const order: acc[] = [];
  const byKey = new Map<string, acc>();
  for (let i = 0; i < dates.length; i++) {
    const v = values[i];
    if (v == null || Number.isNaN(v)) continue;
    const key = bucketKey(dates[i], grain);
    let a = byKey.get(key);
    if (!a) {
      a = { key, sum: 0, n: 0 };
      byKey.set(key, a);
      order.push(a);
    }
    a.sum += v;
    a.n += 1;
  }
  return order.map((a) => ({ date: a.key, value: a.sum / a.n }));
}
