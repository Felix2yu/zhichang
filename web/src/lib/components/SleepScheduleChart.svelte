<script lang="ts">
  import { onMount } from 'svelte';
  import * as echarts from 'echarts';
  import { STAGE_META, stageLabel, stageMeta } from '#lib/sleep-stages';
  import { dataColor, palette } from '#lib/theme.svelte';
  import type { NightStages } from '#lib/api';

  type Props = {
    /** 每晚一行；缺失的夜晚传空数组，画成留白而不是跳过（断档要看得见） */
    nights: { date: string; segments: NightStages['segments'] }[];
    height?: number;
    /** 点某一行 = 查看那一晚 */
    onselect?: (date: string) => void;
  };

  let { nights, height = 0, onselect }: Props = $props();

  let el: HTMLDivElement | undefined = $state();
  let chart: echarts.ECharts | undefined;
  let width = $state(0);

  const EVENING_ANCHOR = 18 * 60;

  /**
   * 横轴用「相对 18:00 的分钟」，这样每一晚都从同一个就寝时段起点开始，
   * 23:30 与凌晨 03:00 分别落在 330 与 540，跨零点不会把行切成两段。
   * 午睡（早于 18:00 的那一觉）不属于这张图，直接不画。
   */
  function rel(localMin: number): number {
    return localMin - EVENING_ANCHOR;
  }

  const rows = $derived(
    nights.map((n) => ({
      date: n.date,
      segs: n.segments
        .map((s) => ({ ...s, x0: rel(s.local_min), x1: rel(s.local_min + s.duration_min) }))
        // 完全落在本夜窗口外的（午睡、次日下午）不画；跨界的裁进 0~1440
        .filter((s) => s.x1 > 0 && s.x0 < 1440)
        .map((s) => ({ ...s, x0: Math.max(0, s.x0), x1: Math.min(1440, s.x1) }))
    }))
  );

  const hasData = $derived(rows.some((r) => r.segs.length > 0));

  const domain = $derived.by(() => {
    let lo = 0;
    let hi = 1440;
    const xs: number[] = [];
    for (const r of rows) for (const s of r.segs) xs.push(s.x0, s.x1);
    if (xs.length > 0) {
      lo = Math.floor(Math.min(...xs) / 60) * 60;
      hi = Math.ceil(Math.max(...xs) / 60) * 60;
    }
    return { lo, hi: Math.max(hi, lo + 120) };
  });

  /** 刻度疏密：窄屏 2 小时会粘成一片，退到 4 小时 */
  const tickInterval = $derived(width > 0 && width < 460 ? 240 : 120);

  function clock(min: number): string {
    const m = ((Math.round(min) % 1440) + 1440) % 1440;
    return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
  }

  function buildOption(): echarts.EChartsOption {
    const p = palette();
    // 新夜在上：与下方明细表的排序方向一致
    const labels = rows.map((r) => r.date.slice(5)).reverse();
    return {
      animation: false,
      grid: { left: 52, right: 16, top: 8, bottom: 26 },
      tooltip: {
        trigger: 'item',
        backgroundColor: p.surface,
        borderColor: p.line,
        borderWidth: 1,
        textStyle: { color: p.ink, fontSize: 12 },
        formatter: (params) => {
          const d = (params as { data?: number[] }).data;
          if (!d) return '';
          const row = rows[rows.length - 1 - d[0]];
          const seg = row?.segs[d[1]];
          if (!row || !seg) return '';
          const mins = Math.round(seg.duration_min);
          return `<div style="font-size:12px;color:${p.ink};line-height:1.6">
						<b>${stageLabel(seg.stage)}</b> · ${row.date}<br/>
						<span style="color:${p.inkSoft}">${clock(seg.x0 + EVENING_ANCHOR)} – ${clock(
              seg.x1 + EVENING_ANCHOR
            )}</span><br/>
						${mins >= 60 ? `${(mins / 60).toFixed(1)} 小时` : `${mins} 分钟`}
					</div>`;
        }
      },
      xAxis: {
        type: 'value',
        min: domain.lo,
        max: domain.hi,
        interval: tickInterval,
        axisLine: { show: false },
        axisTick: { show: false },
        splitLine: { show: true, lineStyle: { color: p.split } },
        axisLabel: {
          color: p.inkFaint,
          fontSize: 10,
          formatter: (v: number) => clock(v + EVENING_ANCHOR)
        }
      },
      yAxis: {
        type: 'category',
        data: labels,
        inverse: true,
        axisLine: { show: false },
        axisTick: { show: false },
        axisLabel: { color: p.inkSoft, fontSize: 10 },
        splitLine: { show: false }
      },
      series: [
        {
          type: 'custom',
          renderItem: (params, api) => {
            const rowIdx = Number(api.value(0));
            const segIdx = Number(api.value(1));
            const row = rows[rows.length - 1 - rowIdx];
            const seg = row?.segs[segIdx];
            if (!seg) return null;
            const laneH = api.size?.([0, 1]);
            const h = Math.max(4, (Array.isArray(laneH) ? laneH[1] : (laneH as number)) * 0.62);
            const y = api.coord([0, rowIdx])[1] as number;
            const x0 = api.coord([seg.x0, rowIdx])[0] as number;
            const x1 = api.coord([seg.x1, rowIdx])[0] as number;
            return {
              type: 'rect',
              shape: { x: x0, y: y - h / 2, width: Math.max(1.5, x1 - x0), height: h, r: 2 },
              style: { fill: dataColor(stageMeta(seg.stage).color ?? p.inkFaint) }
            };
          },
          // data: [行号（y 轴序号，新夜在前）, 段号]
          data: rows.flatMap((r, ri) => {
            const rowIdx = rows.length - 1 - ri;
            return r.segs.map((_, si) => [rowIdx, si]);
          })
        }
      ]
    };
  }

  onMount(() => {
    if (!el) return;
    chart = echarts.init(el, undefined, { renderer: 'canvas' });
    chart.setOption(buildOption());
    chart.on('click', (params) => {
      const d = (params as { data?: number[] }).data;
      if (!Array.isArray(d)) return;
      const row = rows[rows.length - 1 - d[0]];
      if (row) onselect?.(row.date);
    });
    const ro = new ResizeObserver(() => {
      width = el?.clientWidth ?? 0;
      chart?.resize();
    });
    ro.observe(el);
    width = el.clientWidth;
    return () => {
      ro.disconnect();
      chart?.dispose();
      chart = undefined;
    };
  });

  $effect(() => {
    void rows;
    void domain;
    void width;
    if (chart) chart.setOption(buildOption(), { notMerge: true });
  });

  /** 图例：只列真实出现的阶段 */
  const legend = $derived(
    STAGE_META.filter((m) => m.drawn && rows.some((r) => r.segs.some((s) => s.stage === m.key)))
  );
  const chartHeight = $derived(height || Math.max(160, rows.length * 13 + 34));
</script>

{#if hasData}
  <div bind:this={el} style="height:{chartHeight}px;width:100%;"></div>
  <div
    class="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-[var(--color-ink-500)]"
  >
    {#each legend as m (m.key)}
      <span class="inline-flex items-center gap-1.5">
        <span class="inline-block h-2 w-2 rounded-[2px]" style="background:{dataColor(m.color)}"
        ></span>
        {m.label}
      </span>
    {/each}
  </div>
{:else}
  <div class="py-8 text-center text-[13px] text-[var(--color-ink-500)]">暂无分期数据</div>
{/if}
