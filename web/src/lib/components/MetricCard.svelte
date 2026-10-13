<script lang="ts">
	import { formatValue } from '#lib/api';

	type Props = {
		label: string;
		value: number | null;
		unit?: string;
		prev?: number | null;
		digits?: number;
		hint?: string;
	};

	let { label, value, unit = '', prev = null, digits = 0, hint = '' }: Props = $props();

	let delta = $derived(value !== null && prev !== null ? value - prev : null);
	let pct = $derived(delta !== null && prev ? (delta / prev) * 100 : null);
</script>

<div class="card px-4 py-3">
	<div class="flex items-baseline justify-between">
		<span class="text-[12px] text-[var(--color-ink-500)]">{label}</span>
		{#if pct !== null}
			<span
				class="num text-[11px] {pct >= 0 ? 'text-[#a32d2d]' : 'text-[#0f6e56]'}"
				title="较上一日"
			>
				{pct >= 0 ? '+' : ''}{pct.toFixed(1)}%
			</span>
		{/if}
	</div>
	<div class="num mt-1 text-[22px] leading-tight font-medium">
		{formatValue(value, unit, digits)}
	</div>
	{#if hint}
		<div class="mt-0.5 text-[11px] text-[var(--color-ink-500)]">{hint}</div>
	{/if}
</div>
