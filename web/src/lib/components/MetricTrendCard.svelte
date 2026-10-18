<script lang="ts">
  import { onMount } from 'svelte';
  import * as echarts from 'echarts';
  import { dataColor, palette } from '#lib/theme.svelte';

  // 单指标趋势卡：一张卡只承载一个指标、一张图、一套坐标轴与单位。
  // 这是趋势页的展示原子——分组只决定卡片的版面归属，绝不把多指标塞进同一张图。
  type Props = {
    name: string;
    unit: string;
    /** 数据点（按日期升序） */
    points: { date: string; value: number }[];
    /** 图型：累计量/类别量用柱，瞬时量用线 */
    chartType: 'bar' | 'line';
    /** 主色（取自所属分类的 tint） */
    color: string;
    /** 数值小数位 */
    digits: number;
    /** 口径说明：日内聚合 · 跨来源聚合 */
    aggNote: string;
    loading?: boolean;
    /** 统计区间天数，用于空数据文案 */
    days?: number;
    /** 久未更新的提示文案（如「12 天前更新」）；null 表示不提示 */
    staleText?: string | null;
    /** 陈旧提示是否加强（明显陈旧） */
    staleStrong?: boolean;
    /** 透传类名（网格项用 h-full 拉平同排卡片高度） */
    class?: string;
  };

  let {
    name,
    unit,
    points,
    chartType,
    color,
    digits,
    aggNote,
    loading = false,
    days = 30,
    staleText = null,
    staleStrong = false,
    class: klass = ''
  }: Props = $props();

  let el: HTMLDivElement | undefined = $state();
  let chart: echarts.ECharts | undefined;

  // —— 数值格式化 ——
  function fmt(v: number): string {
    if (digits === 0 && Math.abs(v) >= 1000) return Math.round(v).toLocaleString('zh-CN');
    return v.toFixed(digits);
  }
  // Y 轴刻度：按本图数据最大值统一量纲——上万整轴用「万」，否则千分位。
  // 避免同一坐标轴里「1万」与「5,000」混排。
  function axisFmt(maxAbs: number): (v: number) => string {
    if (maxAbs >= 10000) {
      return (v: number) => {
        if (v === 0) return '0';
        const n = v / 10000;
        return `${Number.isInteger(n) ? n : +n.toFixed(1)}万`;
      };
    }
    return (v: number) => Math.round(v).toLocaleString('zh-CN');
  }

  // —— 统计量 ——
  let stats = $derived.by(() => {
    const vs = points.map((p) => p.value).filter((v) => Number.isFinite(v));
    if (vs.length === 0) return null;
    const sum = vs.reduce((s, v) => s + v, 0);
    return {
      avg: sum / vs.length,
      max: Math.max(...vs),
      min: Math.min(...vs),
      latest: vs[vs.length - 1]
    };
  });
  // 环比上一日（仅在有 >=2 个点时展示）
  let delta = $derived.by(() => {
    const vs = points.map((p) => p.value).filter((v) => Number.isFinite(v));
    if (vs.length < 2) return null;
    return vs[vs.length - 1] - vs[vs.length - 2];
  });

  let hasData = $derived(points.length > 0 && stats !== null);
  let maxAbs = $derived.by(() => points.reduce((m, p) => Math.max(m, Math.abs(p.value)), 0));

  function buildOption(): echarts.EChartsOption {
    const p = palette();
    const af = axisFmt(maxAbs);
    return {
      animationDuration: 260,
      grid: { left: 4, right: 6, top: 8, bottom: 4, containLabel: true },
      tooltip: {
        trigger: 'axis',
        backgroundColor: p.surface,
        borderColor: p.line,
        borderWidth: 1,
        textStyle: { color: p.ink, fontSize: 12 },
        axisPointer: {
          type: chartType === 'bar' ? 'shadow' : 'line',
          lineStyle: { color: p.line }
        },
        formatter: (params) => {
          const arr = Array.isArray(params) ? params : [params];
          if (arr.length === 0) return '';
          const pp = arr[0] as { axisValue?: string; value?: number; marker?: string };
          const date = pp.axisValue ?? '';
          return `<div style="font-size:11px;color:${p.inkSoft};margin-bottom:2px">${date}</div>
						<div style="font-size:12px;color:${p.ink}">${pp.marker ?? ''}${name} <b>${fmt(Number(pp.value ?? 0))}${unit ? ' ' + unit : ''}</b></div>`;
        }
      },
      xAxis: {
        type: 'category',
        data: points.map((p) => p.date.slice(5)),
        boundaryGap: chartType === 'bar',
        axisLine: { lineStyle: { color: p.line } },
        axisTick: { show: false },
        axisLabel: { color: p.inkFaint, fontSize: 10, hideOverlap: true }
      },
      yAxis: {
        type: 'value',
        scale: chartType === 'line',
        splitNumber: 3,
        axisLine: { show: false },
        axisTick: { show: false },
        splitLine: { lineStyle: { color: p.split } },
        axisLabel: { color: p.inkFaint, fontSize: 10, formatter: (v: number) => af(v) }
      },
      series: [
        {
          type: chartType,
          data: points.map((p) => p.value),
          smooth: chartType === 'line',
          symbol: 'circle',
          symbolSize: 3,
          showSymbol: false,
          lineStyle: { width: 1.8, color: dataColor(color) },
          itemStyle: {
            color: dataColor(color),
            borderRadius: chartType === 'bar' ? [3, 3, 0, 0] : 0
          },
          barMaxWidth: 14,
          areaStyle: chartType === 'line' ? { color: dataColor(color), opacity: 0.08 } : undefined
        }
      ]
    };
  }

  onMount(() => {
    if (!el) return;
    chart = echarts.init(el, undefined, { renderer: 'canvas' });
    if (hasData) chart.setOption(buildOption());
    const onResize = () => chart?.resize();
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      chart?.dispose();
      chart = undefined;
    };
  });

  // 数据/图型变化时重绘（空数据时不初始化图形）
  $effect(() => {
    if (!chart) return;
    if (hasData) chart.setOption(buildOption(), { notMerge: true });
    else chart.clear();
  });
