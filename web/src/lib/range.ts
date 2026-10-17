import { shiftDays, todayStr } from './api';

/**
 * 区间：一对闭区间日期（含两端）。
 *
 * 之前各页只有「最近 7/30/90/365 天」且结束日锚定今天，问不出
 * 「2026 年 3 月那两周」或「整个自然年」这种问题；而所有页面都在
 * 自己拼 `shiftDays(-days + 1, today)`，一旦要支持自定义就得各改一遍。
 * 所以把「区间」提成一个值：页面只拿 from/to，天数是它的派生量。
 */
export type Range = { from: string; to: string };

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

/** 是否是一个合法的 YYYY-MM-DD（不校验日期是否存在，交给后端与浏览器控件）。 */
export function isDate(s: string): boolean {
  return DATE_RE.test(s);
}

/** 最近 n 天（含今天）。 */
export function lastDays(n: number, today = todayStr()): Range {
  return { from: shiftDays(-n + 1, today), to: today };
}

/** 今年 1 月 1 日至今：年度回顾的默认口径。 */
export function thisYear(today = todayStr()): Range {
  return { from: `${today.slice(0, 4)}-01-01`, to: today };
}

/** 区间的天数（含两端）；解析不了按 1 天，避免 NaN 渗进分桶与坐标轴。 */
export function spanDays(r: Range): number {
  const a = Date.parse(`${r.from}T00:00:00Z`);
  const b = Date.parse(`${r.to}T00:00:00Z`);
  if (!Number.isFinite(a) || !Number.isFinite(b) || b < a) return 1;
  return Math.round((b - a) / 86_400_000) + 1;
}

/**
 * 校正一对用户输入的日期：任一端非法就退回原区间，
 * 起止颠倒时交换——「从 10 号到 5 号」是笔误而不是错误，替他改对。
 */
export function normalizeRange(r: Range, fallback: Range): Range {
  if (!isDate(r.from) || !isDate(r.to)) return fallback;
  return r.from <= r.to ? r : { from: r.to, to: r.from };
}

/** 区间预设（天数与「今年」用哨兵区分；哨兵必须是负数，真实天数不可能为负）。 */
export const THIS_YEAR = -366;

/** 把预设换算成区间。 */
export function presetRange(preset: number, today = todayStr()): Range {
  return preset === THIS_YEAR ? thisYear(today) : lastDays(preset, today);
}

/** 当前区间命中的预设（自定义到非预设范围时返回 undefined，控件据此高亮「自定义」）。 */
export function matchPreset(r: Range, presets: number[], today = todayStr()): number | undefined {
  return presets.find((p) => {
    const q = presetRange(p, today);
    return q.from === r.from && q.to === r.to;
  });
}
