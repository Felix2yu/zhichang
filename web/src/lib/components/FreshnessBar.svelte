<script lang="ts">
	import type { SyncStatus } from '#lib/api';

	// 数据新鲜度条：概览页第一屏第一行。
	//
	// 为什么放在最上面：自托管管线的失效是静默的——iOS 锁屏、低电量、自动化停了，
	// 界面上表现为「今天数字有点低」。如果页面不先说明数据到了哪一刻，
	// 用户会把「还没同步」误读成「今天没怎么动」，这是最容易让人白担心的一类误读。
	type Props = {
		status: SyncStatus | null;
		loading?: boolean;
		/** 卡片实际展示的数据日期说明（今天没数据时会是更早的一天） */
		dataNote?: string;
	};

	let { status, loading = false, dataNote = '' }: Props = $props();

	/** 「多久之前」的中文说法；超过两天就按天算，小时数没有意义了。 */
	function ago(iso: string): string {
		const mins = Math.floor((Date.now() - new Date(iso).getTime()) / 60000);
		if (mins < 1) return '刚刚';
		if (mins < 60) return `${mins} 分钟前`;
		const hours = Math.floor(mins / 60);
		if (hours < 24) return `${hours} 小时前`;
		return `${Math.floor(hours / 24)} 天前`;
	}

	// 6 小时内算新鲜（HAE 通常一天同步几次），24 小时后基本是断流了
	let level = $derived.by(() => {
		const at = status?.last_run?.received_at;
		if (!at) return 'unknown';
		const hours = (Date.now() - new Date(at).getTime()) / 3600000;
		if (hours <= 6) return 'fresh';
		if (hours <= 24) return 'lagging';
		return 'stale';
	});

	const dot: Record<string, string> = {
		fresh: 'bg-[#1d9e75]',
		lagging: 'bg-[#ba7517]',
		stale: 'bg-[#a32d2d]',
		unknown: 'bg-[var(--color-ink-500)]'
	};
</script>

<div class="card flex flex-wrap items-center gap-x-4 gap-y-1.5 px-4 py-2.5 text-[12px]">
	<span class="flex items-center gap-1.5">
		<span class="h-2 w-2 shrink-0 rounded-full {dot[level]}"></span>
		<span class="text-[var(--color-ink-700)]">
			{#if loading}
				正在读取同步状态…
			{:else if status?.last_run}
				最后同步 {ago(status.last_run.received_at)}
				<span class="num text-[var(--color-ink-500)]">
					· 收到 {status.last_run.sample_count} 条，入库 {status.last_run.inserted}
				</span>
			{:else}
				尚无同步记录
			{/if}
		</span>
	</span>

	{#if dataNote}
		<span class="text-[var(--color-ink-500)]">{dataNote}</span>
	{/if}

	<span class="flex-1"></span>

	{#if status && status.missing_days.length > 0}
		<a href="/sync" class="text-[#a32d2d] hover:underline">
			近 30 天缺 {status.missing_days.length} 天
		</a>
	{/if}
	<a href="/sync" class="text-[var(--color-brand)] hover:underline">数据源详情</a>
</div>