</script>

<div class="card flex flex-col p-3 {klass}">
  <!-- 卡头：指标名 + 单位徽标 · 最新值 + 环比 -->
  <div class="flex items-start justify-between gap-2">
    <div class="flex min-w-0 items-center gap-1.5">
      <span class="truncate text-[13px] font-medium">{name}</span>
      <span
        class="shrink-0 rounded bg-[var(--color-line)]/60 px-1.5 py-0.5 text-[10px] text-[var(--color-ink-500)]"
        >{unit || '—'}</span
      >
    </div>
    <div class="shrink-0 text-right">
      {#if loading}
        <div class="ml-auto h-[16px] w-14 animate-pulse rounded bg-[var(--color-line)]"></div>
      {:else if hasData && stats}
        <div class="num text-[15px] leading-tight font-medium">{fmt(stats.latest)}</div>
        {#if delta !== null}
          <div class="text-[11px] text-[var(--color-ink-500)]">
            {delta > 0 ? '▲' : delta < 0 ? '▼' : '—'}
            {fmt(Math.abs(delta))}
          </div>
        {/if}
      {:else}
        <div class="text-[13px] text-[var(--color-ink-500)]">—</div>
      {/if}
    </div>
  </div>

  <!-- 图形区 -->
  <div class="relative mt-2">
    <div
      bind:this={el}
      style="height:150px;width:100%;"
      class={loading || !hasData ? 'invisible' : ''}
    ></div>
    {#if loading}
      <div class="absolute inset-0 flex items-center justify-center">
        <div class="h-[110px] w-full animate-pulse rounded bg-[var(--color-line)]/50"></div>
      </div>
    {:else if !hasData}
      <div class="absolute inset-0 flex flex-col items-center justify-center gap-1">
        <span class="text-[12px] text-[var(--color-ink-500)]">暂无数据</span>
        <span class="text-[11px] text-[var(--color-ink-500)]/70">近 {days} 天无同步记录</span>
      </div>
    {/if}
  </div>

  <!-- 卡脚：均值 / 最高 / 最低 + 口径 -->
  <div class="mt-2 border-t border-[var(--color-line)] pt-2">
    {#if hasData && stats}
      <div class="num flex items-center justify-between text-[11px] text-[var(--color-ink-500)]">
        <span>均 <b class="font-medium text-[var(--color-ink-700)]">{fmt(stats.avg)}</b></span>
        <span>高 <b class="font-medium text-[var(--color-ink-700)]">{fmt(stats.max)}</b></span>
        <span>低 <b class="font-medium text-[var(--color-ink-700)]">{fmt(stats.min)}</b></span>
      </div>
    {/if}
    <!-- 口径说明 + 陈旧提示：解释该卡片为何在排序中被后移 -->
    <div class="mt-1 flex items-center gap-2">
      <div
        class="min-w-0 flex-1 truncate text-[10px] text-[var(--color-ink-500)]/80"
        title={aggNote}
      >
        {aggNote}
      </div>
      {#if staleText}
        <span
          class="shrink-0 rounded px-1.5 py-0.5 text-[10px] {staleStrong
            ? 'bg-[var(--color-warn)]/15 text-[var(--color-warn)]'
            : 'bg-[var(--color-line)]/60 text-[var(--color-ink-500)]'}"
          title="该指标久未更新，活跃度低，排序时自动后移">{staleText}</span
        >
      {/if}
    </div>
  </div>
</div>
