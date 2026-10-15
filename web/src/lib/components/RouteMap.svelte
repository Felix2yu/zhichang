<script lang="ts">
	import { onMount } from 'svelte';
	import * as echarts from 'echarts';
	import { dataColor, palette } from '#lib/theme.svelte';
	import type { WorkoutRoute } from '#lib/api';

	/**
	 * 轨迹查看器：把一次运动的折线画出来。
	 *
	 * **刻意不接任何地图瓦片**。瓦片要从第三方服务器按经纬度拉取，
	 * 那等于把「我在哪里走过」逐点发给别人——这份数据的应用定位是值不出本机。
	 * 所以这里画的是等比例的裸折线：形状、方向、疏密、速度配色都在，
	 * 只是没有街道底图。真要底图，得是用户自己部署的瓦片源（另说）。
	 */
	type Props = {
		route: WorkoutRoute;
		height?: number;
	};

	let { route, height = 340 }: Props = $props();

	let el: HTMLDivElement | undefined = $state();
	let chart: echarts.ECharts | undefined;

	const R = 6371000; // 地球半径（米）

	function haversine(a: [number, number], b: [number, number]): number {
		const dLat = ((b[0] - a[0]) * Math.PI) / 180;
		const dLon = ((b[1] - a[1]) * Math.PI) / 180;
		const la1 = (a[0] * Math.PI) / 180;
		const la2 = (b[0] * Math.PI) / 180;
		const h = Math.sin(dLat / 2) ** 2 + Math.cos(la1) * Math.cos(la2) * Math.sin(dLon / 2) ** 2;
		return 2 * R * Math.asin(Math.min(1, Math.sqrt(h)));
	}

	/** 逐点速度（m/s）：由相邻两点距离除以时间差推出，上游没给速度字段也能算。 */
	function speeds(): number[] {
		const n = route.lats.length;
		const out = new Array<number>(n).fill(0);
		for (let i = 1; i < n; i++) {
			const dt = (route.at_secs[i] ?? 0) - (route.at_secs[i - 1] ?? 0);
			if (dt <= 0) {
				out[i] = out[i - 1];
				continue;
			}
			const d = haversine([route.lats[i - 1], route.lons[i - 1]], [route.lats[i], route.lons[i]]);
			out[i] = d / dt;
		}
		return out;
	}

	/** 折线总长（米），用于标出这张图实际画了多远。 */
	function lengthMeters(): number {
		let sum = 0;
		for (let i = 1; i < route.lats.length; i++) {
			sum += haversine([route.lats[i - 1], route.lons[i - 1]], [route.lats[i], route.lons[i]]);
		}
		return sum;
	}

	let drawing = $derived.by(() => {
		const sp = speeds();
		const data: [number, number, number][] = [];
		for (let i = 0; i < route.lats.length; i++) {
			data.push([route.lons[i], route.lats[i], sp[i]]);
		}
		let latMin = route.lat_min;
		let latMax = route.lat_max;
		let lonMin = route.lon_min;
		let lonMax = route.lon_max;
		// 单点或退化 bbox：给一个最小跨度，否则轴范围为零、点画到屏幕外
		if (latMax - latMin < 1e-5) {
			const c = (latMax + latMin) / 2;
			latMin = c - 5e-5;
			latMax = c + 5e-5;
		}
		if (lonMax - lonMin < 1e-5) {
			const c = (lonMax + lonMin) / 2;
			lonMin = c - 5e-5;
			lonMax = c + 5e-5;
		}
		const midLat = (latMin + latMax) / 2;
		// 1° 经度的实际距离随纬度收缩（cos φ），不修正的话高纬度会把轨迹横向拉扁
		const k = Math.max(0.05, Math.cos((midLat * Math.PI) / 180));
		return { data, sp, latMin, latMax, lonMin, lonMax, k };
	});

	/**
	 * 把轴范围撑到与绘图区宽高比一致，形状才不变形。
	 *
	 * ECharts 会各自独立地铺满 x/y 轴，所以「等比例」不能靠它，
	 * 只能把短的那一边的范围放大（并留 6% 边距），让两轴跨度之比
	 * 等于像素之比。
	 */
	function fit(w: number, h: number) {
		const { latMin, latMax, lonMin, lonMax, k } = drawing;
		const pad = 1.06;
		let ySpan = (latMax - latMin) * pad;
		let xSpan = (lonMax - lonMin) * k * pad;
		const want = w > 0 && h > 0 ? w / h : 1.6;
		if (xSpan / ySpan < want) xSpan = ySpan * want;
		else ySpan = xSpan / want;
		const latC = (latMin + latMax) / 2;
		const lonC = (lonMin + lonMax) / 2;
		return {
			yMin: latC - ySpan / 2,
			yMax: latC + ySpan / 2,
			xMin: lonC - xSpan / (2 * k),
			xMax: lonC + xSpan / (2 * k)
		};
	}

	function fmtClock(i: number): string {
		const s = route.at_secs[i] ?? 0;
		const neg = s < 0;
		const a = Math.abs(s);
		const hh = Math.floor(a / 3600);
		const mm = Math.floor((a % 3600) / 60);
		const sign = neg ? '-' : '+';
		return hh > 0 ? `${sign}${hh}:${String(mm).padStart(2, '0')}` : `${sign}${mm} 分`;
	}

	function buildOption(): echarts.EChartsOption {
		const p = palette();
		const { data, sp } = drawing;
		const box = fit(el?.clientWidth ?? 640, height);
		const smax = Math.max(0.1, ...sp);
		const hasSpeed = smax > 0.15;
		const start = data[0];
		const end = data[data.length - 1];
		return {
			animation: false,
			backgroundColor: 'transparent',
			grid: { left: 8, right: 8, top: 8, bottom: 8, containLabel: false },
			tooltip: {
				trigger: 'item',
				backgroundColor: p.surface,
				borderColor: p.line,
				textStyle: { color: p.ink, fontSize: 12 },
				formatter: (raw: unknown) => {
					const v = (raw as { data?: [number, number, number]; dataIndex?: number }).data;
					if (!v) return '';
					const i = (raw as { dataIndex: number }).dataIndex;
					const ele = route.eles[i] ?? 0;
					return [
						`第 ${i + 1} / ${data.length} 点 · ${fmtClock(i)}`,
						`速度 ${(v[2] * 3.6).toFixed(1)} km/h`,
						ele ? `高程 ${ele.toFixed(1)} m` : '',
						`${v[1].toFixed(5)}, ${v[0].toFixed(5)}`
					]
						.filter(Boolean)
						.join('<br/>');
				}
			},
			xAxis: { type: 'value', min: box.xMin, max: box.xMax, show: false },
			yAxis: { type: 'value', min: box.yMin, max: box.yMax, show: false },
			// 速度着色：走得多快一眼可见，也顺带暴露「一段一段停着」的采样特征。
			// 全程静止（点数太少或时间缺失）时不画色条，否则图例在撒谎。
			visualMap: hasSpeed
				? {
						show: true,
						dimension: 2,
						min: 0,
						max: Number(smax.toFixed(2)),
						right: 10,
						bottom: 12,
						itemWidth: 10,
						itemHeight: 90,
						text: ['快', '慢'],
						textStyle: { color: p.inkSoft, fontSize: 11 },
						inRange: { color: [dataColor('#9bb7d4'), dataColor('#378add'), dataColor('#0c447c')] }
					}
				: undefined,
			series: [
				{
					type: 'line',
					data,
					showSymbol: false,
					sampling: 'lttb',
					lineStyle: { width: 2.6, color: hasSpeed ? undefined : dataColor('#378add') },
					emphasis: { disabled: true },
					z: 2
				},
				{
					type: 'scatter',
					data: [start, end],
					symbolSize: 9,
					z: 5,
					label: {
						show: true,
						position: 'right',
						fontSize: 11,
						color: p.ink,
						formatter: (raw: unknown) => ((raw as { dataIndex: number }).dataIndex === 0 ? '起' : '终')
					},
					itemStyle: { color: [dataColor('#2f8f5b'), dataColor('#c0453f')] }
				}
				// 折线与起终点散点是两种 series，ECharts 的联合类型推不出来，显式交给它
			] as echarts.EChartsOption['series']
		};
	}

	onMount(() => {
		if (!el) return;
		chart = echarts.init(el, undefined, { renderer: 'canvas' });
		chart.setOption(buildOption());
		const onResize = () => chart?.setOption(buildOption(), { notMerge: true });
		window.addEventListener('resize', onResize);
		return () => {
			window.removeEventListener('resize', onResize);
			chart?.dispose();
			chart = undefined;
		};
	});

	// 换一条轨迹（切换运动）时重建，而不是只 setOption 数据：
	// 轴范围与比例修正都依赖新的 bbox，留着旧范围会把新轨迹画到画布外。
	$effect(() => {
		void route;
		if (chart) chart.setOption(buildOption(), { notMerge: true });
	});

	let km = $derived(lengthMeters() / 1000);
	let minutes = $derived(Math.abs((route.at_secs.at(-1) ?? 0) - (route.at_secs[0] ?? 0)) / 60);
</script>

<div class="card overflow-hidden">
	<div class="flex flex-wrap items-baseline justify-between gap-2 border-b border-[var(--color-line)] px-4 py-2.5">
		<div class="text-[13px] font-medium">轨迹</div>
		<div class="num text-[12px] text-[var(--color-ink-500)]">
			{route.points} 点 · 折线 {km.toFixed(2)} km · {minutes.toFixed(0)} 分钟
		</div>
	</div>
	<div bind:this={el} style="height:{height}px"></div>
	<div class="flex flex-wrap items-center justify-between gap-2 border-t border-[var(--color-line)] px-4 py-2 text-[11px] text-[var(--color-ink-500)]">
		<span>
			不接地图瓦片：瓦片要按经纬度向第三方拉取，等于把行踪逐点发出去。这里只画等比例折线。
		</span>
		<span class="num">
			{route.lat_min.toFixed(4)}~{route.lat_max.toFixed(4)}, {route.lon_min.toFixed(4)}~{route.lon_max.toFixed(4)}
			{#if route.elev_max > route.elev_min}· 高程 {route.elev_min.toFixed(0)}~{route.elev_max.toFixed(0)} m{/if}
			· {route.file_name}
		</span>
	</div>
</div>
