<script lang="ts">
	import { onMount } from 'svelte';
	import * as echarts from 'echarts';
	import { STAGE_META, stageLabel, stageMeta } from '#lib/sleep-stages';
	import type { NightSeries, SleepStageSeg } from '#lib/api';

	type Props = {
		segments: SleepStageSeg[];
		/** 夜间窗口（分钟，起点为睡眠日本地 00:00），用于定横轴范围 */
		winStart: number;
		winEnd: number;
		height?: number;
		/** 叠加在时间线下方的生理指标曲线（夜间心率等），与分段共用同一条本夜时间轴 */
		overlay?: NightSeries | null;
		/** 叠加曲线的标注（如「夜间心率 bpm」） */
		overlayLabel?: string;
	};

	let { segments, winStart, winEnd, height = 240, overlay = null, overlayLabel = '' }: Props = $props();

	let el: HTMLDivElement | undefined = $state();
	let chart: echarts.ECharts | undefined;
	/** 容器宽度（CSS px），用于按可用宽度决定刻度疏密 */
	let width = $state(0);

	/** 只渲染真正的睡眠阶段；在床（inbed）不是阶段，Apple 的时间线也不画它 */
	const drawn = $derived(segments.filter((s) => s.stage !== 'inbed' && s.stage !== 'other'));

	/** 数据本身的起止（分钟） */
	const rawSpan = $derived.by(() => {
		const lo = Math.min(winStart, ...drawn.map((s) => s.local_min));
		const hi = Math.max(winEnd, ...drawn.map((s) => s.local_min + s.duration_min));
		return { lo, hi };
	});

	/**
	 * 刻度间隔：按可用宽度估算，避免标签互相压叠。
	 * 移动端绘图区只有两百来像素，每小时一格必然重叠（00:00 与 01:00 粘成一片）。
	 */
	const tickInterval = $derived.by(() => {
		const span = Math.max(60, rawSpan.hi - rawSpan.lo);
		const perHour = Math.max(24, width / (span / 60));
		const need = Math.ceil(52 / perHour) * 60; // 每个标签至少留 52px
		for (const step of [60, 120, 180, 240, 360]) {
			if (step >= need) return step;
		}
		return 360;
	});

	/**
	 * 横轴范围：对齐到刻度间隔的整数倍。
	 * 若只对齐到整点，末尾会多出一个与刻度不同步的边界标签（如 07:00 后面紧跟 08:00），
	 * 窄屏下这两个标签就粘在一起了。
	 */
	const axis = $derived.by(() => {
		const step = tickInterval;
		return {
			lo: Math.floor(rawSpan.lo / step) * step,
			hi: Math.ceil(rawSpan.hi / step) * step
		};
	});

	/** 分钟 → 本地墙钟 HH:MM（跨零点后自然显示为次日时刻） */
	function clock(min: number): string {
		const m = ((Math.round(min) % 1440) + 1440) % 1440;
		return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
	}

	function fmtDur(min: number): string {
		const m = Math.round(min);
		if (m < 60) return `${m} 分钟`;
		const h = Math.floor(m / 60);
		const r = m % 60;
		return r === 0 ? `${h} 小时` : `${h} 小时 ${r} 分`;
	}

	function buildOption(): echarts.EChartsOption {
		// Y 轴只列出这一晚真实出现的阶段（与 Apple 一致：没有深睡就不占那一行）
		const laneKeys: string[] = STAGE_META.filter(
			(s) => s.drawn && drawn.some((d) => d.stage === s.key)
		).map((s) => s.key);
		// 窄屏用短标签 + 更小的左边距，把宽度让给绘图区
		const narrow = width > 0 && width < 420;
		// 叠加曲线：与分期泳道共用同一条本夜时间轴（两个 xAxis 用同一组 min/max/interval）
		const ov = overlay && overlay.points.length > 0 ? overlay : null;
		const ovColor = '#993556';
		const axisBase = {
			type: 'value' as const,
			min: axis.lo,
			max: axis.hi,
			interval: tickInterval,
			splitLine: { show: false },
			axisLine: { show: false },
			axisTick: { show: false }
		};
		return {
			animation: false,
			grid: ov
				? [
						{ left: narrow ? 52 : 76, right: narrow ? 30 : 18, top: 10, bottom: 104 },
						{ left: narrow ? 52 : 76, right: narrow ? 30 : 18, height: 56, bottom: 34 }
					]
				: { left: narrow ? 52 : 76, right: narrow ? 30 : 18, top: 10, bottom: 30 },
			tooltip: {
				trigger: 'item',
				backgroundColor: '#fff',
				borderColor: '#e6e8ec',
				borderWidth: 1,
				textStyle: { color: '#16181d', fontSize: 12 },
				formatter: (p) => {
					const pp = p as { dataIndex?: number; seriesType?: string; value?: number[] };
					if (ov && pp.seriesType === 'line' && Array.isArray(pp.value)) {
						return `<div style="font-size:12px;color:#16181d;line-height:1.6">
							<b>${overlayLabel || ov.metric}</b><br/>
							<span style="color:#6b7280">${clock(pp.value[0])}</span><br/>
							${pp.value[1].toFixed(ov.metric === 'heart_rate' ? 0 : 1)} ${ov.unit}
						</div>`;
					}
					const seg = drawn[pp.dataIndex ?? -1];
					if (!seg) return '';
					const meta = STAGE_META.find((m) => m.key === seg.stage);
					return `<div style="font-size:12px;color:#16181d;line-height:1.6">
						<b>${meta?.label ?? seg.stage}</b><br/>
						<span style="color:#6b7280">${clock(seg.local_min)} – ${clock(
							seg.local_min + seg.duration_min
						)}</span><br/>
						${fmtDur(seg.duration_min)}
					</div>`;
				}
			},
			xAxis: ov
				? [
						{ ...axisBase, axisLabel: { show: false } },
						{
							...axisBase,
							axisLabel: {
								color: '#9aa0a6',
								fontSize: 10,
								formatter: (v: number) => clock(v)
							}
						}
					]
				: { ...axisBase, axisLabel: { color: '#9aa0a6', fontSize: 10, formatter: (v: number) => clock(v) } },
			yAxis: [
				{
					type: 'category',
					// 从上到下：清醒 → REM → 核心 → 深睡（与 Apple 一致）
					data: laneKeys,
					inverse: true,
					axisLine: { show: false },
					axisTick: { show: false },
					axisLabel: {
						color: '#6b7280',
						fontSize: narrow ? 10 : 11,
						formatter: (v: string) => (narrow ? stageMeta(v).short : stageLabel(v))
					},
					splitLine: {
						show: true,
						lineStyle: { color: '#f0f1f4' }
					}
				},
				{
					// 叠加曲线：心率/HRV/血氧量级差得远，各自用自己的刻度，不画网格线以免和泳道打架
					type: 'value',
					gridIndex: ov ? 1 : 0,
					scale: true,
					show: !!ov,
					name: ov ? overlayLabel || `${ov.metric} ${ov.unit}` : '',
					nameLocation: 'end' as const,
					nameTextStyle: { fontSize: 10, color: '#6b7280', align: 'left' as const },
					nameGap: 6,
					axisLine: { show: false },
					axisTick: { show: false },
					splitNumber: 2,
					splitLine: { lineStyle: { color: '#f0f1f4' } },
					axisLabel: { color: '#9aa0a6', fontSize: 10 }
				}
			],
			series: [
				{
					type: 'custom',
					xAxisIndex: 0,
					yAxisIndex: 0,
					// data 带上该段所属泳道序号，renderItem 直接取用
					data: drawn.map((s) => laneKeys.indexOf(s.stage)),
					renderItem: (params, api) => {
						const seg = drawn[params.dataIndex];
						if (!seg) return null;
						const laneIdx = Number(api.value(0));
						if (!Number.isFinite(laneIdx) || laneIdx < 0) return null;
						// api.size 可能返回标量或数组，统一取第 1 个分量的像素高
						const sz = api.size?.([0, 1]);
						const laneH = Array.isArray(sz) ? (sz[1] as number) : (sz as number);
						if (!laneH || !Number.isFinite(laneH)) return null;
						const x0 = api.coord([seg.local_min, laneIdx])[0] as number;
						const x1 = api.coord([seg.local_min + seg.duration_min, laneIdx])[0] as number;
						const yCenter = api.coord([seg.local_min, laneIdx])[1] as number;
						// 柱子不占满泳道高度：留出泳道间距，视觉上分层才清楚
						const h = Math.max(5, Math.min(laneH * 0.58, 22));
						const y = yCenter - h / 2;
						const meta = STAGE_META.find((m) => m.key === seg.stage);
						// 很短的段（如 1 分钟清醒）也要看得见，给一个最小宽度
						const w = Math.max(2.5, x1 - x0 - 1.5);
						return {
							type: 'rect',
							shape: { x: x0, y, width: w, height: h, r: Math.min(h / 2, 5) },
							style: { fill: meta?.color ?? '#9aa0a6' }
						};
					}
				},
				{
					type: 'line',
					xAxisIndex: ov ? 1 : 0,
					yAxisIndex: 1,
					silent: !ov,
					showSymbol: false,
					sampling: 'lttb',
					smooth: 0.15,
					data: ov ? ov.points.map((p) => [p.local_min, p.value]) : [],
					lineStyle: { width: 1.4, color: ovColor },
					itemStyle: { color: ovColor },
					// 最低点标出来：Apple 的夜间心率主打的就是这个值
					markPoint: ov
						? {
								symbol: 'circle',
								symbolSize: 6,
								silent: true,
								label: { show: false },
								data: [{ name: '最低', coord: [ov.min_local_min, ov.min] }],
								itemStyle: { color: ovColor }
							}
						: undefined
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
		const onResize = () => chart?.resize();
		window.addEventListener('resize', onResize);
		return () => {
			ro.disconnect();
			window.removeEventListener('resize', onResize);
			chart?.dispose();
			chart = undefined;
		};
	});

	$effect(() => {
		// 依赖 drawn / axis / width / tickInterval / overlay 的重算结果，数据或尺寸变化时重绘
		void drawn;
		void axis;
		void width;
		void tickInterval;
		void overlay;
		if (chart) chart.setOption(buildOption(), { notMerge: true });
	});
</script>

<div bind:this={el} style="height:{height}px;width:100%;"></div>
