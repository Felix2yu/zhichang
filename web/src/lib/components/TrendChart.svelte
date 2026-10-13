<script lang="ts">
	import { onMount } from 'svelte';
	import * as echarts from 'echarts';

	type Series = {
		name: string;
		points: { date: string; value: number }[];
		type?: 'line' | 'bar';
		color?: string;
		stack?: string;
	};

	type Props = {
		series: Series[];
		height?: number;
		unit?: string;
		digits?: number;
	};

	let { series, height = 280, unit = '', digits = 0 }: Props = $props();

	let el: HTMLDivElement | undefined = $state();
	let chart: echarts.ECharts | undefined;

	function buildOption(): echarts.EChartsOption {
		const palette = ['#0f6e56', '#185fa5', '#ba7517', '#993556', '#5f5e5a'];
		return {
			grid: { left: 44, right: 16, top: 28, bottom: 28 },
			tooltip: {
				trigger: 'axis',
				backgroundColor: '#fff',
				borderColor: '#e6e8ec',
				borderWidth: 1,
				textStyle: { color: '#16181d', fontSize: 12 },
				valueFormatter: (v) => `${Number(v).toFixed(digits)}${unit ? ' ' + unit : ''}`
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
