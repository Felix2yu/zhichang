<script lang="ts">
	import { onMount } from 'svelte';
	import { api, shiftDays, todayStr, type SleepRow } from '#lib/api';
	import RangePicker from '#lib/components/RangePicker.svelte';
	import TrendChart from '#lib/components/TrendChart.svelte';

	let days = $state(30);
	let rows = $state<SleepRow[]>([]);
	let loading = $state(true);
	let error = $state('');

	let stageSeries = $derived([
		{ name: '深睡', type: 'bar' as const, stack: 'sleep', color: '#0c447c', points: rows.map((r) => ({ date: r.local_date, value: r.deep_hr ?? 0 })) },
		{ name: '核心', type: 'bar' as const, stack: 'sleep', color: '#378add', points: rows.map((r) => ({ date: r.local_date, value: r.core_hr ?? 0 })) },
		{ name: 'REM', type: 'bar' as const, stack: 'sleep', color: '#85b7eb', points: rows.map((r) => ({ date: r.local_date, value: r.rem_hr ?? 0 })) }
	]);
	let totalSeries = $derived([
		{ name: '总睡眠', points: rows.map((r) => ({ date: r.local_date, value: r.total_sleep_hr ?? 0 })) }
	]);
	let avg = $derived(
		rows.length
			? rows.reduce((s, r) => s + (r.total_sleep_hr ?? 0), 0) / rows.length
			: null
	);

	async function load() {
		loading = true;
		try {
			const to = todayStr();
			rows = (await api.sleep(shiftDays(-days + 1, to), to)).rows;
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	onMount(load);
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-baseline justify-between gap-2">
		<h1 class="text-[15px] font-medium">睡眠</h1>
		<RangePicker value={days} onchange={(d) => ((days = d), load())} />
	</div>

	{#if error}
		<div class="card px-4 py-3 text-[13px] text-[#a32d2d]">{error}</div>
	{/if}

	<div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">平均总睡眠</div>
			<div class="num mt-1 text-[20px] font-medium">
				{avg === null ? '—' : `${avg.toFixed(1)} h`}
			</div>
		</div>
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">记录天数</div>
			<div class="num mt-1 text-[20px] font-medium">{rows.length}</div>
		</div>
	</div>

	<div class="card p-4">
		<div class="mb-2 text-[13px] font-medium">睡眠分期</div>
		{#if loading}
			<div class="flex h-[260px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">加载中…</div>
		{:else}
			<TrendChart series={stageSeries} height={260} unit="h" digits={1} />
		{/if}
	</div>

	<div class="card p-4">
		<div class="mb-2 text-[13px] font-medium">总睡眠时长</div>
		{#if !loading}
			<TrendChart series={totalSeries} height={220} unit="h" digits={1} />
		{/if}
	</div>

	<div class="card overflow-hidden">
		<table class="w-full text-[13px]">
			<thead class="bg-[#fafbfc] text-[12px] text-[var(--color-ink-500)]">
				<tr>
					<th class="px-4 py-2 text-left font-normal">日期</th>
					<th class="px-4 py-2 text-right font-normal">总睡眠 (h)</th>
					<th class="px-4 py-2 text-right font-normal">深睡</th>
					<th class="px-4 py-2 text-right font-normal">核心</th>
					<th class="px-4 py-2 text-right font-normal">REM</th>
					<th class="px-4 py-2 text-right font-normal">在床</th>
					<th class="px-4 py-2 text-left font-normal">来源</th>
				</tr>
			</thead>
			<tbody>
				{#each [...rows].reverse() as r (r.local_date + r.source)}
					<tr class="border-t border-[var(--color-line)]">
						<td class="num px-4 py-2">{r.local_date}</td>
						<td class="num px-4 py-2 text-right">{(r.total_sleep_hr ?? 0).toFixed(1)}</td>
						<td class="num px-4 py-2 text-right">{(r.deep_hr ?? 0).toFixed(1)}</td>
						<td class="num px-4 py-2 text-right">{(r.core_hr ?? 0).toFixed(1)}</td>
						<td class="num px-4 py-2 text-right">{(r.rem_hr ?? 0).toFixed(1)}</td>
						<td class="num px-4 py-2 text-right">{(r.in_bed_hr ?? 0).toFixed(1)}</td>
						<td class="px-4 py-2">{r.source}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
</div>
