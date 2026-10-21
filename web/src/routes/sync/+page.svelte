<script lang="ts">
  import { onMount } from 'svelte';
  import { api, describeRun, eventCounts, type SourceRow, type SyncStatus } from '#lib/api';
  import Card from '#lib/components/ui/Card.svelte';
  import CardHeader from '#lib/components/ui/CardHeader.svelte';
  import DataTable from '#lib/components/DataTable.svelte';
  import EmptyState from '#lib/components/ui/EmptyState.svelte';
  import ErrorBanner from '#lib/components/ui/ErrorBanner.svelte';
  import PageHeader from '#lib/components/ui/PageHeader.svelte';

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
  <PageHeader>数据源与同步</PageHeader>

  <ErrorBanner {error} />

  <div class="grid gap-3 lg:grid-cols-2">
    <Card>
      <CardHeader>最近同步</CardHeader>
      {#if status?.last_run}
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[13px]">
          <dt class="text-[var(--color-ink-500)]">时间</dt>
          <dd class="num">{fmt(status.last_run.received_at)}</dd>
          <dt class="text-[var(--color-ink-500)]">来源</dt>
          <dd>
            {status.last_run.transport}{status.last_run.automation_id
              ? ` · ${status.last_run.automation_id}`
              : ''}
          </dd>
          <dt class="text-[var(--color-ink-500)]">条数</dt>
          <dd class="num">
            {describeRun(status.last_run)}
            {#if status.last_run.duplicates}· 重复 {status.last_run.duplicates}{/if}
          </dd>
          <dt class="text-[var(--color-ink-500)]">状态</dt>
          <dd>{status.last_run.status}</dd>
        </dl>
      {:else}
        <p class="text-[13px] text-[var(--color-ink-500)]">
          {loading ? '加载中…' : '尚无同步记录'}
        </p>
      {/if}
    </Card>

    <Card>
      <CardHeader>数据缺口</CardHeader>
      {#if status && (status.missing_days?.length ?? 0) > 0}
        <div class="flex flex-wrap gap-1.5">
          {#each status.missing_days ?? [] as d (d)}
            <span
              class="rounded bg-[var(--color-danger-soft)] px-2 py-0.5 text-[11px] text-[var(--color-danger)]"
              >{d}</span
            >
          {/each}
        </div>
      {:else}
        <p class="text-[13px] text-[var(--color-ink-500)]">近期没有缺失日</p>
      {/if}
    </Card>
  </div>

  <Card pad="none" class="overflow-hidden">
    <div class="border-b border-[var(--color-line)] px-4 py-2 text-[13px] font-medium">
      数据来源
    </div>
    <DataTable
      rows={sources}
      rowKey={(s) => s.source}
      columns={[
        { label: '来源' },
        { label: '设备类别' },
        { label: '样本数', align: 'right' },
        { label: '首次出现' },
        { label: '最近出现' }
      ]}
    >
      {#snippet children(s)}
        <td class="px-4 py-2">{s.source}</td>
        <td class="px-4 py-2">
          {s.device_class === 'watch'
            ? '手表'
            : s.device_class === 'phone'
              ? '手机'
              : s.device_class === 'app'
                ? '应用'
                : '未知'}
        </td>
        <td class="num px-4 py-2 text-right">{s.samples}</td>
        <td class="num px-4 py-2">{fmt(s.first_seen)}</td>
        <td class="num px-4 py-2">{fmt(s.last_seen)}</td>
      {/snippet}
      {#snippet empty()}
        <EmptyState variant="row" colspan={5} title="暂无来源记录" />
      {/snippet}
    </DataTable>
  </Card>

  <Card pad="none" class="overflow-hidden">
    <div class="border-b border-[var(--color-line)] px-4 py-2 text-[13px] font-medium">
      同步记录
    </div>
    <DataTable
      rows={status?.recent_runs ?? []}
      rowKey={(r) => String(r.id)}
      columns={[
        { label: '时间' },
        { label: '自动化' },
        { label: 'Session' },
        { label: '收到', align: 'right' },
        { label: '入库', align: 'right' },
        { label: '重复', align: 'right' },
        { label: '事件', align: 'right' },
        { label: '状态' }
      ]}
    >
      {#snippet children(r)}
        <td class="num px-4 py-2">{fmt(r.received_at)}</td>
        <td class="px-4 py-2">{r.automation_id || '—'}</td>
        <td class="px-4 py-2">{r.session_id || '—'}</td>
        <td class="num px-4 py-2 text-right">{r.sample_count}</td>
        <td class="num px-4 py-2 text-right">{r.inserted}</td>
        <td class="num px-4 py-2 text-right">{r.duplicates}</td>
        <td class="num px-4 py-2 text-right">{eventCounts(r) || '—'}</td>
        <td class="px-4 py-2">{r.status}</td>
      {/snippet}
      {#snippet empty()}
        <EmptyState variant="row" colspan={8} title={loading ? '加载中…' : '暂无记录'} />
      {/snippet}
    </DataTable>
  </Card>
</div>
