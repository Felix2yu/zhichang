<script lang="ts">
	import { onMount } from 'svelte';
	import { api, shiftDays, todayStr, type DailyPoint, type MetricDef } from '#lib/api';
	import RangePicker from '#lib/components/RangePicker.svelte';
	import TrendChart from '#lib/components/TrendChart.svelte';

	let defs = $state<MetricDef[]>([]);
	let selected = $state<string[]>(['step_count', 'heart_rate']);
	let days = $state(30);
	let series = $state<Record<string, DailyPoint[]>>({});
	let loading = $state(true);
	let error = $state('');

	let palette = ['#0f6e56', '#185fa5', '#ba7517', '#993556', '#5f5e5a', '#0c447c'];
	let byName = $derived(new Map(defs.map((d) => [d.metric, d])));
	let chartSeries = $derived(
		selected.map((m, i) => ({
			name: byName.get(m)?.display_name_zh ?? m,
			type: (byName.get(m)?.kind === 'cumulative' ? 'bar' : 'line') as 'bar' | 'line',
			color: palette[i % palette.length],
			points: (series[m] ?? []).map((p) => ({ date: p.date, value: p.value }))
		}))
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

	<div class="card p-3">
		<div class="flex flex-wrap gap-1.5">
			{#each defs as d (d.metric)}
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

	{#if error}
		<div class="card px-4 py-3 text-[13px] text-[#a32d2d]">{error}</div>
	{/if}

	<div class="card p-4">
		{#if loading}
			<div class="flex h-[320px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">
				加载中…
			</div>
		{:else if chartSeries.length === 0}
			<div class="flex h-[320px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">
				请至少选择一个指标
			</div>
		{:else}
			<TrendChart series={chartSeries} height={320} />
			<div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-[var(--color-ink-500)]">
				{#each selected as m (m)}
					<span>
						{byName.get(m)?.display_name_zh ?? m} · 日内 {byName.get(m)?.day_agg === 'sum'
							? '求和'
							: byName.get(m)?.day_agg === 'mean'
								? '均值'
								: byName.get(m)?.day_agg}
						· 跨来源 {byName.get(m)?.cross_source_agg === 'max' ? '取最大' : '加权均值'}
						· 单位 {byName.get(m)?.preferred_unit || '—'}
					</span>
				{/each}
			</div>
		{/if}
	</div>
</div>
