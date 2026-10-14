<script lang="ts">
	import { onMount } from 'svelte';
	import * as echarts from 'echarts';
	import type { WeekdayRhythm } from '#lib/sleep-rhythm';

	type Props = {
		byWeekday: WeekdayRhythm[];
		height?: number;
	};

	let { byWeekday, height = 260 }: Props = $props();

	let el: HTMLDivElement | undefined = $state();
	let chart: echarts.ECharts | undefined;
	let width = $state(0);

	/** 只有实际有样本的星期才画 */
	const lanes = $derived(byWeekday.filter((w) => w.nights > 0 && w.bedtimeMin !== null));

	/**
	 * 横轴：按实际数据自适应，但两端各留 1 小时余量。
	 * 固定 16:00→12:00 会在「早睡早起」或「晚睡晚起」的人身上留下大片空白。
	 */
	const axisRange = $derived.by(() => {
		const pts: number[] = [];
		for (const w of lanes) {
			pts.push(bedToAxis(w.bedtimeMin!));
			if (w.wakeMin !== null) pts.push(wakeToAxis(w.wakeMin));
		}
		if (pts.length === 0) return { min: 16 * 60, max: 36 * 60 };
		return {
			min: Math.floor((Math.min(...pts) - 60) / 60) * 60,
			max: Math.ceil((Math.max(...pts) + 60) / 60) * 60
		};
	});

	/** 本地当日分钟 → 轴位置：早晨的排到次日 */
	function minOfDayToAxis(mod: number): number {
		const m = ((Math.round(mod) % 1440) + 1440) % 1440;
		return m + (m < 12 * 60 ? 1440 : 0);
	}

	/** 相对 18:00 的就寝分钟 → 轴位置 */
	function bedToAxis(rel: number): number {
		return minOfDayToAxis(rel + 18 * 60);
	}

	/** 起床：已经是本地当日分钟 */
	function wakeToAxis(min: number): number {
		return minOfDayToAxis(min);
	}

	function axisClock(v: number): string {
		const m = Math.round(v) % 1440;
		return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
	}

	const BED_COLOR = '#3f7fc4';
	const WAKE_COLOR = '#ba7517';

	function buildOption(): echarts.EChartsOption {
		const narrow = width > 0 && width < 420;
		return {
			animation: false,
			grid: { left: narrow ? 42 : 56, right: 16, top: 26, bottom: 30 },
			tooltip: {
				trigger: 'item',
				backgroundColor: '#fff',
				borderColor: '#e6e8ec',
				borderWidth: 1,
				textStyle: { color: '#16181d', fontSize: 12 },
				formatter: (p) => {
					const pp = p as { dataIndex?: number };
					const w = lanes[pp.dataIndex ?? -1];
					if (!w) return '';
					const hrs = w.sleepHr === null ? '—' : `${w.sleepHr.toFixed(1)} h`;
					const spread =
						w.bedtimeSpreadMin === null ? '—' : `±${Math.round(w.bedtimeSpreadMin)} 分钟`;
					return `<div style="font-size:12px;color:#16181d;line-height:1.6">
						<b>${w.label}</b> · ${w.nights} 晚<br/>
						<span style="color:#6b7280">就寝</span> ${axisClock(bedToAxis(w.bedtimeMin!))}
						（离散 ${spread}）<br/>
						<span style="color:#6b7280">起床</span> ${
							w.wakeMin === null ? '—' : axisClock(wakeToAxis(w.wakeMin))
						}　<span style="color:#6b7280">时长</span> ${hrs}
					</div>`;
				}
			},
			legend: {
				top: 0,
				right: 0,
				itemWidth: 10,
				itemHeight: 8,
				textStyle: { fontSize: 11, color: '#6b7280' },
				data: ['就寝时刻', '起床时刻']
			},
			xAxis: {
				type: 'value',
				min: axisRange.min,
				max: axisRange.max,
				interval: (axisRange.max - axisRange.min) > 720 ? 120 : 60,
				axisLine: { lineStyle: { color: '#e6e8ec' } },
				axisTick: { show: false },
				axisLabel: {
					color: '#9aa0a6',
					fontSize: 10,
					formatter: (v: number) => axisClock(v)
				},
				splitLine: { lineStyle: { color: '#f4f5f7' } }
			},
			yAxis: {
				type: 'category',
				data: lanes.map((w) => w.label),
				inverse: true,
				axisLine: { show: false },
				axisTick: { show: false },
				axisLabel: { color: '#6b7280', fontSize: 11 }
			},
			series: [
				{
					// 「睡眠区间」必须先画：ECharts 后面的序列盖在上面，
					// 放在就寝/起床之后会把两个圆点埋掉。
					name: '睡眠区间',
					type: 'custom',
					silent: true,
					z: 0,
					data: lanes.map(() => 0),
					renderItem: (params, api) => {
						const w = lanes[params.dataIndex];
						if (!w || w.wakeMin === null) return null;
						const laneH = api.size?.([0, 1]);
						const h = Array.isArray(laneH) ? (laneH[1] as number) : (laneH as number);
						if (!h || !Number.isFinite(h)) return null;
						const lane = params.dataIndex;
						const x0 = api.coord([bedToAxis(w.bedtimeMin!), lane])[0] as number;
						const x1 = api.coord([wakeToAxis(w.wakeMin), lane])[0] as number;
						const y = api.coord([0, lane])[1] as number;
						const barH = Math.max(4, Math.min(h * 0.22, 10));
						return {
							type: 'rect',
							shape: {
								x: x0,
								y: y - barH / 2,
								width: Math.max(1, x1 - x0),
								height: barH,
								r: barH / 2
							},
							style: { fill: '#e8edf5' }
						};
					}
				},
				{
					name: '就寝时刻',
					type: 'scatter',
					z: 2,
					symbolSize: 11,
					itemStyle: { color: BED_COLOR },
					data: lanes.map((w) => bedToAxis(w.bedtimeMin!))
				},
				{
					name: '起床时刻',
					type: 'scatter',
					z: 2,
					symbolSize: 11,
					itemStyle: { color: WAKE_COLOR },
					data: lanes.map((w) => (w.wakeMin === null ? null : wakeToAxis(w.wakeMin)))
				}
			]
		};
	}

	onMount(() => {
		if (!el) return;
		chart = echarts.init(el, undefined, { renderer: 'canvas' });
		chart.setOption(buildOption());
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
		void lanes;
		void width;
		void axisRange;
		if (chart) chart.setOption(buildOption(), { notMerge: true });
	});
</script>

<div bind:this={el} style="height:{height}px;width:100%;"></div>