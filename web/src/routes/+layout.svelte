<script lang="ts">
	import '../app.css';
	import NavBar from '#lib/components/NavBar.svelte';

	let { children } = $props();
</script>

<div class="min-h-screen">
	<NavBar />
	<main class="mx-auto max-w-6xl px-5 py-5">
		{@render children()}
	</main>
</div>
