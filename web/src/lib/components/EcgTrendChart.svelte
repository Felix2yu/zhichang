<script lang="ts">
  import { onMount } from 'svelte';
  import * as echarts from 'echarts';
  import { dataColor, palette } from '#lib/theme.svelte';
  import Segmented from '#lib/components/Segmented.svelte';
  import { codeColor } from '#lib/ecg-levels';
  import type { EcgAnalysisRecord } from '#lib/api';

  type Props = {
    /** 按时间升序的记录 */
    records: EcgAnalysisRecord[];
    /** 当前选中的记录 id（趋势与底部波形联动） */
    selectedId: number | null;
    /** 点击数据点：下钻到该次测量的波形 */
    onselect: (id: number) => void;
    height?: number;
  };

  let { records, selectedId, onselect, height = 240 }: Props = $props();

  // 指标切换：心率与三个指数共用一张图，纵轴量纲不同所以一次只画一个。
  type TrendKey = 'hr' | 'hrv' | 'stress' | 'fatigue';
  let metric = $state<TrendKey>('hr');

  const METRICS: { key: TrendKey; label: string; unit: string; color: string; max?: number }[] = [
    { key: 'hr', label: '平均心率', unit: 'bpm', color: '#185fa5' },
    { key: 'hrv', label: '心率变异性', unit: '分', color: '#0f6e56', max: 100 },
    { key: 'stress', label: '精神压力', unit: '分', color: '#ba7517', max: 100 },
    { key: 'fatigue', label: '疲劳指数', unit: '分', color: '#993556', max: 100 }
  ];
  let cur = $derived(METRICS.find((m) => m.key === metric) ?? METRICS[0]);

  function valueOf(r: EcgAnalysisRecord): number | null {
    switch (metric) {
      case 'hr':
        return r.avg_hr;
      case 'hrv':
        return r.hrv;
      case 'stress':
        return r.stress;
      case 'fatigue':
        return r.fatigue;
    }
  }

  let el: HTMLDivElement | undefined = $state();
  let chart: echarts.ECharts | undefined;

  function buildOption(): echarts.EChartsOption {
    const p = palette();
    // 异常判定（房颤/测不准）用判定色标出，让趋势线上的「坏点」自己说话
    const data = records.map((r) => {
      const v = valueOf(r);
      if (v === null) return null;
      const isSel = r.id === selectedId;
      return {
        value: v,
        symbolSize: isSel ? 9 : 5,
        itemStyle: {
          color: r.code === 'sinus_rhythm' ? dataColor(cur.color) : codeColor(r.code),
          borderColor: isSel ? p.ink : 'transparent',
          borderWidth: isSel ? 2 : 0
        }
      };
    });
    return {
      animation: false,
      grid: { left: 44, right: 14, top: 10, bottom: 24 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: p.surface,
        borderColor: p.line,
        borderWidth: 1,
        textStyle: { color: p.ink, fontSize: 12 },
        formatter: (params) => {
          const arr = Array.isArray(params) ? params : [params];
          if (arr.length === 0) return '';
          const idx = (arr[0] as { dataIndex?: number }).dataIndex ?? 0;
          const r = records[idx];
          if (!r) return '';
          const v = valueOf(r);
          return `<div style="font-size:11px;color:${p.inkSoft};margin-bottom:2px">${r.date.slice(5)} ${r.clock} · ${r.label}</div>
            <div style="font-size:12px;color:${p.ink}">${cur.label} <b>${v === null ? '—' : v + ' ' + cur.unit}</b></div>`;
        }
      },
      xAxis: {
        type: 'category',
        data: records.map((r) => r.date.slice(5)),
        axisLine: { lineStyle: { color: p.line } },
        axisLabel: { color: p.inkSoft, fontSize: 11, hideOverlap: true },
        axisTick: { show: false }
      },
      yAxis: {
        type: 'value',
        min: cur.max === 100 ? 0 : undefined,
        max: cur.max,
        scale: cur.max !== 100,
        splitLine: { lineStyle: { color: p.split } },
        axisLabel: { color: p.inkSoft, fontSize: 11 }
      },
      series: [
        {
          type: 'line',
          data,
          showSymbol: true,
          symbol: 'circle',
          smooth: true,
          lineStyle: { width: 1.8, color: dataColor(cur.color) },
          areaStyle: { color: dataColor(cur.color), opacity: 0.07 },
          connectNulls: false
        }
      ]
    };
  }

  onMount(() => {
    if (!el) return;
    chart = echarts.init(el, undefined, { renderer: 'canvas' });
    const onClick = (params: { componentType?: string; dataIndex?: number }) => {
      if (params.componentType !== 'series') return;
      const r = records[params.dataIndex ?? -1];
      if (r) onselect(r.id);
    };
    chart.on('click', onClick);
    const onResize = () => chart?.resize();
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      chart?.off('click', onClick);
      chart?.dispose();
      chart = undefined;
    };
  });

  $effect(() => {
    if (!chart) return;
    const hasData = records.some((r) => valueOf(r) !== null);
    if (hasData) chart.setOption(buildOption(), { notMerge: true });
    else chart.clear();
  });
</script>

<div class="flex flex-wrap items-center justify-between gap-2">
  <div class="text-[13px] font-medium">趋势</div>
  <Segmented
    value={metric}
    options={METRICS.map((m) => ({ label: m.label, value: m.key }))}
    onchange={(v) => (metric = v)}
  />
</div>
{#if records.length === 0}
  <div class="py-10 text-center text-[13px] text-[var(--color-ink-500)]">
    所选时间段内没有测量记录
  </div>
{:else if !records.some((r) => valueOf(r) !== null)}
  <div class="py-10 text-center text-[13px] text-[var(--color-ink-500)]">
    这些记录算不出{cur.label}（没有可用波形，或信号质量不达标）
  </div>
{:else}
  <div bind:this={el} style="height:{height}px;width:100%;" class="mt-2"></div>
  <div class="mt-1 text-[11px] text-[var(--color-ink-500)]">
    点击圆点可查看该次测量的心电图波形；非绿色圆点表示判定异常
  </div>
{/if}
