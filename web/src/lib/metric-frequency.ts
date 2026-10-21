// 指标的数据频率档——趋势页指标选择器的主轴。
//
// 为什么不用苹果健康的 12 分类做主轴：分类回答的是「这是什么」，
// 而用户在这个页面真正要问的是「它有数据吗、多久更新一次」。
// 51 个指标里实际有数据的往往只有二十来个，按分类罗列会把没数据的那三十个
// 和天天更新的那些混在一起，用户必须点进去才知道是空的。
//
// 频率档由 /api/coverage 一次 GROUP BY 算出（days / last_date / days_since），
// 不需要为了排顺序再拉一次全量日级序列。
//
// 本模块为纯展示层逻辑，不进入 API / 数据库契约。

import type { CoverageItem } from './api';

/** 频率档。 */
export type FreqTier = 'daily' | 'regular' | 'occasional' | 'never';

export const FREQ_LABEL: Record<FreqTier, string> = {
  daily: '日更',
  regular: '定期',
  occasional: '偶发',
  never: '未接入'
};

export const FREQ_HINT: Record<FreqTier, string> = {
  daily: '设备自动回传，几乎每天都有',
  regular: '按周或按月记录',
  occasional: '偶尔测一次，间隔不定',
  never: '字典已登记，尚无数据'
};

/**
 * 评估窗口（天）。取一年，不取 90 天。
 *
 * 90 天窗口会把「上一次测量在 224 天前」的指标判成「从未接入」——
 * 肺活量一年测两次完全正常，把它和真正没接过数据的指标混在一起，
 * 恰恰是这个分区要解决的问题的反面。
 */
export const FREQ_WINDOW_DAYS = 365;

/**
 * 判定阈值按窗口比例给，不写死天数：窗口改成别的长度时结论才一致。
 *   daily   —— 窗口内半数以上天有数据
 *   regular —— 窗口内 1/15 以上的天有数据（365 天 ≈ 每月至少一次）
 */
export const FREQ_RATIO = {
  daily: 0.5,
  regular: 1 / 15
} as const;

/** 该窗口下「日更 / 定期」的天数门槛，供界面把口径写出来。 */
export function freqBounds(windowDays = FREQ_WINDOW_DAYS): { daily: number; regular: number } {
  return {
    daily: Math.round(windowDays * FREQ_RATIO.daily),
    regular: Math.round(windowDays * FREQ_RATIO.regular)
  };
}

/** 窗口内一天都没有 → 从未接入。 */
export function freqOf(days: number, windowDays = FREQ_WINDOW_DAYS): FreqTier {
  if (!Number.isFinite(days) || days <= 0) return 'never';
  if (days >= freqBounds(windowDays).daily) return 'daily';
  if (days >= freqBounds(windowDays).regular) return 'regular';
  return 'occasional';
}

/** 一个指标的覆盖情况；窗口内无数据时从未接入。 */
export type MetricCoverage = {
  metric: string;
  /** 窗口内有数据的天数 */
  days: number;
  /** 最后有数据的日期；未接入为空串 */
  lastDate: string;
  /** 距窗口末端多少天；未接入为 null */
  daysSince: number | null;
  freq: FreqTier;
};

/**
 * 把 /api/coverage 的行与指标字典合成为完整的覆盖表。
 *
 * coverage 只回「有数据的指标」（服务端不知道前端想看哪些），
 * 所以未接入的指标要靠字典做差集补齐——这正是界面需要区分的那一类。
 */
export function buildCoverage(
  defs: { metric: string }[],
  rows: CoverageItem[],
  windowDays = FREQ_WINDOW_DAYS
): Map<string, MetricCoverage> {
  const byMetric = new Map<string, CoverageItem>();
  for (const r of rows ?? []) byMetric.set(r.metric, r);

  const out = new Map<string, MetricCoverage>();
  for (const d of defs) {
    const r = byMetric.get(d.metric);
    const days = r?.days ?? 0;
    const daysSince = r && r.days_since >= 0 ? r.days_since : null;
    out.set(d.metric, {
      metric: d.metric,
      days,
      lastDate: r?.last_date ?? '',
      daysSince,
      freq: freqOf(days, windowDays)
    });
  }
  return out;
}

/** 「多久之前」的中文说法。0 = 今天；未接入返回 null。 */
export function agoText(daysSince: number | null): string | null {
  if (daysSince === null) return null;
  if (daysSince <= 0) return '今天';
  if (daysSince === 1) return '昨天';
  return `${daysSince} 天前`;
}
