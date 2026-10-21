<script module lang="ts">
  import type { MetricCoverage } from '#lib/metric-frequency';
  import type { ActivityStats } from '#lib/metric-activity';
  import type { Tier } from '#lib/metric-tiers';

  /** 列表视图的一行。图形与统计都在页面侧算好，这里只负责排布与排序。 */
  export type TrendRow = {
    metric: string;
    name: string;
    unit: string;
    digits: number;
    color: string;
    points: { date: string; value: number }[];
    /** 近一年的测量日期：密度条专用，与图表区间无关 */
    strip: string[];
    tier: Tier;
    cov: MetricCoverage | null;
    activity: ActivityStats;
    /** 节奏说法，如「每 42 天一次」；不足两次测量时为 null */
    cadence: string | null;
    baseline: number | null;
  };
</script>

<script lang="ts">
  import { spanDays, type Range } from '#lib/range';
  import { agoText } from '#lib/metric-frequency';
  import Sparkline from './Sparkline.svelte';
  import DensityStrip from './DensityStrip.svelte';

  // 列表视图：一次扫完全部指标。
  //
  // 这是本页承载「数十种指标」的主力视图。网格视图每张卡一个 ECharts 实例，
  // 51 个指标就是 51 个 canvas + 51 个 resize 监听，规模上不成立；
  // 这里每行只用一段原生 SVG：密集指标画迷你走势线，稀疏指标画密度条——
  // 后者把「多久测一次」直接画成刻度，比一条三点的假曲线诚实得多。

  type Props = {
    rows: TrendRow[];
    range: Range;
    loading?: boolean;
    onselect: (metric: string) => void;
  };

  let { rows, range, loading = false, onselect }: Props = $props();

  type SortKey = 'activity' | 'updated' | 'latest' | 'avg' | 'name';
  let sortKey = $state<SortKey>('activity');

  // 少于 3 个点不给均值：两个数的平均是噪声，不是统计
  const AVG_MIN = 3;

  function avgOf(r: TrendRow): number | null {
    if (r.points.length < AVG_MIN) return null;
    const s = r.points.reduce((acc, p) => acc + p.value, 0);
    return s / r.points.length;
  }

  let sorted = $derived.by(() => {
    const out = [...rows];
    out.sort((a, b) => {
      switch (sortKey) {
        case 'updated': {
          // 未接入的永远沉底；其余按「距今天数」升序，今天在最前
          const av = a.cov?.daysSince ?? Number.MAX_SAFE_INTEGER;
          const bv = b.cov?.daysSince ?? Number.MAX_SAFE_INTEGER;
          return av - bv || a.name.localeCompare(b.name, 'zh-CN');
        }
        case 'latest': {
          const av = a.points.at(-1)?.value ?? Number.NEGATIVE_INFINITY;
          const bv = b.points.at(-1)?.value ?? Number.NEGATIVE_INFINITY;
          return bv - av || a.name.localeCompare(b.name, 'zh-CN');
        }
        case 'avg': {
          // 算不出均值的（稀疏指标）排在有均值之后
          const av = avgOf(a);
          const bv = avgOf(b);
          if (av === null && bv === null) return a.name.localeCompare(b.name, 'zh-CN');
          if (av === null) return 1;
          if (bv === null) return -1;
          return bv - av || a.name.localeCompare(b.name, 'zh-CN');
        }
        case 'name':
          return a.name.localeCompare(b.name, 'zh-CN');
        default:
          return b.activity.score - a.activity.score || a.name.localeCompare(b.name, 'zh-CN');
      }
    });
    return out;
  });

  function fmt(v: number, digits: number): string {
    if (!Number.isFinite(v)) return '—';
    if (digits === 0 && Math.abs(v) >= 1000) return Math.round(v).toLocaleString('zh-CN');
    return v.toFixed(digits);
  }

  // 走势列的口径跟着区间走：写死「近 90 天」在切到 365 天时会说谎
  let cols = $derived([
    { key: 'name' as SortKey, label: '指标', width: 0 },
    { key: 'latest' as SortKey, label: '最新值', width: 92 },
    { key: 'avg' as SortKey, label: '区间均值', width: 76 },
    { key: 'updated' as SortKey, label: '更新于', width: 116 },
    { key: 'activity' as SortKey, label: `近 ${spanDays(range)} 天`, width: 100 }
  ]);
