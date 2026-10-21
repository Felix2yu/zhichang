<script lang="ts">
  import { onMount } from 'svelte';
  import { flip } from 'svelte/animate';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { api, shiftDays, todayStr, type DailyPoint, type MetricDef } from '#lib/api';
  import { isDate, lastDays, matchPreset, spanDays, type Range } from '#lib/range';
  import { METRIC_GROUPS, groupOf, unitDigits, type MetricGroup } from '#lib/metric-groups';
  import { prefs, TREND_SELECTED_KEY } from '#lib/prefs.svelte';
  import {
    NO_ACTIVITY,
    activityFromCoverage,
    isVeryStale,
    meanScore,
    rankByActivity,
    staleLabel,
    type ActivityStats
  } from '#lib/metric-activity';
  import {
    FREQ_WINDOW_DAYS,
    agoText,
    buildCoverage,
    type MetricCoverage
  } from '#lib/metric-frequency';
  import { cadenceText, tierOfPoints } from '#lib/metric-tiers';
  import type { EmptyKind } from '#lib/metric-render';
  import RangePicker from '#lib/components/RangePicker.svelte';
  import Card from '#lib/components/ui/Card.svelte';
  import Chip from '#lib/components/ui/Chip.svelte';
  import EmptyState from '#lib/components/ui/EmptyState.svelte';
  import ErrorBanner from '#lib/components/ui/ErrorBanner.svelte';
  import LoadingState from '#lib/components/ui/LoadingState.svelte';
  import PageHeader from '#lib/components/ui/PageHeader.svelte';
  import MetricTrendCard from '#lib/components/MetricTrendCard.svelte';
  import MetricPicker from '#lib/components/MetricPicker.svelte';
  import TrendListView from '#lib/components/TrendListView.svelte';
  import Segmented from '#lib/components/Segmented.svelte';

  // 趋势页的重心是两件事：
  //   1) 51 个指标怎么选——主轴换成「数据频率」，并给搜索、批量、持久化与 URL 同步；
  //   2) 画出来是什么样——由区间内实际点数分档，密集的画趋势、稀疏的画间隔。
  // 详见 docs/trends-redesign.md。

  type View = 'grid' | 'list' | 'focus';
  /** 网格视图的 ECharts 上限：每张卡一个实例，超过这个数就该切列表 */
  const GRID_MAX = 12;
  /** 稀疏指标回溯取数的窗口（天）与保留次数 */
  const BACKFILL_DAYS = 365;
  const BACKFILL_KEEP = 6;
  /** 密度条的窗口：恒为近一年，与图表的区间无关 */
  const STRIP_WINDOW_DAYS = 365;

  let defs = $state<MetricDef[]>([]);
  let cov = $state<Map<string, MetricCoverage>>(new Map());
  let selected = $state<string[]>([]);
  let range = $state<Range>(lastDays(prefs.rangeDays));
  let view = $state<View>('grid');
  let focusMetric = $state<string | null>(null);
  let series = $state<Record<string, DailyPoint[]>>({});
  let baselines = $state<Record<string, number | null>>({});
  let backfill = $state<Record<string, { count: number; days: number }>>({});
  /** 密度条数据：近一年的测量日期。与区间无关，所以不随区间作废 */
  let strips = $state<Record<string, string[]>>({});
  /** 被用户手动关掉回溯的指标 */
  let backfillOff = $state<Record<string, true>>({});
  /** 用户手动点过「显示最近 6 次」的指标：即使是同步延迟也要照取 */
  let forceBackfill: Record<string, true> = {};
  let loading = $state(true);
  let error = $state('');

  let byName = $derived(new Map(defs.map((d) => [d.metric, d])));
  let span = $derived(spanDays(range));

  /**
   * 是不是「今天还没同步」：区间内一个点都没有，但一两天前还有数据。
   *
   * 这种情况不该按稀疏处理去回溯——它不是「测得少」，是同步还没跑完，
   * 把前几次记录翻出来反而掩盖了「今天的数据还没到」这个事实。
   */
  function isPendingSync(metric: string): boolean {
    const c = cov.get(metric);
    if (!c || c.daysSince === null) return false;
    return c.daysSince <= 2 && (series[metric] ?? []).length === 0;
  }

  function aggLabel(a: string | undefined): string {
    switch (a) {
      case 'sum':
        return '求和';
      case 'mean':
        return '均值';
      case 'max':
        return '最大';
      case 'min':
        return '最小';
      case 'last':
        return '最近一次';
      case 'count':
        return '计数';
      default:
        return a ?? '—';
    }
  }
  function crossLabel(a: string | undefined): string {
    switch (a) {
      case 'max':
        return '跨来源取最大';
      case 'mean':
        return '跨来源加权均值';
      case 'sum':
        return '跨来源求和';
      case 'latest':
        return '跨来源最近';
      default:
        return a ?? '—';
    }
  }

  // —— 卡片视图模型：一个指标一张卡 ——
  type Card = ReturnType<typeof cardOf>;
  function cardOf(metric: string) {
    const d = byName.get(metric);
    const group = groupOf(metric);
    const unit = d?.preferred_unit || '';
    const pts = (series[metric] ?? []).map((p) => ({ date: p.date, value: p.value }));
    // 活跃度改由 coverage 算：不再为了排序拉一次全 51 指标 × 90 天的全量序列
    const c = cov.get(metric) ?? null;
    const activity: ActivityStats = c
      ? activityFromCoverage(c.daysSince, c.days, FREQ_WINDOW_DAYS)
      : NO_ACTIVITY;
    // 空态三分支：从未接入 / 今天还没同步 / 区间内没有但历史有。
    // 三者的下一步完全不同，混成一句「无记录」用户就不知道该做什么
    const emptyKind: EmptyKind =
      !c || c.freq === 'never' ? 'never' : isPendingSync(metric) ? 'pending' : 'stale';
    const lastText = c?.lastDate ? `${c.lastDate} · ${agoText(c.daysSince) ?? ''}` : '';
    return {
      metric,
      name: d?.display_name_zh ?? metric,
      unit,
      kind: d?.kind ?? ('instant' as MetricDef['kind']),
      dayAgg: d?.day_agg ?? ('mean' as MetricDef['day_agg']),
      digits: unitDigits(unit),
      color: group.tint,
      aggNote: `日内${aggLabel(d?.day_agg)} · ${crossLabel(d?.cross_source_agg)}`,
      points: pts,
      tier: tierOfPoints(
        pts.length,
        pts.map((p) => p.date)
      ),
      cov: c,
      activity,
      cadence: cadenceText(pts.map((p) => p.date)),
      baseline: baselines[metric] ?? null,
      staleText: staleLabel(activity),
      staleStrong: isVeryStale(activity),
      backfill: backfill[metric] ?? null,
      backfillOff: backfillOff[metric] ?? false,
      emptyKind,
      lastText,
      strip: strips[metric] ?? [],
      order: d?.sort_order ?? 999
    };
  }

  let cards = $derived(selected.map((m) => cardOf(m)));
  let ranked = $derived(rankByActivity(cards));

  // 分区视图：分组 => 若干卡片。分组只决定版面归属，不参与图形合成。
  // 活跃度高的分区在前：整组都陈旧的会被推到后面。
  let sections = $derived(
    METRIC_GROUPS.map((group, gi) => {
      const cs = ranked.filter((c) => group.metrics.includes(c.metric));
      if (cs.length === 0) return null;
      return { group, gi, cards: cs, score: meanScore(cs) };
    })
      .filter(
        (v): v is { group: MetricGroup; gi: number; cards: Card[]; score: number } => v !== null
      )
      .sort((a, b) => b.score - a.score || a.gi - b.gi)
  );

  // 网格有实例上限：超出部分不画，提示切列表（列表每行只用 SVG）
  let gridTop = $derived(ranked.slice(0, GRID_MAX).map((c) => c.metric));
  let gridSections = $derived(
    sections
      .map((s) => ({ ...s, cards: s.cards.filter((c) => gridTop.includes(c.metric)) }))
      .filter((s) => s.cards.length > 0)
  );
  let focusCard = $derived(
    focusMetric ? (cards.find((c) => c.metric === focusMetric) ?? null) : null
  );

  // —— URL 是这一屏的真相：链接可直接分享，刷新可还原 ——
  let ready = $state(false);
  let urlSelected: string[] | null = null;
  /** 选择是否由用户/链接显式指定：仅这种情况才把整串指标写进 URL。
   *  否则每次打开页面都会把自动挑选的默认项塞进地址栏，链接又长又没有信息量。 */
  let pinned = $state(false);
  /** 天数预设：命中时 URL 写 days=N，比一对日期短，也和区间控件同一套口径 */
  const URL_PRESETS = [7, 30, 90, 365];

  function readUrl() {
    const q = page.url.searchParams;
    const f = q.get('from') ?? '';
    const t = q.get('to') ?? '';
    if (isDate(f) && isDate(t) && f <= t) range = { from: f, to: t };
    else {
      const d = Number(q.get('days'));
      if (d === 7 || d === 30 || d === 90 || d === 365) range = lastDays(d);
    }
    const m = q.get('m');
    if (m) {
      urlSelected = m.split(',').filter(Boolean);
      pinned = true;
    }
    const v = q.get('v');
    if (v === 'grid' || v === 'list' || v === 'focus') view = v;
    const fm = q.get('f');
    if (fm) focusMetric = fm;
  }

  function readStored(): string[] | null {
    try {
      const raw = localStorage.getItem(TREND_SELECTED_KEY);
      const arr = raw ? (JSON.parse(raw) as unknown) : null;
      if (Array.isArray(arr)) return arr.filter((x): x is string => typeof x === 'string');
    } catch {
      /* 读不到按默认 */
    }
    return null;
  }

  $effect(() => {
    if (!ready) return;
    // SvelteKit 3 的 shallow 导航只更新 page.shallow.url，page.url 仍停在整页加载时的地址；
    // 拿 page.url 做「当前地址」会让下一次比较永远对着旧值，返回网格时清不掉 v/f。
    const current = page.shallow?.url ?? page.url;
    const u = new URL(current.href);
    if (pinned) {
      if (selected.length > 0) u.searchParams.set('m', selected.join(','));
      else u.searchParams.delete('m');
    } else {
      u.searchParams.delete('m');
    }
    const preset = matchPreset(range, URL_PRESETS);
    const fallback = lastDays(prefs.rangeDays);
    if (range.from === fallback.from && range.to === fallback.to) {
      // 默认区间不写进 URL：地址栏只承载「与平时不一样」的状态
      u.searchParams.delete('from');
      u.searchParams.delete('to');
      u.searchParams.delete('days');
    } else if (preset !== undefined) {
      u.searchParams.set('days', String(preset));
      u.searchParams.delete('from');
      u.searchParams.delete('to');
    } else {
      u.searchParams.set('from', range.from);
      u.searchParams.set('to', range.to);
      u.searchParams.delete('days');
    }
    if (view === 'focus' && focusMetric) {
      u.searchParams.set('v', 'focus');
      u.searchParams.set('f', focusMetric);
    } else if (view === 'list') {
      // 网格是默认视图，不写 v；返回网格时地址栏自然缩短
      u.searchParams.set('v', 'list');
      u.searchParams.delete('f');
    } else {
      u.searchParams.delete('v');
      u.searchParams.delete('f');
    }
    // 先看再写：goto 会改当前地址，不比较就会「写 URL → 依赖变化 → 再写一次」地空转
    const next = `${u.pathname}${u.search}`;
    const cur = `${current.pathname}${current.search}`;
    if (next === cur) return;
    // eslint-disable-next-line svelte/no-navigation-without-resolve
    void goto(next, { replace: true, shallow: true });
  });

  $effect(() => {
    if (!ready) return;
    try {
      localStorage.setItem(TREND_SELECTED_KEY, JSON.stringify(selected));
    } catch {
      /* 隐私模式存不下，不影响本次浏览 */
    }
  });

  // —— 取数 ——
  // 已取过数的指标（按当前区间）。用普通对象记账即可：它不参与渲染，
  // 换成 Set 反而会触发「响应式上下文里用可变集合」的告警。
  let loadedKey = '';
  let loaded: Record<string, true> = {};
  // 回溯与常态线同样要记账：否则每点一次指标就会把已算过的那些重算一遍，
  // 选中 20 项时连点几下就发出去几十次请求
  let loadedBackfill: Record<string, true> = {};
  let loadedBaseline: Record<string, true> = {};
  // 密度条与区间无关，只按指标记一次即可
  let loadedStrips: Record<string, true> = {};

  function rangeKey(): string {
    return `${range.from}~${range.to}`;
  }

  async function loadSeries() {
    const k = rangeKey();
    if (k !== loadedKey) {
      // 区间变了：旧序列全部作废，否则会画出别的区间的点
      loaded = {};
      loadedBackfill = {};
      loadedBaseline = {};
      loadedKey = k;
      series = {};
      backfill = {};
      baselines = {};
    }
    const need = selected.filter((m) => !loaded[m]);
    if (need.length === 0) return;
    try {
      const res = await api.daily(need, range.from, range.to);
      series = { ...series, ...res.series };
      for (const m of need) loaded[m] = true;
      error = '';
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  /**
   * 稀疏指标回溯：区间内不足 3 个点时，图形没有意义。
   * 默认把它扩到最近 6 次记录（上限 365 天），并在卡片上标注「已回溯」。
   * 所有需要回溯的指标合并成一次请求，不逐指标发。
   */
  async function loadBackfill() {
    const need = selected.filter(
      (m) =>
        (series[m] ?? []).length < 3 &&
        cov.get(m)?.freq !== 'never' &&
        !backfillOff[m] &&
        !loadedBackfill[m] &&
        // 同步延迟不算稀疏，不自动回溯；用户显式要求时才取
        (!isPendingSync(m) || forceBackfill[m])
    );
    if (need.length === 0) return;
    const to = range.to;
    const from = shiftDays(-(BACKFILL_DAYS - 1), to);
    try {
      const res = await api.daily(need, from, to);
      const nextSeries = { ...series };
      const nextInfo: Record<string, { count: number; days: number }> = {};
      for (const m of need) {
        const pts = (res.series[m] ?? []).map((p) => ({ date: p.date, value: p.value }));
        // 一条记录也要留下：只测过一次的指标（如肺活量）拿到这唯一的值后，
        // 卡片会走「单次记录」档把数值和日期显示出来，而不是干巴巴的「无记录」
        loadedBackfill[m] = true;
        if (pts.length === 0) continue;
        const keep = pts.slice(-BACKFILL_KEEP);
        // 只在确实比区间内拿到的多时才替换，否则会平白把区间口径改掉
        if (keep.length <= (series[m] ?? []).length) continue;
        nextSeries[m] = keep.map((p) => ({
          date: p.date,
          metric: m,
          value: p.value,
          sample_count: 0,
          source_count: 0,
          sources: []
        }));
        nextInfo[m] = { count: keep.length, days: spanDays({ from: keep[0].date, to }) };
      }
      series = nextSeries;
      backfill = { ...backfill, ...nextInfo };
    } catch {
      /* 回溯是增强，失败就按区间内的原样显示 */
    }
  }

  /** 常态线只给连续档；3 个点算不出常态。 */
  async function loadBaseline() {
    const dense = selected.filter(
      (m) =>
        !loadedBaseline[m] &&
        tierOfPoints(
          (series[m] ?? []).length,
          (series[m] ?? []).map((p) => p.date)
        ) === 'continuous'
    );
    if (dense.length === 0) return;
    try {
      const res = await api.baseline(dense, range.to, 30);
      const next: Record<string, number | null> = { ...baselines };
      for (const it of res.items) next[it.metric] = it.baseline;
      for (const m of dense) loadedBaseline[m] = true;
      baselines = next;
    } catch {
      /* 常态线是增强，失败不阻断 */
    }
  }

  /**
   * 密度条数据：近一年的测量日期，只取非日更的指标。
   *
   * 日更指标的「密度条」是 365 根刻度挤成一条实线，没有信息量；
   * 稀疏指标才是这条要服务的对象。窗口与区间无关，所以只按指标取一次。
   */
  async function loadStrips() {
    const need = selected.filter(
      (m) => !loadedStrips[m] && cov.get(m)?.freq !== 'daily' && cov.get(m)?.freq !== undefined
    );
    if (need.length === 0) return;
    const to = todayStr();
    const from = shiftDays(-(STRIP_WINDOW_DAYS - 1), to);
    try {
      const res = await api.daily(need, from, to);
      const next = { ...strips };
      for (const m of need) {
        loadedStrips[m] = true;
        next[m] = (res.series[m] ?? []).map((p) => p.date).sort();
      }
      strips = next;
    } catch {
      /* 密度条是增强，失败就不画 */
    }
  }

  let timer: ReturnType<typeof setTimeout> | null = null;
  /** 连点多个指标只发一次请求：每次点击都全量重拉会把接口打满。 */
  function scheduleLoad() {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = null;
      void runLoad();
    }, 200);
  }

  async function runLoad() {
    loading = true;
    try {
      await loadSeries();
      await Promise.all([loadBackfill(), loadBaseline(), loadStrips()]);
    } finally {
      loading = false;
    }
  }

  function onSelectChange(next: string[]) {
    pinned = true;
    selected = next;
    scheduleLoad();
  }
  function onRangeChange(r: Range) {
    range = r;
    scheduleLoad();
  }

  /**
   * 开/关某个指标的回溯。
   *
   * 关掉时必须把回填进来的序列一并清掉再按区间重取——否则卡片还画着区间外的点，
   * 而徽标已经消失了，用户会以为看到的就是区间内的数据。
   */
  function setBackfill(metric: string, on: boolean) {
    delete loadedBackfill[metric];
    if (on) {
      const next = { ...backfillOff };
      delete next[metric];
      backfillOff = next;
      forceBackfill[metric] = true;
    } else {
      backfillOff = { ...backfillOff, [metric]: true };
      delete forceBackfill[metric];
      const nextSeries = { ...series };
      delete nextSeries[metric];
      series = nextSeries;
      delete loaded[metric];
      const nb = { ...backfill };
      delete nb[metric];
      backfill = nb;
    }
    scheduleLoad();
  }
  function openFocus(metric: string) {
    focusMetric = metric;
    view = 'focus';
  }
  function backFromFocus() {
    view = 'grid';
    focusMetric = null;
  }

  /** 默认选择：不再硬编码两个指标，改成「日更里最近更新的前 6 项」——有数据才算数。 */
  function autoDefault(): string[] {
    const age = (d: MetricDef) => cov.get(d.metric)?.daysSince ?? Number.MAX_SAFE_INTEGER;
    const withData = defs.filter((d) => (cov.get(d.metric)?.freq ?? 'never') !== 'never');
    if (withData.length === 0) return ['step_count', 'heart_rate'];
    const daily = withData
      .filter((d) => cov.get(d.metric)?.freq === 'daily')
      .sort((a, b) => age(a) - age(b));
    const rest = withData
      .filter((d) => cov.get(d.metric)?.freq !== 'daily')
      .sort((a, b) => age(a) - age(b));
    return [...daily, ...rest].slice(0, 6).map((d) => d.metric);
  }

  onMount(async () => {
    readUrl();
    try {
      defs = (await api.metrics()).metrics.filter((d) => d.enabled);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      ready = true;
      return;
    }
    try {
      // 一年的窗口：90 天会把「上次测量在半年前」的指标误判成从未接入
      const res = await api.coverage(FREQ_WINDOW_DAYS);
      cov = buildCoverage(defs, res.rows, FREQ_WINDOW_DAYS);
    } catch {
      // 覆盖度拿不到就退化成「全部未知」，选择器仍可用（全部落在未接入）
      cov = buildCoverage(defs, [], FREQ_WINDOW_DAYS);
    }
    const known = new Set(defs.map((d) => d.metric));
    const pick = (urlSelected ?? readStored() ?? autoDefault()).filter((m) => known.has(m));
    selected = pick.length > 0 ? pick : autoDefault();
    if (view === 'focus' && (!focusMetric || !selected.includes(focusMetric))) view = 'grid';
    ready = true;
    await runLoad();
  });
