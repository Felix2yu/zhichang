<script lang="ts">
	type Props = {
		value: number;
		options?: { label: string; days: number }[];
		onchange: (days: number) => void;
	};

	let { value, options, onchange }: Props = $props();

	const defaults = [
		{ label: '7 天', days: 7 },
		{ label: '30 天', days: 30 },
		{ label: '90 天', days: 90 },
		{ label: '365 天', days: 365 }
	];

	let items = $derived(options ?? defaults);
</script>

<div class="inline-flex overflow-hidden rounded-md border border-[var(--color-line)] bg-white">
	{#each items as it (it.days)}
		<button
			class="px-3 py-1.5 text-[12px] transition-colors {value === it.days
				? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
				: 'text-[var(--color-ink-700)] hover:bg-[var(--color-line)]/50'}"
			onclick={() => onchange(it.days)}
		>
			{it.label}
		</button>
	{/each}
</div>
