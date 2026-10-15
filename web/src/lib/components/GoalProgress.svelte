<script lang="ts">
	import { formatValue } from '#lib/api';

	// 目标进度：把「今天走了多少步 / 离目标还差多少」压成一行一条。
	//
	// 目标存在服务端的 goals 表里（按指标存一个数值），写接口需要
	// ZHICHANG_SETTINGS_TOKEN，所以这里只做展示；没有目标整块不渲染——
	// 一排空进度条只会占位置，什么也没说。
	export type GoalRowView = {
		metric: string;
		label: string;
		unit: string;
		digits: number;
		target: number;
		value: number | null;
	};

	type Props = { rows: GoalRowView[] };

	let { rows }: Props = $props();

	let items = $derived(
		rows.map((r) => {
			const value = r.value ?? 0;
			return {
				...r,
				ratio: r.target > 0 ? Math.min(1, value / r.target) : 0,
				reached: r.value !== null && value >= r.target
			};
		})
	);
</script>

<div class="grid gap-x-6 gap-y-3 sm:grid-cols-2 lg:grid-cols-4">
	{#each items as g (g.metric)}
		<div>
			<div class="flex items-baseline justify-between gap-2">
				<span class="truncate text-[12px] text-[var(--color-ink-500)]">{g.label}</span>
				<span class="num shrink-0 text-[11px] {g.reached ? 'text-[var(--color-brand)]' : 'text-[var(--color-ink-500)]'}">
					{g.value === null ? '—' : Math.round(g.ratio * 100) + '%'}
				</span>
			</div>
			<div class="mt-1 h-1.5 overflow-hidden rounded-full bg-[var(--color-line)]">
				<div
					class="h-full rounded-full transition-[width] duration-300 {g.reached
						? 'bg-[var(--color-brand)]'
						: 'bg-[#4a9e80]'}"
					style="width:{(g.ratio * 100).toFixed(1)}%"
				></div>
			</div>
			<div class="num mt-1 text-[11px] text-[var(--color-ink-500)]">
				{formatValue(g.value, g.unit, g.digits)} / {formatValue(g.target, g.unit, g.digits)}
			</div>
		</div>
	{/each}
</div>
