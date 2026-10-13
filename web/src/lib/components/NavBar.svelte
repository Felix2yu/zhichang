<script lang="ts">
	import { page } from '$app/state';

	const links = [
		{ href: '/', label: '概览' },
		{ href: '/trends', label: '趋势' },
		{ href: '/sleep', label: '睡眠' },
		{ href: '/workouts', label: '运动' },
		{ href: '/sync', label: '数据源' }
	];

	let current = $derived(page.url.pathname);
</script>

<header class="border-b border-[var(--color-line)] bg-white">
	<div class="mx-auto flex max-w-6xl items-center gap-6 px-5 py-3">
		<a href="/" class="flex items-baseline gap-2">
			<span class="text-[17px] font-medium tracking-wide">知常</span>
			<span class="text-[12px] text-[var(--color-ink-500)]">复命曰常，知常曰明</span>
		</a>
		<nav class="flex items-center gap-1 text-[13px]">
			{#each links as l (l.href)}
				<a
					href={l.href}
					class="rounded-md px-2.5 py-1.5 transition-colors {current === l.href
						? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
						: 'text-[var(--color-ink-700)] hover:bg-[var(--color-line)]/60'}"
				>
					{l.label}
				</a>
			{/each}
		</nav>
	</div>
</header>
