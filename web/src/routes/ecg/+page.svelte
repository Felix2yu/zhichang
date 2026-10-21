<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type EcgAnalysis, type EcgWaveform } from '#lib/api';
  import { lastDays, type Range } from '#lib/range';
  import RangePicker from '#lib/components/RangePicker.svelte';
  import Card from '#lib/components/ui/Card.svelte';
  import DataTable from '#lib/components/DataTable.svelte';
  import EmptyState from '#lib/components/ui/EmptyState.svelte';
  import ErrorBanner from '#lib/components/ui/ErrorBanner.svelte';
  import PageHeader from '#lib/components/ui/PageHeader.svelte';
  import InfoNote from '#lib/components/ui/InfoNote.svelte';
  import EcgVerdictCard from '#lib/components/EcgVerdictCard.svelte';
  import EcgIndexCard from '#lib/components/EcgIndexCard.svelte';
  import EcgTrendChart from '#lib/components/EcgTrendChart.svelte';
  import EcgDistributionChart from '#lib/components/EcgDistributionChart.svelte';
  import EcgComparisonCard from '#lib/components/EcgComparisonCard.svelte';
  import EcgStripChart from '#lib/components/EcgStripChart.svelte';
  import { QUALITY_LABELS, RHYTHM_LABELS } from '#lib/ecg-levels';
  import { prefs } from '#lib/prefs.svelte';

  let range = $state<Range>(lastDays(prefs.rangeDays));
  let analysis = $state<EcgAnalysis | null>(null);
  let loading = $state(true);
  let error = $state('');

  // 选中态由趋势图与记录列表共享：点哪边都是「在该行下方展开波形」。
  let selectedId = $state<number | null>(null);
  let waveform = $state<EcgWaveform | null>(null);
  let waveLoading = $state(false);
  let waveError = $state('');
  // 只有用户主动点的选中才需要把它滚进视野；进页面时的默认选中不滚动。
  let revealId = $state<number | null>(null);

  // 分布图点击 → 记录列表筛选（纯前端状态，不回插服务端）
  let filterCode = $state<string | null>(null);

  let records = $derived(analysis?.records ?? []);
  let shown = $derived(filterCode ? records.filter((r) => r.code === filterCode) : records);
  let selected = $derived(records.find((r) => r.id === selectedId) ?? null);
  let dots = $derived(
    [...records]
      .slice(-12)
      .map((r) => ({ code: r.code, date: r.date, clock: r.clock, label: r.label }))
  );

  async function load() {
    loading = true;
    error = '';
    try {
      analysis = await api.ecgAnalysis(range.from, range.to);
      filterCode = null;
      // 默认看最近一次测量（有波形的优先——没波形的记录点开也只有一句说明）
      const latest =
        [...(analysis?.records ?? [])].reverse().find((r) => r.beats !== null) ??
        (analysis?.records ?? []).at(-1) ??
        null;
      await select(latest?.id ?? null, { reveal: false });
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      analysis = null;
    } finally {
      loading = false;
    }
  }

  async function select(id: number | null, opts?: { reveal?: boolean }) {
    // 再点已选中的一行 = 收起
    if (id !== null && id === selectedId) {
      selectedId = null;
      revealId = null;
      waveform = null;
      waveError = '';
      return;
    }
    selectedId = id;
    // 从趋势图下钻、但这条记录被分布筛选挡住了时，展开块会落在看不见的行上——
    // 先把筛选清掉，选中一定可见。
    if (id !== null && filterCode && records.find((r) => r.id === id)?.code !== filterCode) {
      filterCode = null;
    }
    revealId = opts?.reveal === false ? null : id;
    waveform = null;
    waveError = '';
    if (id === null) return;
    waveLoading = true;
    try {
      waveform = await api.ecgWaveform(id);
    } catch (e) {
      waveError = e instanceof Error ? e.message : String(e);
    } finally {
      waveLoading = false;
    }
  }

  onMount(load);

  // 展开块进入视野：波形是异步加载的，撑高发生在图表画完之后，
  // 所以参数带上 !waveLoading，加载结束再滚一次，免得下半截留在屏外。
  function reveal(node: HTMLTableRowElement, show: boolean) {
    let last = false;
    function run(v: boolean) {
      if (v && !last) node.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
      last = v;
    }
    run(show);
    return { update: run };
  }

  // 时刻按「这条记录自带的偏移」还原，不跟浏览器时区走——
  // 与睡眠页同一约定（sleep-night-metrics.ts 的 nightMinClock）。
  function fmtTime(date: string, clock: string): string {
    return `${date.slice(5).replace('-', '月')}日 ${clock}`;
  }

  // 波形面板的用户可理解摘要：时长/心跳数/平均心率/节律。
  // 刻意不出现采样率与点数——那是工程参数，不是健康信息。
  let waveSummary = $derived.by(() => {
    if (!selected || !waveform || waveform.voltages.length === 0) return null;
    return {
      secs: Math.max(1, Math.round(waveform.duration_sec)),
      beats: selected.beats,
      hr: selected.avg_hr,
      rhythm: RHYTHM_LABELS[selected.rhythm] ?? '—'
    };
  });
