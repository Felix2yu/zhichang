<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type WorkoutRow } from '#lib/api';
	import { lastDays, type Range } from '#lib/range';
	import RangePicker from '#lib/components/RangePicker.svelte';
	import WorkoutDetail from '#lib/components/WorkoutDetail.svelte';
	import { prefs, formatDuration } from '#lib/prefs.svelte';

	let range = $state<Range>(lastDays(prefs.rangeDays));
	let rows = $state<WorkoutRow[]>([]);
	let loading = $state(true);
	let error = $state('');
	// 点开的那次运动：轨迹与逐点心率只在点开时才取（一次列表 100+ 条，全取就是几百次请求）
	let open = $state<WorkoutRow | null>(null);

	let totalMin = $derived(rows.reduce((s, r) => s + r.duration_min, 0));
	/**
	 * 「消耗」按主动能量口径：Apple 运动记录里显示的就是主动能量，
	 * total_energy 含基础代谢，拿它求和会比健康 App 高出约三成（一年实测 +29.4%）。
	 * 上游没给主动能量的记录回落用总能量，并在数字旁标「总」——
	 * 两种口径混在同一个表格里却不作区分，是会让人做出错误比较的那种显示错误。
	 */
	const shownEnergy = (r: WorkoutRow) => r.active_energy ?? r.total_energy ?? null;
	const isTotalCaliber = (r: WorkoutRow) => r.active_energy == null && r.total_energy != null;
	let totalEnergy = $derived(rows.reduce((s, r) => s + (shownEnergy(r) ?? 0), 0));
	let totalAllEnergy = $derived(rows.reduce((s, r) => s + (r.total_energy ?? 0), 0));
		// 消耗单位取自数据本身：入库时能量已统一到 kcal，这里不写死字面量，
	// 否则上游再换一次单位（kJ）就会把数值标成 kcal。
	// 单位混用时求和本身没有意义，此时不标单位；无数据时回落到入库口径 kcal。
	let energyUnit = $derived.by(() => {
		const units = new Set(
			rows.map((r) => (r.active_energy != null ? r.active_energy_unit : r.energy_unit)).filter(
				(u): u is string => Boolean(u)
			)
		);
		if (units.size === 1) return [...units][0];
		return units.size === 0 ? 'kcal' : '';
	});
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
			rows = (await api.workouts(range.from, range.to)).rows;
			// 换区间后原来点开的那条可能已经不在列表里了，别留一个看不见的选中态
			const picked = open;
			if (picked && !rows.some((r) => r.id === picked.id)) open = null;
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
		<RangePicker value={range} onchange={(r) => ((range = r), load())} />
	</div>

	{#if error}
		<div class="card px-4 py-3 text-[13px] text-[var(--color-danger)]">{error}</div>
	{/if}

	<div class="grid grid-cols-3 gap-3">
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">次数</div>
			<div class="num mt-1 text-[20px] font-medium">{rows.length}</div>
		</div>
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">总时长</div>
			<div class="num mt-1 text-[20px] font-medium">{formatDuration(totalMin)}</div>
		</div>
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">总消耗（主动）</div>
			<div class="num mt-1 text-[20px] font-medium"
				>{Math.round(totalEnergy)}{energyUnit ? ` ${energyUnit}` : ''}</div
			>
			{#if totalAllEnergy > totalEnergy}
				<div class="mt-0.5 text-[11px] text-[var(--color-ink-500)]">
					含基础代谢 {Math.round(totalAllEnergy)}{energyUnit ? ` ${energyUnit}` : ''}
				</div>
			{/if}
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
			<thead class="bg-[var(--color-surface-2)] text-[12px] text-[var(--color-ink-500)]">
				<tr>
					<th class="px-4 py-2 text-left font-normal">开始</th>
					<th class="px-4 py-2 text-left font-normal">项目</th>
					<th class="px-4 py-2 text-right font-normal">时长 (min)</th>
					<th
						class="px-4 py-2 text-right font-normal"
						title="主动能量口径（不含基础代谢），与健康 App 一致">消耗 (kcal)</th>
					<th class="px-4 py-2 text-right font-normal">距离</th>
					<th class="px-4 py-2 text-right font-normal">平均心率</th>
					<th class="px-4 py-2 text-right font-normal">最大心率</th>
					<th class="px-4 py-2 text-left font-normal">来源</th>
				</tr>
			</thead>
			<tbody>
				{#each rows as r (r.id)}
					<tr
						class="cursor-pointer border-t border-[var(--color-line)] hover:bg-[var(--color-surface-2)]"
						class:bg-[var(--color-surface-3)]={open?.id === r.id}
						title="查看轨迹与逐点心率"
						onclick={() => (open = open?.id === r.id ? null : r)}
					>
						<td class="num px-4 py-2">{fmtTime(r.start)}</td>
						<td class="px-4 py-2">{r.name}</td>
						<td class="num px-4 py-2 text-right">{r.duration_min.toFixed(0)}</td>
						<td class="num px-4 py-2 text-right">
							{#if shownEnergy(r) != null}
								{Math.round(shownEnergy(r) ?? 0)}
								{#if isTotalCaliber(r)}
									<!-- 老记录上游没给主动能量，只有含基础代谢的总消耗：
									     标出来，否则两行数字看着同口径，人会拿它们互相比较。 -->
									<span
										class="text-[11px] text-[var(--color-ink-500)]"
										title="该记录上游未提供主动能量，此处为含基础代谢的总消耗">总</span
									>
								{/if}
							{:else}
								—
							{/if}
						</td>
						<td class="num px-4 py-2 text-right">
							{r.total_distance ? `${r.total_distance.toFixed(2)} ${r.distance_unit ?? ''}` : '—'}
						</td>
						<td class="num px-4 py-2 text-right">{r.avg_hr ? Math.round(r.avg_hr) : '—'}</td>
						<td class="num px-4 py-2 text-right">{r.max_hr ? Math.round(r.max_hr) : '—'}</td>
						<td class="px-4 py-2">{r.source}</td>
					</tr>
				{:else}
					<tr>
						<td colspan="8" class="px-4 py-6 text-center text-[var(--color-ink-500)]">
							{loading ? '加载中…' : '暂无运动记录'}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>

	{#if open}
		<WorkoutDetail workout={open} onclose={() => (open = null)} />
	{/if}
</div>