</script>

<div class="card overflow-hidden">
  <div class="flex items-center justify-between border-b border-[var(--color-line)] px-3 py-2">
    <span class="text-[12px] font-medium text-[var(--color-ink-700)]">
      列表 · <span class="num">{rows.length}</span> 项
    </span>
    <span class="text-[11px] text-[var(--color-ink-500)]">点表头排序，点行看大图</span>
  </div>

  <div
    class="flex items-center gap-2 border-b border-[var(--color-line)] bg-[var(--color-surface-2)] px-3 py-1.5 text-[11px] text-[var(--color-ink-500)]"
  >
    {#each cols as c (c.key)}
      <button
        class="text-left transition-colors hover:text-[var(--color-brand)] {sortKey === c.key
          ? 'text-[var(--color-brand)]'
          : ''} {c.width ? 'shrink-0 text-right' : 'min-w-0 flex-1'}"
        style={c.width ? `width:${c.width}px` : ''}
        onclick={() => (sortKey = c.key)}
      >
        {c.label}{sortKey === c.key ? ' ▾' : ''}
      </button>
    {/each}
  </div>

  {#if loading && rows.length === 0}
    <div class="flex h-[160px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">
      加载中…
    </div>
  {:else}
    {#each sorted as r (r.metric)}
      {@const last = r.points.at(-1)}
      {@const prev = r.points.at(-2)}
      {@const delta = last && prev ? last.value - prev.value : null}
      {@const avg = avgOf(r)}
      <button
        class="flex w-full items-center gap-2 border-b border-[var(--color-line)] px-3 py-2 text-left transition-colors last:border-b-0 hover:bg-[var(--color-surface-2)]"
        onclick={() => onselect(r.metric)}
      >
        <span class="min-w-0 flex-1 truncate text-[13px]">{r.name}</span>

        <span class="num shrink-0 text-right text-[13px]" style="width:92px">
          {#if last}
            {fmt(last.value, r.digits)}<span class="text-[10px] text-[var(--color-ink-500)]"
              >{r.unit ? ' ' + r.unit : ''}</span
            >
          {:else}
            <span class="text-[var(--color-ink-500)]">—</span>
          {/if}
          {#if delta !== null}
            <span class="num text-[10px] text-[var(--color-ink-500)]"
              >{delta > 0 ? '▲' : delta < 0 ? '▼' : '—'}{fmt(Math.abs(delta), r.digits)}</span
            >
          {/if}
        </span>

        <span
          class="num shrink-0 text-right text-[13px] text-[var(--color-ink-700)]"
          style="width:76px"
        >
          {#if avg !== null}
            {fmt(avg, r.digits)}
          {:else}
            <span class="text-[11px] text-[var(--color-ink-400)]" title="记录少于 3 次，均值无意义"
              >—</span
            >
          {/if}
        </span>

        <span
          class="shrink-0 text-right text-[11px] text-[var(--color-ink-500)]"
          style="width:116px"
        >
          {#if r.cov && r.cov.freq !== 'never'}
            {agoText(r.cov.daysSince) ?? '—'}
            <span class="block text-[10px] text-[var(--color-ink-500)]/70">
              {r.cadence ?? r.cov.lastDate}
            </span>
          {:else}
            <span class="text-[var(--color-ink-400)]">未接入</span>
          {/if}
        </span>

        <span class="shrink-0" style="width:100px">
          {#if r.tier === 'continuous' || r.tier === 'points'}
            <Sparkline points={r.points} height={22} color={r.color} baseline={r.baseline} />
          {:else if r.strip.length > 0}
            <!-- 密度条：恒用近一年的测量日期，不跟区间走。
                 跟区间走的话，切到短区间时稀疏指标只剩一两根刻度，
                 而区间外的回填点还会被挤到边缘堆成一团，画出根本不存在的节奏 -->
            <div>
              <DensityStrip dates={r.strip} color={r.color} height={18} />
              <div class="num text-right text-[10px] text-[var(--color-ink-500)]/70">
                近一年 {r.strip.length} 次
              </div>
            </div>
          {:else}
            <span class="block text-right text-[10px] text-[var(--color-ink-400)]">无记录</span>
          {/if}
        </span>
      </button>
    {/each}
  {/if}
</div>
