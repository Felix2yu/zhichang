<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type EcgAnalysis, type EcgWaveform } from '#lib/api';
  import { lastDays, type Range } from '#lib/range';
  import RangePicker from '#lib/components/RangePicker.svelte';
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

  // 选中态由趋势图与记录列表共享：点哪边都是「看这一次的波形」。
  let selectedId = $state<number | null>(null);
  let waveform = $state<EcgWaveform | null>(null);
  let waveLoading = $state(false);
  let waveError = $state('');

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
      await select(latest?.id ?? null);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      analysis = null;
    } finally {
      loading = false;
    }
  }

  async function select(id: number | null) {
    selectedId = id;
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
  <div class="flex flex-wrap items-baseline justify-between gap-2">
    <h1 class="text-[15px] font-medium">心电图</h1>
    <RangePicker value={range} onchange={(r) => ((range = r), load())} />
  </div>

  {#if error}
    <div class="card px-4 py-3 text-[13px] text-[var(--color-danger)]">{error}</div>
  {/if}

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
    <div class="card p-4">
      <EcgTrendChart {records} {selectedId} onselect={select} />
    </div>

    <!-- 分布 + 对比 -->
    <div class="grid gap-3 lg:grid-cols-2">
      <div class="card p-4">
        <EcgDistributionChart
          items={analysis.distribution}
          total={analysis.summary.count}
          activeCode={filterCode}
          onfilter={(c) => (filterCode = c)}
        />
      </div>
      <div class="card p-4">
        <EcgComparisonCard comparison={analysis.comparison} />
      </div>
    </div>

    <!-- 记录列表：紧凑、无工程参数 -->
    <div class="card overflow-hidden">
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
          <span class="num">{shown.length} 条</span>
        </div>
      </div>
      <table class="w-full text-[13px]">
        <thead class="bg-[var(--color-surface-2)] text-[12px] text-[var(--color-ink-500)]">
          <tr>
            <th class="px-4 py-2 text-left font-normal">测量时刻</th>
            <th class="px-4 py-2 text-left font-normal">判定</th>
            <th class="px-4 py-2 text-right font-normal">心率</th>
            <th class="px-4 py-2 text-right font-normal">节律</th>
            <th class="px-4 py-2 text-right font-normal">信号</th>
          </tr>
        </thead>
        <tbody>
          {#each shown as r (r.id)}
            <tr
              class="cursor-pointer border-t border-[var(--color-line)] transition-colors {selectedId ===
              r.id
                ? 'bg-[var(--color-brand-soft)]/60'
                : 'hover:bg-[var(--color-surface-2)]'}"
              onclick={() => select(r.id)}
            >
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
            </tr>
          {:else}
            <tr>
              <td colspan="5" class="px-4 py-6 text-center text-[var(--color-ink-500)]">
                该筛选下没有记录
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>

    <!-- 底部波形面板：与趋势图、记录列表联动 -->
    {#if selected}
      <div class="card p-4">
        <div class="mb-1 flex flex-wrap items-baseline justify-between gap-2">
          <div class="text-[13px] font-medium">
            心电图波形
            <span class="num text-[12px] font-normal text-[var(--color-ink-500)]">
              {fmtTime(selected.date, selected.clock)} · {selected.label}
            </span>
          </div>
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
          <div class="py-8 text-center text-[13px] text-[var(--color-ink-500)]">波形加载中…</div>
        {:else if waveError}
          <div class="py-3 text-[13px] text-[var(--color-danger)]">{waveError}</div>
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
          <div class="py-8 text-center text-[13px] text-[var(--color-ink-500)]">
            这条记录没有波形数据（导出没给波形，或波形有点缺电压、整段没入库）
          </div>
        {/if}
        {#if selected.quality === 'poor'}
          <div
            class="mt-2 rounded bg-[var(--color-warn)]/10 px-3 py-2 text-[12px] text-[var(--color-warn)]"
          >
            这条记录的信号质量不佳，它的心率变异性等指数未计入上方统计
          </div>
        {/if}
      </div>
    {/if}

    <p class="px-1 text-[11px] leading-relaxed text-[var(--color-ink-500)]">
      说明：所有统计与指数均由本机根据波形数据实时计算，不上传、不联网；指数是健康参考而非医学诊断，
      异常提示请以医疗机构意见为准。心电数据来自 iPhone/Apple Watch 的心电测量（单次约 30 秒），
      同步方式见「同步」页。
    </p>
  {:else}
    <div class="card px-4 py-10 text-center">
      <div class="text-[13px] text-[var(--color-ink-500)]">所选时间段内暂无心电记录</div>
      <div class="mt-1 text-[12px] text-[var(--color-ink-500)]">
        用 iPhone/Apple Watch 测几次心电并同步后，这里会显示趋势、分布与各项指数
      </div>
    </div>
  {/if}
</div>
