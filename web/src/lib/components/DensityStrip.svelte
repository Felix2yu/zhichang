<script lang="ts">
  import { shiftDays, todayStr } from '#lib/api';
  import { dataColor } from '#lib/theme.svelte';

  // 年度密度条：近 365 天，每次测量一根刻度。
  //
  // 稀疏指标最想回答的问题是「这个指标多久测一次」，而这个问题与用户当前
  // 看的是 7 天还是 365 天无关——所以这条的窗口恒定为「近一年」，不跟区间走。
  // 跟区间走会有两个后果，都是真会发生的：
  //   1. 切到 7 天区间时，稀疏指标在区间内只剩 0~1 次记录，密度条信息归零；
  //   2. 区间外的回填点（回溯最近 6 次拿到的）会被钳到边缘，
  //      六七根刻度全堆在最左端，画出一个根本不存在的密集节奏。
  //
  // 因此这里对窗口外的日期**直接不画**，而不是钳到 0/100。

  type Props = {
    /** 测量日期（YYYY-MM-DD，无需排序，可重复） */
    dates: string[];
    /** 主色（十六进制）；深色模式下会自动提亮 */
    color?: string;
    /** 图形高度（px） */
    height?: number;
    /** 窗口天数 */
    windowDays?: number;
    /** 窗口末端日期；留空取今天 */
    to?: string;
  };

  let { dates, color = '#8a8f98', height = 14, windowDays = 365, to = '' }: Props = $props();

  let end = $derived(to || todayStr());
  let start = $derived(shiftDays(-(windowDays - 1), end));

  function dayNum(d: string): number {
    const [y, m, dd] = d.split('-').map(Number);
    if (!y || !m || !dd) return Number.NaN;
    return Date.UTC(y, m - 1, dd) / 86_400_000;
  }

  /**
   * 日期在条上的横向百分比；落在窗口外返回 null（不画，不钳边）。
   * 按 UTC 整天算，规避时区与夏令时。
   */
  function xOf(date: string): number | null {
    const a = dayNum(start);
    const b = dayNum(end);
    const c = dayNum(date);
    if (!Number.isFinite(a) || !Number.isFinite(b) || !Number.isFinite(c) || b <= a) return null;
    const t = ((c - a) / (b - a)) * 100;
    return t < 0 || t > 100 ? null : t;
  }

  let xs = $derived(
    [...new Set(dates)]
      .map(xOf)
      .filter((x): x is number => x !== null)
      .sort((a, b) => a - b)
  );

  let mid = $derived(height / 2);
  let tickTop = $derived(Math.max(1, mid - height * 0.32));
  let tickBottom = $derived(Math.min(height - 1, mid + height * 0.32));
</script>

{#if xs.length > 0}
  <svg
    class="block w-full"
    style="height:{height}px"
    viewBox="0 0 100 {height}"
    preserveAspectRatio="none"
    aria-hidden="true"
  >
    <line
      x1="0"
      y1={mid}
      x2="100"
      y2={mid}
      stroke="var(--color-line-strong)"
      stroke-width="1"
      stroke-dasharray="3 2"
      vector-effect="non-scaling-stroke"
    />
    {#each xs as x, i (i)}
      <line
        x1={x}
        y1={tickTop}
        x2={x}
        y2={tickBottom}
        stroke={dataColor(color)}
        stroke-width="2"
        vector-effect="non-scaling-stroke"
      />
    {/each}
  </svg>
{/if}