</script>

<div class="space-y-4">
  <!-- 工具条：标题 + 区间 + 视图 -->
  <PageHeader>
    趋势
    {#snippet actions()}
      <div class="flex flex-wrap items-center gap-2">
        <Segmented
          value={view === 'focus' ? 'grid' : view}
          options={[
            { label: '网格', value: 'grid' as const },
            { label: '列表', value: 'list' as const }
          ]}
          onchange={(v) => {
            view = v;
            focusMetric = null;
          }}
        />
        <RangePicker value={range} onchange={onRangeChange} />
      </div>
    {/snippet}
  </PageHeader>

  <ErrorBanner {error} />

  <MetricPicker
    {defs}
    {cov}
    {selected}
    windowDays={FREQ_WINDOW_DAYS}
    onchange={onSelectChange}
    onclear={() => onSelectChange([])}
  />

  {#if defs.length === 0}
    {#if !error}
      <Card pad="none">
        <LoadingState text="加载中…" height={160} />
      </Card>
    {/if}
  {:else if selected.length === 0}
    <Card pad="none">
      <EmptyState height={160} title="尚未选择指标">
        {#snippet desc()}在上方按更新频率挑选，每个指标一张独立的图{/snippet}
      </EmptyState>
    </Card>
  {:else if view === 'focus' && focusCard}
    <!-- 聚焦：一个指标一张大图，其余选中项作为切换条 -->
    <div class="space-y-3">
      <button
        class="text-[12px] text-[var(--color-ink-500)] transition-colors hover:text-[var(--color-brand)]"
        onclick={backFromFocus}
      >
        ← 返回
      </button>
      <div class="mx-auto max-w-3xl">
        <MetricTrendCard
          metric={focusCard.metric}
          name={focusCard.name}
          unit={focusCard.unit}
          points={focusCard.points}
          kind={focusCard.kind}
          dayAgg={focusCard.dayAgg}
          color={focusCard.color}
          digits={focusCard.digits}
          aggNote={focusCard.aggNote}
          baseline={focusCard.baseline}
          staleText={focusCard.staleText}
          staleStrong={focusCard.staleStrong}
          backfill={focusCard.backfill}
          strip={focusCard.strip}
          emptyKind={focusCard.emptyKind}
          lastText={focusCard.lastText}
          days={span}
          tall
          {loading}
          onbackfill={() => setBackfill(focusCard.metric, true)}
          onbackfilloff={() => setBackfill(focusCard.metric, false)}
        />
      </div>
      <div class="flex flex-wrap gap-1.5">
        {#each ranked as c (c.metric)}
          <Chip selected={c.metric === focusMetric} onclick={() => (focusMetric = c.metric)}>
            {c.name}
          </Chip>
        {/each}
      </div>
    </div>
  {:else if view === 'list'}
    <TrendListView rows={cards} {range} {loading} onselect={openFocus} />
  {:else}
    <div class="space-y-6">
      {#if selected.length > GRID_MAX}
        <div
          class="card flex flex-wrap items-center justify-between gap-2 px-4 py-2.5 text-[12px] text-[var(--color-ink-500)]"
        >
          <span>
            网格每张卡一个图表实例，这里只画活跃度最高的 {GRID_MAX} 项（共 {selected.length} 项）
          </span>
          <button class="text-[var(--color-brand)]" onclick={() => (view = 'list')}
            >切到列表看全部</button
          >
        </div>
      {/if}
      {#each gridSections as section (section.group.id)}
        <section animate:flip={{ duration: 420 }}>
          <div class="flex items-baseline gap-2">
            <span class="h-3.5 w-1 self-center rounded-full" style="background:{section.group.tint}"
            ></span>
            <h2 class="text-[13px] font-medium">{section.group.label}</h2>
            <span class="min-w-0 flex-1 truncate text-[11px] text-[var(--color-ink-500)]"
              >{section.group.desc}</span
            >
            <span class="shrink-0 text-[11px] text-[var(--color-ink-500)]"
              >{section.cards.length} 项</span
            >
          </div>
          <div class="mt-2.5 grid grid-cols-1 gap-3 md:grid-cols-2 2xl:grid-cols-3">
            {#each section.cards as c (c.metric)}
              <div class="min-w-0" animate:flip={{ duration: 420 }}>
                <MetricTrendCard
                  metric={c.metric}
                  name={c.name}
                  unit={c.unit}
                  points={c.points}
                  kind={c.kind}
                  dayAgg={c.dayAgg}
                  color={c.color}
                  digits={c.digits}
                  aggNote={c.aggNote}
                  baseline={c.baseline}
                  staleText={c.staleText}
                  staleStrong={c.staleStrong}
                  backfill={c.backfill}
                  strip={c.strip}
                  emptyKind={c.emptyKind}
                  lastText={c.lastText}
                  days={span}
                  class="h-full"
                  {loading}
                  onselect={() => openFocus(c.metric)}
                  onbackfill={() => setBackfill(c.metric, true)}
                  onbackfilloff={() => setBackfill(c.metric, false)}
                />
              </div>
            {/each}
          </div>
        </section>
      {/each}
    </div>
  {/if}
</div>
