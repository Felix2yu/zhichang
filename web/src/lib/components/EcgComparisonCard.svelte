<script lang="ts">
  import type { EcgComparison, EcgDelta } from '#lib/api';

  type Props = {
    comparison: EcgComparison;
  };

  let { comparison }: Props = $props();

  // 每行的展示口径：单位、小数位、增减方向（up=越高越好 / down=越低越好 / neutral=中性）
  const ROWS: {
    key: keyof EcgDelta;
    label: string;
    unit: string;
    digits: number;
    better: 'up' | 'down' | 'neutral';
    /** 比值类（0-1）要先乘 100 再展示 */
    percent?: boolean;
  }[] = [
    { key: 'avg_hr', label: '平均心率', unit: 'bpm', digits: 0, better: 'neutral' },
    {
      key: 'sinus_ratio',
      label: '窦性心律占比',
      unit: '%',
      digits: 0,
      better: 'up',
      percent: true
    },
    { key: 'hrv', label: '心率变异性', unit: '分', digits: 0, better: 'up' },
    { key: 'stress', label: '精神压力', unit: '分', digits: 0, better: 'down' },
    { key: 'fatigue', label: '疲劳指数', unit: '分', digits: 0, better: 'down' },
    { key: 'risk', label: '风险关注度', unit: '分', digits: 0, better: 'down' }
  ];

  function fmt(v: number | null | undefined, digits: number, percent = false): string {
    if (v === null || v === undefined) return '—';
    const n = percent ? v * 100 : v;
    return n.toFixed(digits);
  }

  /** 增减箭头 + 颜色：按「这个指标变大是好是坏」上色，而不是一律红涨绿跌 */
  function deltaView(
    v: number | null | undefined,
    better: 'up' | 'down' | 'neutral'
  ): { text: string; cls: string } {
    if (v === null || v === undefined) return { text: '—', cls: 'text-[var(--color-ink-500)]' };
    const rounded = Math.abs(v) < 0.005 ? 0 : v;
    if (rounded === 0) return { text: '±0', cls: 'text-[var(--color-ink-500)]' };
    const arrow = rounded > 0 ? '▲' : '▼';
    const good = better === 'up' ? rounded > 0 : better === 'down' ? rounded < 0 : null;
    const cls =
      good === null
        ? 'text-[var(--color-ink-700)]'
        : good
          ? 'text-[var(--color-brand)]'
          : 'text-[var(--color-danger)]';
    return { text: arrow, cls };
  }

  function deltaNum(v: number | null | undefined, digits: number, percent = false): string {
    if (v === null || v === undefined) return '';
    const n = percent ? v * 100 : v;
    return Math.abs(n).toFixed(digits);
  }
</script>

<div class="flex items-baseline justify-between">
  <div class="text-[13px] font-medium">对比</div>
  <div class="text-[11px] text-[var(--color-ink-500)]">与上一个 {comparison.window_days} 天</div>
</div>

{#if comparison.previous === null || comparison.previous.count === 0}
  <div class="py-8 text-center text-[13px] text-[var(--color-ink-500)]">
    上一个 {comparison.window_days} 天没有测量记录，无法对比
  </div>
{:else if comparison.current}
  <div class="mt-3 overflow-hidden">
    <table class="w-full text-[12px]">
      <thead class="text-[11px] text-[var(--color-ink-500)]">
        <tr>
          <th class="py-1.5 text-left font-normal">指标</th>
          <th class="py-1.5 text-right font-normal">本周期</th>
          <th class="py-1.5 text-right font-normal">上周期</th>
          <th class="py-1.5 text-right font-normal">变化</th>
        </tr>
      </thead>
      <tbody>
        {#each ROWS as row (row.key)}
          <tr class="border-t border-[var(--color-line)]">
            <td class="py-1.5">{row.label}</td>
            <td class="num py-1.5 text-right">
              {fmt(comparison.current[row.key], row.digits, row.percent)}{row.percent ? '%' : ''}
            </td>
            <td class="num py-1.5 text-right text-[var(--color-ink-500)]">
              {fmt(comparison.previous[row.key], row.digits, row.percent)}{row.percent ? '%' : ''}
            </td>
            <td
              class="num py-1.5 text-right {deltaView(comparison.delta?.[row.key], row.better).cls}"
            >
              {deltaView(comparison.delta?.[row.key], row.better).text}
              {deltaNum(comparison.delta?.[row.key], row.digits, row.percent)}{row.percent
                ? ' pp'
                : ''}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <div class="mt-2 text-[11px] text-[var(--color-ink-500)]">
    变化列按指标方向上色：绿色代表变好、红色代表变差；心率为中性指标
  </div>
{/if}