</script>

<div class="space-y-4">
  <PageHeader>
    心电图
    {#snippet actions()}
      <RangePicker value={range} onchange={(r) => ((range = r), load())} />
    {/snippet}
  </PageHeader>

  <ErrorBanner {error} />

  {#if loading}
    <div class="card px-4 py-10 text-center text-[13px] text-[var(--color-ink-500)]">
      正在分析心电数据…
    </div>
  {:else if analysis && analysis.summary.count > 0}
    <!-- 印象卡：整段测量的总体结论 -->
    <EcgVerdictCard
      dominantLabel={analysis.summary.dominant_label}
      dominantRatio={analysis.summary.dominant_ratio}
      count={analysis.summary.count}
      withMetrics={analysis.summary.with_metrics}
      abnormalCount={analysis.summary.abnormal_count}
      latestDate={analysis.summary.latest_date}
      latestClock={analysis.summary.latest_clock}
      latestLabel={records.at(-1)?.label ?? '—'}
      {dots}
      measuredDays={analysis.summary.measured_days}
    />

    <!-- 四个可解释指数 -->
    <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <EcgIndexCard
        metric="hrv"
        score={analysis.indices.hrv.score}
        latest={analysis.indices.hrv.latest}
        level={analysis.indices.hrv.level}
        basis={analysis.indices.hrv.basis}
      />
      <EcgIndexCard
        metric="risk"
        score={analysis.indices.risk.score}
        latest={analysis.indices.risk.score}
        level={analysis.indices.risk.level}
        basis={analysis.indices.risk.basis}
        drivers={analysis.indices.risk.drivers}
      />
      <EcgIndexCard
        metric="stress"
        score={analysis.indices.stress.score}
        latest={analysis.indices.stress.latest}
        level={analysis.indices.stress.level}
        basis={analysis.indices.stress.basis}
      />
      <EcgIndexCard
        metric="fatigue"
        score={analysis.indices.fatigue.score}
        latest={analysis.indices.fatigue.latest}
        level={analysis.indices.fatigue.level}
        basis={analysis.indices.fatigue.basis}
      />
    </div>

    <!-- 趋势：指标切换 + 点击下钻 -->
    <Card>
      <EcgTrendChart {records} {selectedId} onselect={select} />
    </Card>

    <!-- 分布 + 对比 -->
    <div class="grid gap-3 lg:grid-cols-2">
      <Card>
        <EcgDistributionChart
          items={analysis.distribution}
          total={analysis.summary.count}
          activeCode={filterCode}
          onfilter={(c) => (filterCode = c)}
        />
      </Card>
      <Card>
        <EcgComparisonCard comparison={analysis.comparison} />
      </Card>
    </div>

    <!-- 记录列表：紧凑、无工程参数 -->
    <Card pad="none" class="overflow-hidden">
      <div class="flex flex-wrap items-baseline justify-between gap-2 px-4 py-2.5">
        <div class="text-[13px] font-medium">测量记录</div>
        <div class="flex items-center gap-2 text-[12px] text-[var(--color-ink-500)]">
          {#if filterCode}
            {@const item = analysis.distribution.find((d) => d.code === filterCode)}
            <button
              class="rounded bg-[var(--color-brand-soft)] px-1.5 py-0.5 text-[11px] text-[var(--color-brand)]"
              onclick={() => (filterCode = null)}
            >
              只看「{item?.label ?? filterCode}」 × {item?.count ?? 0} · 点击取消
            </button>
          {/if}
          <span class="text-[11px]">点一行展开波形 · 再点收起</span>
          <span class="num">{shown.length} 条</span>
        </div>
      </div>
      <DataTable
        rows={shown}
        rowKey={(r) => String(r.id)}
        selectedKey={selectedId === null ? null : String(selectedId)}
        emphasis
        onrowclick={(r) => select(r.id)}
        columns={[
          { label: '测量时刻' },
          { label: '判定' },
          { label: '心率', align: 'right' },
          { label: '节律', align: 'right' },
          { label: '信号', align: 'right' }
        ]}
      >
        {#snippet children(r)}
          <td class="num px-4 py-2">{fmtTime(r.date, r.clock)}</td>
          <td class="px-4 py-2">
            <!-- 归一没认出来的判定照原样展示，并标注它的来路 -->
            <span class={r.known ? '' : 'text-[var(--color-ink-500)]'}>{r.label}</span>
            {#if !r.known}
              <span class="text-[11px] text-[var(--color-ink-500)]"> · 上游原文</span>
            {/if}
          </td>
          <td class="num px-4 py-2 text-right">
            {r.avg_hr === null ? '—' : `${Math.round(r.avg_hr)} 次/分`}
          </td>
          <td class="num px-4 py-2 text-right">
            {RHYTHM_LABELS[r.rhythm] ?? '—'}
          </td>
          <td class="num px-4 py-2 text-right">
            {QUALITY_LABELS[r.quality] ?? '—'}
          </td>
        {/snippet}
        {#snippet expand(r)}
          {#if selectedId === r.id}
            <tr
              class="border-t border-[var(--color-line)] bg-[var(--color-brand-soft)]/25"
              use:reveal={r.id === revealId && !waveLoading}
            >
              <td colspan="5" class="px-4 pt-2.5 pb-3">
                <div class="flex flex-wrap items-baseline justify-between gap-2">
                  <div class="text-[12px] font-medium">心电图波形</div>
                  {#if waveSummary}
                    <div class="num flex flex-wrap gap-x-4 text-[12px] text-[var(--color-ink-500)]">
                      <span>约 {waveSummary.secs} 秒</span>
                      {#if waveSummary.beats !== null}
                        <span>{waveSummary.beats} 次心跳</span>
                      {/if}
                      {#if waveSummary.hr !== null}
                        <span>平均 {Math.round(waveSummary.hr)} 次/分</span>
                      {/if}
                      <span>节律{waveSummary.rhythm}</span>
                    </div>
                  {/if}
                </div>
                {#if waveLoading}
                  <div class="py-6 text-center text-[13px] text-[var(--color-ink-500)]">
                    波形加载中…
                  </div>
                {:else if waveError}
                  <ErrorBanner error={waveError} bare class="py-2" />
                {:else if waveform && waveform.voltages.length > 0}
                  <EcgStripChart
                    voltages={waveform.voltages}
                    unit=""
                    samplingFrequency={waveform.sampling_frequency ?? null}
                    peaks={waveform.peaks ?? []}
                    height={240}
                  />
                  <div class="mt-1 text-[11px] text-[var(--color-ink-500)]">
                    黄色竖线标出服务端识别出的每次心跳；波形仅供浏览，判读以设备给出的结论为准
                  </div>
                {:else}
                  <div class="py-6 text-center text-[13px] text-[var(--color-ink-500)]">
                    这条记录没有波形数据（导出没给波形，或波形有点缺电压、整段没入库）
                  </div>
                {/if}
                {#if r.quality === 'poor'}
                  <div
                    class="mt-2 rounded bg-[var(--color-warn)]/10 px-3 py-2 text-[12px] text-[var(--color-warn)]"
                  >
                    这条记录的信号质量不佳，它的心率变异性等指数未计入上方统计
                  </div>
                {/if}
              </td>
            </tr>
          {/if}
        {/snippet}
        {#snippet empty()}
          <EmptyState variant="row" colspan={5} title="该筛选下没有记录" />
        {/snippet}
      </DataTable>
    </Card>

    <!-- 说明 -->
    <InfoNote dim={false} class="px-1">
      说明：所有统计与指数均由本机根据波形数据实时计算，不上传、不联网；指数是健康参考而非医学诊断，
      异常提示请以医疗机构意见为准。心电数据来自 iPhone/Apple Watch 的心电测量（单次约 30 秒），
      同步方式见「同步」页。
    </InfoNote>
  {:else}
    <div class="card px-4 py-10 text-center">
      <div class="text-[13px] text-[var(--color-ink-500)]">所选时间段内暂无心电记录</div>
      <div class="mt-1 text-[12px] text-[var(--color-ink-500)]">
        用 iPhone/Apple Watch 测几次心电并同步后，这里会显示趋势、分布与各项指数
      </div>
    </div>
  {/if}
</div>
