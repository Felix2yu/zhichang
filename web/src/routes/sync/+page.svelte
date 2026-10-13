<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type SourceRow, type SyncStatus } from '#lib/api';

	let status = $state<SyncStatus | null>(null);
	let sources = $state<SourceRow[]>([]);
	let loading = $state(true);
	let error = $state('');

	onMount(async () => {
		try {
			const [s, src] = await Promise.all([api.syncStatus(30), api.sources()]);
			status = s;
			sources = src.sources;
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	});

	function fmt(iso: string): string {
		const d = new Date(iso);
		return d.toLocaleString('zh-CN', { hour12: false });
	}
</script>

<div class="space-y-4">
	<h1 class="text-[15px] font-medium">数据源与同步</h1>

	{#if error}
		<div class="card px-4 py-3 text-[13px] text-[#a32d2d]">{error}</div>
	{/if}

	<div class="grid gap-3 lg:grid-cols-2">
		<div class="card p-4">
			<div class="mb-2 text-[13px] font-medium">最近同步</div>
			{#if status?.last_run}
				<dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[13px]">
					<dt class="text-[var(--color-ink-500)]">时间</dt>
					<dd class="num">{fmt(status.last_run.received_at)}</dd>
					<dt class="text-[var(--color-ink-500)]">来源</dt>
					<dd>{status.last_run.transport}{status.last_run.automation_id ? ` · ${status.last_run.automation_id}` : ''}</dd>
					<dt class="text-[var(--color-ink-500)]">样本</dt>
					<dd class="num">
						收到 {status.last_run.sample_count} · 入库 {status.last_run.inserted} · 重复 {status.last_run.duplicates}
					</dd>
					<dt class="text-[var(--color-ink-500)]">状态</dt>
					<dd>{status.last_run.status}</dd>
				</dl>
			{:else}
				<p class="text-[13px] text-[var(--color-ink-500)]">{loading ? '加载中…' : '尚无同步记录'}</p>
			{/if}
		</div>

		<div class="card p-4">
			<div class="mb-2 text-[13px] font-medium">数据缺口</div>
			{#if status && (status.missing_days?.length ?? 0) > 0}
				<div class="flex flex-wrap gap-1.5">
					{#each status.missing_days ?? [] as d (d)}
						<span class="rounded bg-[#fcebeb] px-2 py-0.5 text-[11px] text-[#a32d2d]">{d}</span>
					{/each}
				</div>
			{:else}
				<p class="text-[13px] text-[var(--color-ink-500)]">近期没有缺失日</p>
			{/if}
		</div>
	</div>

	<div class="card overflow-hidden">
		<div class="border-b border-[var(--color-line)] px-4 py-2 text-[13px] font-medium">数据来源</div>
		<table class="w-full text-[13px]">
			<thead class="bg-[#fafbfc] text-[12px] text-[var(--color-ink-500)]">
				<tr>
					<th class="px-4 py-2 text-left font-normal">来源</th>
					<th class="px-4 py-2 text-left font-normal">设备类别</th>
					<th class="px-4 py-2 text-right font-normal">样本数</th>
					<th class="px-4 py-2 text-left font-normal">首次出现</th>
					<th class="px-4 py-2 text-left font-normal">最近出现</th>
				</tr>
			</thead>
			<tbody>
				{#each sources as s (s.source)}
					<tr class="border-t border-[var(--color-line)]">
						<td class="px-4 py-2">{s.source}</td>
						<td class="px-4 py-2">
							{s.device_class === 'watch' ? '手表' : s.device_class === 'phone' ? '手机' : s.device_class === 'app' ? '应用' : '未知'}
						</td>
						<td class="num px-4 py-2 text-right">{s.samples}</td>
						<td class="num px-4 py-2">{fmt(s.first_seen)}</td>
						<td class="num px-4 py-2">{fmt(s.last_seen)}</td>
					</tr>
				{:else}
					<tr>
						<td colspan="5" class="px-4 py-6 text-center text-[var(--color-ink-500)]">暂无来源记录</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>

	<div class="card overflow-hidden">
		<div class="border-b border-[var(--color-line)] px-4 py-2 text-[13px] font-medium">同步记录</div>
		<table class="w-full text-[13px]">
			<thead class="bg-[#fafbfc] text-[12px] text-[var(--color-ink-500)]">
				<tr>
					<th class="px-4 py-2 text-left font-normal">时间</th>
					<th class="px-4 py-2 text-left font-normal">自动化</th>
					<th class="px-4 py-2 text-left font-normal">Session</th>
					<th class="px-4 py-2 text-right font-normal">收到</th>
					<th class="px-4 py-2 text-right font-normal">入库</th>
					<th class="px-4 py-2 text-right font-normal">重复</th>
					<th class="px-4 py-2 text-left font-normal">状态</th>
				</tr>
			</thead>
			<tbody>
				{#each status?.recent_runs ?? [] as r (r.id)}
					<tr class="border-t border-[var(--color-line)]">
						<td class="num px-4 py-2">{fmt(r.received_at)}</td>
						<td class="px-4 py-2">{r.automation_id || '—'}</td>
						<td class="px-4 py-2">{r.session_id || '—'}</td>
						<td class="num px-4 py-2 text-right">{r.sample_count}</td>
						<td class="num px-4 py-2 text-right">{r.inserted}</td>
						<td class="num px-4 py-2 text-right">{r.duplicates}</td>
						<td class="px-4 py-2">{r.status}</td>
					</tr>
				{:else}
					<tr>
						<td colspan="7" class="px-4 py-6 text-center text-[var(--color-ink-500)]">暂无记录</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
</div>
