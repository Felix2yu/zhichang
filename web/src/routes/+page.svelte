<script lang="ts">
	import { onMount } from 'svelte';
	import {
		api,
		formatValue,
		shiftDays,
		todayStr,
		type DailyPoint,
		type MetricDef,
		type SleepRow,
		type SummaryItem
	} from '#lib/api';
	import MetricCard from '#lib/components/MetricCard.svelte';
	import TrendChart from '#lib/components/TrendChart.svelte';

	let defs = $state<MetricDef[]>([]);
	let items = $state<SummaryItem[]>([]);
	let steps = $state<DailyPoint[]>([]);
	let hr = $state<DailyPoint[]>([]);
	let sleep = $state<SleepRow[]>([]);
	let updated = $state('');
	let loading = $state(true);
	let error = $state('');

	const focus = [
		'step_count',
		'active_energy',
		'resting_heart_rate',
		'heart_rate',
		'body_mass',
		'heart_rate_variability'
	];

let byName = $derived(new Map(defs.map((d) => [d.metric, d])));
// 关注的指标一律占位：本期没数据的（如这段时间没称体重）渲染成一张
// 显示「—」的卡，而不是从网格里消失。直接过滤掉会让后面几张卡整体前移，
// 看起来像布局错位，也让人不知道到底是「没测」还是「我们没接这个指标」。
// 字典里没有的指标才真的不占位——那种情况下我们连它的名字和单位都不知道。
let cards = $derived(
	focus
		.filter((m) => byName.has(m))
		.map(
			(m) =>
				items.find((i) => i.metric === m) ?? {
					metric: m,
					display_name: '',
					unit: '',
					value: null,
					prev_value: null,
					delta: null,
					delta_pct: null
				}
		)
);
let latestSleep = $derived(sleep.at(-1) ?? null);

	function unitOf(metric: string): string {
		return byName.get(metric)?.preferred_unit ?? '';
	}
	function labelOf(metric: string): string {
		return byName.get(metric)?.display_name_zh ?? metric;
	}

	onMount(async () => {
		try {
			const to = todayStr();
			const from = shiftDays(-29, to);
			const [m, s, d, sl] = await Promise.all([
				api.metrics(),
				api.summary(),
				api.daily(['step_count', 'heart_rate'], from, to),
				api.sleep(shiftDays(-7, to), to)
			]);
			defs = m.metrics;
			items = s.items;
			steps = d.series['step_count'] ?? [];
			hr = d.series['heart_rate'] ?? [];
			sleep = sl.rows;
			updated = s.date;
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	});
</script>

<div class="space-y-4">
	<div class="flex items-baseline justify-between">
		<h1 class="text-[15px] font-medium">今日概览</h1>
		<span class="text-[12px] text-[var(--color-ink-500)]">
			{loading ? '加载中…' : `数据日期 ${updated}`}
		</span>
	</div>

	{#if error}
		<div class="card px-4 py-3 text-[13px] text-[#a32d2d]">{error}</div>
	{/if}

	{#if !loading && cards.length === 0}
		<div class="card px-4 py-3 text-[12px] text-[var(--color-ink-500)]">
			关注的指标暂无数据，可能是还没同步，或这次导出里没有这些指标。
		</div>
	{/if}

	<div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
		{#each cards as c (c.metric)}
			<MetricCard
				label={c.display_name || labelOf(c.metric)}
				value={c.value}
				unit={c.unit || unitOf(c.metric)}
				prev={c.prev_value}
				hint={c.value === null
					? '本期无数据'
					: c.prev_value !== null
						? `前一日 ${formatValue(c.prev_value, c.unit)}`
						: ''}
			/>
		{/each}
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">睡眠时长</div>
			<div class="num mt-1 text-[22px] leading-tight font-medium">
				{formatValue(latestSleep?.total_sleep_hr ?? null, 'h', 1)}
			</div>
			<div class="mt-0.5 text-[11px] text-[var(--color-ink-500)]">
				{latestSleep ? `${latestSleep.local_date} · ${latestSleep.source}` : '本期无数据'}
			</div>
		</div>
	</div>

	<div class="grid gap-3 lg:grid-cols-2">
		<div class="card p-4">
			<div class="mb-2 flex items-baseline justify-between">
				<span class="text-[13px] font-medium">步数 · 近 30 天</span>
				<span class="text-[11px] text-[var(--color-ink-500)]">跨来源取各来源总量最大值</span>
			</div>
			<TrendChart
				series={[{ name: '步数', type: 'bar', points: steps.map((p) => ({ date: p.date, value: p.value })) }]}
				height={240}
				unit="步"
			/>
		</div>
		<div class="card p-4">
			<div class="mb-2 flex items-baseline justify-between">
				<span class="text-[13px] font-medium">心率 · 近 30 天</span>
				<span class="text-[11px] text-[var(--color-ink-500)]">日内均值</span>
			</div>
			<TrendChart
				series={[{ name: '心率', points: hr.map((p) => ({ date: p.date, value: p.value })) }]}
				height={240}
				unit="bpm"
				digits={1}
			/>
		</div>
	</div>
</div>
