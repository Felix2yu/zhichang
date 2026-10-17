<script lang="ts">
  import { onMount } from 'svelte';
  import * as echarts from 'echarts';
  import { dataColor, palette } from '#lib/theme.svelte';

  export type TrendSeries = {
    name: string;
    points: { date: string; value: number }[];
    type?: 'line' | 'bar';
    color?: string;
    stack?: string;
    /** 该序列专属单位；缺省回退到 chart 级 unit */
    unit?: string;
    /** 该序列专属小数位；缺省回退到 chart 级 digits */
    digits?: number;
    /** 关联的指标 id，便于页面回查口径（可选） */
    metric?: string;
  };

  type Props = {
    series: TrendSeries[];
    height?: number;
    /** chart 级兜底单位（旧调用方式，如睡眠页传 "h"） */
    unit?: string;
    /** chart 级兜底小数位 */
    digits?: number;
    /** Y 轴单位标注；同组单位不一致时留空，改由 tooltip / 图例按序列显示 */
    yUnit?: string;
    /** 目标线：画在第一条序列上，用于「离目标还差多少」 */
    targetLine?: { value: number; label: string } | null;
  };

  let {
    series,
    height = 280,
    unit = '',
    digits = 0,
    yUnit = '',
    targetLine = null
  }: Props = $props();

  let el: HTMLDivElement | undefined = $state();
  let chart: echarts.ECharts | undefined;

  // 序列身份色：同一个指标在任何图上都是同一个颜色。
  // 暗底下由 dataColor() 统一提亮度，色相与相互次序都不动。
  const SERIES_PALETTE = ['#0f6e56', '#185fa5', '#ba7517', '#993556', '#5f5e5a', '#0c447c'];

  function unitOf(name: string): string {
    return series.find((s) => s.name === name)?.unit ?? unit;
  }
  function digitsOf(name: string): number {
    return series.find((s) => s.name === name)?.digits ?? digits;
  }
  function fmt(name: string, v: number): string {
    const d = digitsOf(name);
    const u = unitOf(name);
    const n = Math.abs(v) >= 1000 && d === 0 ? Math.round(v).toLocaleString('zh-CN') : v.toFixed(d);
    return u ? `${n} ${u}` : String(n);
  }

  function buildOption(): echarts.EChartsOption {
    const p = palette();
    return {
      grid: { left: yUnit ? 52 : 44, right: 16, top: 28, bottom: 28 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: p.surface,
        borderColor: p.line,
        borderWidth: 1,
        textStyle: { color: p.ink, fontSize: 12 },
        // 按序列显示各自的单位与小数位
        formatter: (params) => {
          const arr = Array.isArray(params) ? params : [params];
          if (arr.length === 0) return '';
          const date = (arr[0] as { axisValue?: string }).axisValue ?? '';
          let html = `<div style="font-size:12px;color:${p.ink};margin-bottom:2px">${date}</div>`;
          for (const pp0 of arr) {
            const pp = pp0 as { marker?: string; seriesName?: string; value?: number };
            const val = Number(pp.value ?? 0);
            html += `<div style="font-size:12px;color:${p.ink};line-height:1.5">${pp.marker ?? ''}${pp.seriesName} <b>${fmt(pp.seriesName ?? '', val)}</b></div>`;
          }
          return html;
        }
      },
      legend: {
        top: 0,
        itemHeight: 8,
        itemWidth: 12,
        textStyle: { fontSize: 11, color: p.inkSoft }
      },
      xAxis: {
        type: 'category',
        data: series[0]?.points.map((pt) => pt.date.slice(5)) ?? [],
        axisLine: { lineStyle: { color: p.line } },
        axisLabel: { color: p.inkSoft, fontSize: 11 },
        axisTick: { show: false }
      },
      yAxis: {
        type: 'value',
        name: yUnit,
        nameTextStyle: { fontSize: 11, color: p.inkSoft, align: 'left' as const },
        nameGap: 8,
        // 量级较小的序列也能看清：开启缩放，但不强制从 0 起（避免折线被压平）
        scale: series.some((s) => s.type === 'line'),
        splitLine: { lineStyle: { color: p.split } },
        axisLabel: { color: p.inkSoft, fontSize: 11 }
      },
      series: series.map((s, i) => ({
        name: s.name,
        type: s.type ?? 'line',
        stack: s.stack,
        data: s.points.map((pt) => pt.value),
        smooth: (s.type ?? 'line') === 'line',
        symbol: 'circle',
        symbolSize: 4,
        lineStyle: { width: 1.8 },
        itemStyle: { color: dataColor(s.color ?? SERIES_PALETTE[i % SERIES_PALETTE.length]) },
        barMaxWidth: 18,
        // 目标线只画一条：挂在首序列上，避免多序列重复
        ...(i === 0 && targetLine
          ? {
              markLine: {
                silent: true,
                symbol: 'none',
                lineStyle: { type: 'dashed', color: p.warn, width: 1.2 },
                label: {
                  formatter: targetLine.label,
                  fontSize: 10,
                  color: p.warn,
                  position: 'insideEndTop'
                },
                data: [{ yAxis: targetLine.value }]
              }
            }
          : {})
      }))
    };
  }

  onMount(() => {
    if (!el) return;
    chart = echarts.init(el, undefined, { renderer: 'canvas' });
    chart.setOption(buildOption());
    const onResize = () => chart?.resize();
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      chart?.dispose();
      chart = undefined;
    };
  });

  $effect(() => {
    if (chart && series.length >= 0) {
      chart.setOption(buildOption(), { notMerge: true });
    }
  });
</script>

<div bind:this={el} style="height:{height}px;width:100%;"></div>
