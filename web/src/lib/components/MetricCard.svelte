<script lang="ts">
	import { formatValue } from '#lib/api';
	import { TONE_CLASS, toneOf } from '#lib/metric-direction';
	import Sparkline from '#lib/components/Sparkline.svelte';

	// 概览卡片 = 一个指标的一句结论。
	//
	// 对比基准是「我的常态」（近 30 天中位数）而不是「昨天」：
	// 体重、静息心率这类指标的日内噪声远大于日间趋势，日环比几乎全是噪声，
	// 看久了只会让人以为数据坏了。常态才回答得出「今天偏离自己多少」。
	//
	// 配色按指标方向走，不是「涨红跌绿」：步数涨了是好事不该标红，
	// 详见 metric-direction.ts 的理由。
	type Props = {
		label: string;
		value: number | null;
		unit?: string;
		digits?: number;
		/** 指标 id，用于查涨跌方向；缺省视为中性（只用次级灰） */
		metric?: string;
		/** 常态值；样本不足时为 null，此时不显示对比 */
		baseline?: number | null;
		deviationPct?: number | null;
		recent?: { date: string; value: number }[];
		/** 常态窗口天数：把「常态是什么」写在卡上，免得用户猜 */
		windowDays?: number;
		/** 对比日说明：数据不是今天的时必须说清楚 */
		dateNote?: string;
		/** 空态或补充说明 */
		hint?: string;
		loading?: boolean;
	};

	let {
		label,
		value,
		unit = '',
		digits = 0,
		metric = '',
		baseline = null,
		deviationPct = null,
		recent = [],
		windowDays = 30,
		dateNote = '',
		hint = '',
		loading = false
	}: Props = $props();

	let toneClass = $derived(TONE_CLASS[toneOf(metric, deviationPct)]);
</script>

<div class="card px-4 py-3">
	<div class="flex items-baseline justify-between gap-2">
		<span class="truncate text-[12px] text-[var(--color-ink-500)]">{label}</span>
		{#if deviationPct !== null}
			<span class="num shrink-0 text-[11px] {toneClass}" title="较近 {windowDays} 天常态">
				{deviationPct >= 0 ? '+' : ''}{deviationPct.toFixed(1)}%
			</span>
		{/if}
	</div>

	{#if loading}
		<div class="mt-1.5 h-[22px] w-20 animate-pulse rounded bg-[var(--color-line)]"></div>
	{:else}
		<div class="num mt-1 text-[22px] leading-tight font-medium">
			{formatValue(value, unit, digits)}
		</div>
	{/if}

	<div class="mt-0.5 text-[11px] text-[var(--color-ink-500)]">
		{#if loading}
			加载中…
		{:else if value === null}
			{hint || '本期无数据'}
		{:else if baseline !== null}
			常态 {formatValue(baseline, unit, digits)} · 近 {windowDays} 天
		{:else}
			{hint || '常态数据不足'}
		{/if}
	</div>

	<div class="mt-1.5">
		<Sparkline points={recent} baseline={baseline} />
	</div>

	{#if value !== null && dateNote}
		<div class="mt-0.5 text-[11px] text-[var(--color-ink-500)]">{dateNote}</div>
	{/if}
</div>
