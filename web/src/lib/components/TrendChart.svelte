<script lang="ts">
	import { onMount } from 'svelte';
	import * as echarts from 'echarts';

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
	};

	let { series, height = 280, unit = '', digits = 0, yUnit = '' }: Props = $props();

	let el: HTMLDivElement | undefined = $state();
	let chart: echarts.ECharts | undefined;

	const palette = ['#0f6e56', '#185fa5', '#ba7517', '#993556', '#5f5e5a', '#0c447c'];

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
		return {
			grid: { left: yUnit ? 52 : 44, right: 16, top: 28, bottom: 28 },
			tooltip: {
				trigger: 'axis',
				backgroundColor: '#fff',
				borderColor: '#e6e8ec',
				borderWidth: 1,
				textStyle: { color: '#16181d', fontSize: 12 },
				// 按序列显示各自的单位与小数位
				formatter: (params) => {
					const arr = Array.isArray(params) ? params : [params];
					if (arr.length === 0) return '';
					const date = (arr[0] as { axisValue?: string }).axisValue ?? '';
					let html = `<div style="font-size:12px;color:#16181d;margin-bottom:2px">${date}</div>`;
					for (const p of arr) {
						const pp = p as { marker?: string; seriesName?: string; value?: number };
						const val = Number(pp.value ?? 0);
						html += `<div style="font-size:12px;color:#16181d;line-height:1.5">${pp.marker ?? ''}${pp.seriesName} <b>${fmt(pp.seriesName ?? '', val)}</b></div>`;
					}
					return html;
				}
			},
			legend: {
				top: 0,
				itemHeight: 8,
				itemWidth: 12,
				textStyle: { fontSize: 11, color: '#6b7280' }
			},
			xAxis: {
				type: 'category',
				data: series[0]?.points.map((p) => p.date.slice(5)) ?? [],
				axisLine: { lineStyle: { color: '#e6e8ec' } },
				axisLabel: { color: '#6b7280', fontSize: 11 },
				axisTick: { show: false }
			},
			yAxis: {
				type: 'value',
				name: yUnit,
				nameTextStyle: { fontSize: 11, color: '#6b7280', align: 'left' as const },
				nameGap: 8,
				// 量级较小的序列也能看清：开启缩放，但不强制从 0 起（避免折线被压平）
				scale: series.some((s) => s.type === 'line'),
				splitLine: { lineStyle: { color: '#f0f1f4' } },
				axisLabel: { color: '#6b7280', fontSize: 11 }
			},
			series: series.map((s, i) => ({
				name: s.name,
				type: s.type ?? 'line',
				stack: s.stack,
				data: s.points.map((p) => p.value),
				smooth: (s.type ?? 'line') === 'line',
				symbol: 'circle',
				symbolSize: 4,
				lineStyle: { width: 1.8 },
				itemStyle: { color: s.color ?? palette[i % palette.length] },
				barMaxWidth: 18
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
