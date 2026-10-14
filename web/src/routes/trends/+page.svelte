<script lang="ts">
	import { onMount } from 'svelte';
	import { api, shiftDays, todayStr, type DailyPoint, type MetricDef } from '#lib/api';
	import { METRIC_GROUPS, unitDigits, type MetricGroup } from '#lib/metric-groups';
	import RangePicker from '#lib/components/RangePicker.svelte';
	import TrendChart, { type TrendSeries } from '#lib/components/TrendChart.svelte';

	let defs = $state<MetricDef[]>([]);
	let selected = $state<string[]>(['step_count', 'heart_rate']);
	let days = $state(30);
	let series = $state<Record<string, DailyPoint[]>>({});
	let loading = $state(true);
	let error = $state('');

	let palette = ['#0f6e56', '#185fa5', '#ba7517', '#993556', '#5f5e5a', '#0c447c'];
	let byName = $derived(new Map(defs.map((d) => [d.metric, d])));

	function aggLabel(a: string | undefined): string {
		switch (a) {
			case 'sum':
				return '求和';
			case 'mean':
				return '均值';
			case 'max':
				return '最大';
			case 'min':
				return '最小';
			case 'last':
				return '最近一次';
			case 'count':
				return '计数';
			default:
				return a ?? '—';
		}
	}
	function crossLabel(a: string | undefined): string {
		switch (a) {
			case 'max':
				return '跨来源取最大';
			case 'mean':
				return '跨来源加权均值';
			case 'sum':
				return '求和';
			case 'latest':
				return '最近一次';
			default:
				return a ?? '—';
		}
	}

	// 构建单个分组的图表序列。
	function buildSeriesForGroup(g: MetricGroup): TrendSeries[] {
		return selected
			.filter((m) => g.metrics.includes(m))
			.map((m, i) => {
				const d = byName.get(m);
				return {
					name: d?.display_name_zh ?? m,
					metric: m,
					type: (d?.kind === 'cumulative' ? 'bar' : 'line') as 'bar' | 'line',
					color: palette[i % palette.length],
					unit: d?.preferred_unit || '',
					digits: unitDigits(d?.preferred_unit || ''),
					points: (series[m] ?? []).map((p) => ({ date: p.date, value: p.value }))
				};
			});
	}

	// 有勾选指标的分组视图：每组独立成图、各自单位与坐标轴。
	let groupsView = $derived(
		METRIC_GROUPS.map((g) => {
			const ms = selected.filter((m) => g.metrics.includes(m));
			if (ms.length === 0) return null;
			const s = buildSeriesForGroup(g);
			const units = s.map((x) => x.unit).filter(Boolean);
			const yUnit = units.length > 0 && units.every((u) => u === units[0]) ? units[0] : '';
			const hasData = s.some((x) => x.points.length > 0);
			return { group: g, series: s, yUnit, hasData };
		}).filter((v): v is NonNullable<typeof v> => v !== null)
	);

	// 指标筛选器：按分组展示，组间用分类头分隔。
	let selectableGroups = $derived(
		METRIC_GROUPS.map((g) => ({
			group: g,
			items: defs.filter((d) => g.metrics.includes(d.metric) && d.enabled)
		})).filter((v) => v.items.length > 0)
	);

	function toggle(m: string) {
		selected = selected.includes(m) ? selected.filter((x) => x !== m) : [...selected, m];
	}

	async function load() {
		if (selected.length === 0) {
			series = {};
			return;
		}
		loading = true;
		try {
			const to = todayStr();
			const res = await api.daily(selected, shiftDays(-days + 1, to), to);
			series = res.series;
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	onMount(async () => {
		try {
			defs = (await api.metrics()).metrics.filter((d) => d.enabled);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
		await load();
	});
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-baseline justify-between gap-2">
		<h1 class="text-[15px] font-medium">趋势</h1>
		<RangePicker value={days} onchange={(d) => ((days = d), load())} />
	</div>

	<!-- 指标筛选器：按苹果健康分类分组 -->
	<div class="card p-3">
		<div class="space-y-2.5">
			{#each selectableGroups as { group, items } (group.id)}
				<div>
					<div class="mb-1.5 flex items-center gap-1 text-[11px] font-medium text-[var(--color-ink-500)]">
						<span>{group.icon}</span>
						<span>{group.label}</span>
					</div>
					<div class="flex flex-wrap gap-1.5">
						{#each items as d (d.metric)}
							<button
								class="rounded-full border px-2.5 py-1 text-[12px] transition-colors {selected.includes(
									d.metric
								)
									? 'border-transparent bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
									: 'border-[var(--color-line)] text-[var(--color-ink-700)] hover:bg-[var(--color-line)]/50'}"
								onclick={() => {
									toggle(d.metric);
									load();
								}}
							>
								{d.display_name_zh}
							</button>
						{/each}
					</div>
				</div>
			{/each}
		</div>
	</div>

	{#if error}
		<div class="card px-4 py-3 text-[13px] text-[#a32d2d]">{error}</div>
	{/if}

	{#if selected.length === 0}
		<div class="card flex h-[200px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">
			请至少选择一个指标
		</div>
	{:else}
		{#each groupsView as view (view.group.id)}
			<div class="card p-4">
				<div class="mb-1 flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
					<span class="text-[13px] font-medium"
						>{view.group.icon} {view.group.label}</span
					>
					<span class="text-[11px] text-[var(--color-ink-500)]">{view.group.hint}</span>
				</div>

				{#if loading}
					<div
						class="flex h-[260px] items-center justify-center text-[13px] text-[var(--color-ink-500)]"
					>
						加载中…
					</div>
				{:else if !view.hasData}
					<div
						class="flex h-[260px] flex-col items-center justify-center gap-1 text-[13px] text-[var(--color-ink-500)]"
					>
						<span>该分组暂无数据</span>
						<span class="text-[11px]">所选指标在 {days} 天内没有同步记录</span>
					</div>
				{:else}
					<TrendChart series={view.series} height={260} yUnit={view.yUnit} />
					<div
						class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-[var(--color-ink-500)]"
					>
						{#each view.series as s (s.metric)}
							{@const d = byName.get(s.metric ?? '')}
							<span>
								{s.name} · 日内 {aggLabel(d?.day_agg)} · {crossLabel(d?.cross_source_agg)} ·
								单位 {s.unit || '—'}
							</span>
						{/each}
					</div>
				{/if}
			</div>
		{/each}
	{/if}
</div>
