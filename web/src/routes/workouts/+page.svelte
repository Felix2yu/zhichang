<script lang="ts">
	import { onMount } from 'svelte';
	import { api, shiftDays, todayStr, type WorkoutRow } from '#lib/api';
	import RangePicker from '#lib/components/RangePicker.svelte';

	let days = $state(90);
	let rows = $state<WorkoutRow[]>([]);
	let loading = $state(true);
	let error = $state('');

	let totalMin = $derived(rows.reduce((s, r) => s + r.duration_min, 0));
	let totalEnergy = $derived(rows.reduce((s, r) => s + (r.total_energy ?? 0), 0));
	let byType = $derived.by(() => {
		const m = new Map<string, { count: number; minutes: number }>();
		for (const r of rows) {
			const cur = m.get(r.name) ?? { count: 0, minutes: 0 };
			cur.count += 1;
			cur.minutes += r.duration_min;
			m.set(r.name, cur);
		}
		return [...m.entries()].sort((a, b) => b[1].minutes - a[1].minutes);
	});

	async function load() {
		loading = true;
		try {
			const to = todayStr();
			rows = (await api.workouts(shiftDays(-days + 1, to), to)).rows;
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	onMount(load);

	function fmtTime(iso: string): string {
		const d = new Date(iso);
		return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
	}
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-baseline justify-between gap-2">
		<h1 class="text-[15px] font-medium">运动</h1>
		<RangePicker value={days} onchange={(d) => ((days = d), load())} />
	</div>

	{#if error}
		<div class="card px-4 py-3 text-[13px] text-[#a32d2d]">{error}</div>
	{/if}

	<div class="grid grid-cols-3 gap-3">
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">次数</div>
			<div class="num mt-1 text-[20px] font-medium">{rows.length}</div>
		</div>
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">总时长</div>
			<div class="num mt-1 text-[20px] font-medium">{Math.round(totalMin)} 分钟</div>
		</div>
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">总消耗</div>
			<div class="num mt-1 text-[20px] font-medium">{Math.round(totalEnergy)} kcal</div>
		</div>
	</div>

	{#if byType.length > 0}
		<div class="card p-4">
			<div class="mb-2 text-[13px] font-medium">按类型</div>
			<div class="flex flex-wrap gap-x-6 gap-y-1 text-[12px]">
				{#each byType as [name, v] (name)}
					<span class="text-[var(--color-ink-700)]">
						{name}
						<span class="num text-[var(--color-ink-500)]">{v.count} 次 · {Math.round(v.minutes)} 分钟</span>
					</span>
				{/each}
			</div>
		</div>
	{/if}

	<div class="card overflow-hidden">
		<table class="w-full text-[13px]">
			<thead class="bg-[#fafbfc] text-[12px] text-[var(--color-ink-500)]">
				<tr>
					<th class="px-4 py-2 text-left font-normal">开始</th>
					<th class="px-4 py-2 text-left font-normal">项目</th>
					<th class="px-4 py-2 text-right font-normal">时长 (min)</th>
					<th class="px-4 py-2 text-right font-normal">消耗</th>
					<th class="px-4 py-2 text-right font-normal">距离</th>
					<th class="px-4 py-2 text-left font-normal">来源</th>
				</tr>
			</thead>
			<tbody>
				{#each rows as r (r.start + r.name)}
					<tr class="border-t border-[var(--color-line)]">
						<td class="num px-4 py-2">{fmtTime(r.start)}</td>
						<td class="px-4 py-2">{r.name}</td>
						<td class="num px-4 py-2 text-right">{r.duration_min.toFixed(0)}</td>
						<td class="num px-4 py-2 text-right">
							{r.total_energy ? `${Math.round(r.total_energy)} ${r.energy_unit ?? ''}` : '—'}
						</td>
						<td class="num px-4 py-2 text-right">
							{r.total_distance ? `${r.total_distance.toFixed(2)} ${r.distance_unit ?? ''}` : '—'}
						</td>
						<td class="px-4 py-2">{r.source}</td>
					</tr>
				{:else}
					<tr>
						<td colspan="6" class="px-4 py-6 text-center text-[var(--color-ink-500)]">
							{loading ? '加载中…' : '暂无运动记录'}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
</div>
