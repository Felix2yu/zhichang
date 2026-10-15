<script lang="ts">
	import Segmented from './Segmented.svelte';

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

<Segmented value={value} options={items.map((it) => ({ label: it.label, value: it.days }))} {onchange} />
