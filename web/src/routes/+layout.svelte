<script lang="ts">
	import '../app.css';
	import NavBar from '#lib/components/NavBar.svelte';
	import { initTheme } from '#lib/theme.svelte';

	let { children } = $props();

	initTheme();
</script>

<div class="min-h-screen bg-[var(--color-bg)]">
	<NavBar />
	<main class="mx-auto max-w-6xl px-5 py-5">
		{@render children()}
	</main>
</div>
