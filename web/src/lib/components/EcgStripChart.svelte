<script lang="ts">
	import { onMount } from 'svelte';
	import * as echarts from 'echarts';

	type Props = {
		/** 按时间升序的电压值 */
		voltages: number[];
		/** 上游单位，原样标注（HAE 实测给 mcV） */
		unit?: string;
		/** 采样率（Hz）；缺省时横轴退化成「第 n 个采样点」 */
		samplingFrequency?: number | null;
		height?: number;
	};

	let { voltages, unit = '', samplingFrequency = null, height = 220 }: Props = $props();

	let el: HTMLDivElement | undefined = $state();
	let chart: echarts.ECharts | undefined;

	// 一次测量 15360 个点。不用 category 轴：那会让 ECharts 为每个点建一份刻度模型，
	// 真正省事的是 value 轴 + 采样降采样（sampling: 'lttb'），
	// 屏幕一个像素宽本来也放不下几千条竖线。
	function points(): [number, number][] {
		const fs = samplingFrequency && samplingFrequency > 0 ? samplingFrequency : null;
		const out: [number, number][] = new Array(voltages.length);
		for (let i = 0; i < voltages.length; i++) {
			out[i] = [fs ? i / fs : i, voltages[i]];
		}
		return out;
	}

	// 把量程边界抬到「整齐」的数。直接把 peak*1.08 当 min/max 交给 ECharts，
	// 它会把自定义边界的原值当刻度标签原样打出来（实测出现 728.7149047851563）。
	function niceBound(v: number): number {
		if (!(v > 0)) return 1;
		const mag = 10 ** Math.floor(Math.log10(v));
		for (const m of [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) {
			if (m * mag >= v - mag * 1e-6) return m * mag;
		}
		return 10 * mag;
	}

	function buildOption(): echarts.EChartsOption {
		const data = points();
		// 纵轴对称：心电以基线为中心上下摆动，自动量程会让一条平直的漂移段
		// 被拉满整个图高，看起来像剧烈波动。
		let peak = 0;
		for (const v of voltages) peak = Math.max(peak, Math.abs(v));
		// 8% 留白后抬到整齐数；interval 取一半，保证 0 正好落在刻度上。
		const bound = niceBound(peak > 0 ? peak * 1.08 : 1);
		const fs = samplingFrequency && samplingFrequency > 0 ? samplingFrequency : null;
		return {
			animation: false,
			grid: { left: 58, right: 14, top: 16, bottom: 26 },
			tooltip: {
				trigger: 'axis',
				backgroundColor: '#fff',
				borderColor: '#e6e8ec',
				borderWidth: 1,
				textStyle: { color: '#16181d', fontSize: 12 },
				formatter: (params) => {
					const arr = Array.isArray(params) ? params : [params];
					const p = arr[0] as { value?: [number, number] } | undefined;
					const v = p?.value;
					if (!v) return '';
					const t = fs ? `${v[0].toFixed(3)} s` : `第 ${v[0]} 点`;
					return `<div style="font-size:12px;color:#16181d">${t} · <b>${v[1].toFixed(1)}${unit ? ` ${unit}` : ''}</b></div>`;
				}
			},
			xAxis: {
				type: 'value',
				name: fs ? '秒' : '采样点',
				nameTextStyle: { fontSize: 11, color: '#6b7280' },
				nameGap: 6,
				min: 0,
				splitLine: { show: false },
				axisLine: { lineStyle: { color: '#e6e8ec' } },
				axisLabel: { color: '#6b7280', fontSize: 11, formatter: (v: number) => (fs ? v.toFixed(0) : String(v)) },
				axisTick: { show: false }
			},
			yAxis: {
				type: 'value',
				name: unit,
				nameTextStyle: { fontSize: 11, color: '#6b7280', align: 'right' as const },
				nameGap: 8,
				min: -bound,
				max: bound,
				interval: bound / 2,
				splitLine: { lineStyle: { color: '#f0f1f4' } },
				axisLabel: { color: '#6b7280', fontSize: 11 }
			},
			series: [
				{
					type: 'line',
					data,
					showSymbol: false,
					symbol: 'none',
					// 降采样只在数据点远多于像素时才动手，屏幕上看不出差别，
					// 但一次绘制从 15360 个点降到几百个。
					sampling: 'lttb',
					lineStyle: { width: 1, color: '#0f6e56' },
					// 基线：心电图判读靠它看抬高/压低
					markLine: {
						silent: true,
						symbol: 'none',
						lineStyle: { type: 'dashed', color: '#c8ccd4', width: 1 },
						label: { show: false },
						data: [{ yAxis: 0 }]
					}
				}
			]
		};
	}

	onMount(() => {
		if (!el) return;
		chart = echarts.init(el, undefined, { renderer: 'canvas' });
		const onResize = () => chart?.resize();
		window.addEventListener('resize', onResize);
		return () => {
			window.removeEventListener('resize', onResize);
			chart?.dispose();
			chart = undefined;
		};
	});

	$effect(() => {
		if (chart && voltages.length >= 0) {
			chart.setOption(buildOption(), { notMerge: true });
		}
	});
</script>

<div bind:this={el} style="height:{height}px;width:100%;"></div>
