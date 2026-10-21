<script lang="ts">
  import { resolve } from '$app/paths';
  import type { MetricDef } from '#lib/api';
  import { METRIC_GROUPS } from '#lib/metric-groups';
  import {
    FREQ_HINT,
    FREQ_LABEL,
    FREQ_WINDOW_DAYS,
    agoText,
    freqBounds,
    type FreqTier,
    type MetricCoverage
  } from '#lib/metric-frequency';
  import { matchMetric } from '#lib/metric-search';
  import Chip from '#lib/components/ui/Chip.svelte';

  // 指标选择器：以「数据频率」为主轴，分类与搜索为交叉筛选。
  //
  // 与旧版（12 个分类平铺 51 个 chip）的差别：
  //   1. 默认折叠，首屏把空间还给图表；
  //   2. 频率分区带计数，未接入的一类默认收起——消除最大一块无效点击；
  //   3. chip 上直接写明「今天 / 3 天前 / 未接入」，不用悬停才知道有没有数据；
  //   4. 有搜索、有「全选本组」，不再只能一个个点。

  type Props = {
    defs: MetricDef[];
    cov: Map<string, MetricCoverage>;
    selected: string[];
    onchange: (next: string[]) => void;
    onclear: () => void;
    /** 评估窗口天数，用于说明频率分区的口径 */
    windowDays?: number;
  };

  let { defs, cov, selected, onchange, onclear, windowDays = FREQ_WINDOW_DAYS }: Props = $props();

  let bounds = $derived(freqBounds(windowDays));

  let open = $state(false);
  let freq = $state<FreqTier | 'all'>('all');
  let q = $state('');
  let group = $state<string | null>(null);
  let showNever = $state(false);

  // 指标 → 分类 id。用普通对象而不是 Map：这里只是查表，不需要 Map 的语义，
  // 也躲开「响应式上下文里用可变 Map」的告警。
  let groupIdOf = $derived.by(() => {
    const m: Record<string, string> = {};
    for (const g of METRIC_GROUPS) for (const mm of g.metrics) m[mm] = g.id;
    return m;
  });

  let counts = $derived.by(() => {
    const c: Record<string, number> = {
      all: defs.length,
      daily: 0,
      regular: 0,
      occasional: 0,
      never: 0
    };
    for (const d of defs) c[cov.get(d.metric)?.freq ?? 'never'] += 1;
    return c;
  });

  // 频率 × 分类 × 搜索，三者取交集
  let matched = $derived(
    defs.filter((d) => {
      if (group && groupIdOf[d.metric] !== group) return false;
      return matchMetric(d, q);
    })
  );

  let sections = $derived(
    (['daily', 'regular', 'occasional', 'never'] as FreqTier[])
      .filter((f) => freq === 'all' || f === freq)
      .map((f) => ({
        freq: f,
        items: matched.filter((d) => (cov.get(d.metric)?.freq ?? 'never') === f)
      }))
      .filter((s) => s.items.length > 0)
  );

  function toggle(m: string) {
    onchange(selected.includes(m) ? selected.filter((x) => x !== m) : [...selected, m]);
  }
  function addAll(items: MetricDef[]) {
    const add = items.map((d) => d.metric).filter((m) => !selected.includes(m));
    if (add.length === 0) return;
    onchange([...selected, ...add]);
  }

  function dotClass(c: MetricCoverage | undefined): string {
    if (!c || c.freq === 'never') return 'border border-[var(--color-ink-400)] bg-transparent';
    if (c.daysSince !== null && c.daysSince > 14) return 'bg-[var(--color-warn)]';
    if (c.daysSince !== null && c.daysSince > 3) return 'bg-[var(--color-ink-400)]';
    return 'bg-[var(--color-brand)]';
  }
</script>

<div class="card p-3">
  <div class="flex items-center justify-between gap-2">
    <span class="text-[12px] font-medium text-[var(--color-ink-700)]">指标选择</span>
    <div class="flex items-center gap-2.5 text-[11px] text-[var(--color-ink-500)]">
      <span>已选 <b class="num font-medium">{selected.length}</b> 项</span>
      {#if selected.length > 0}
        <button class="transition-colors hover:text-[var(--color-brand)]" onclick={onclear}
          >清空</button
        >
      {/if}
      <button
        class="transition-colors hover:text-[var(--color-brand)]"
        onclick={() => (open = !open)}
      >
        {open ? '收起' : '展开'}
      </button>
    </div>
  </div>

  {#if !open}
    <!-- 折叠态：用一行摘要代替 51 个 chip，不占首屏 -->
    <p class="mt-1 truncate text-[11px] text-[var(--color-ink-500)]/80">
      {#if selected.length === 0}
        尚未选择指标 · 点「展开」按更新频率挑选
      {:else}
        {selected
          .slice(0, 8)
          .map((m) => defs.find((d) => d.metric === m)?.display_name_zh ?? m)
          .join(' · ')}{selected.length > 8 ? ` 等 ${selected.length} 项` : ''}
      {/if}
    </p>
  {:else}
    <div class="mt-2 flex flex-wrap items-center gap-2">
      <input
        class="min-w-0 flex-1 rounded-md border border-[var(--color-line)] bg-[var(--color-surface)] px-2 py-1 text-[12px] text-[var(--color-ink-700)] placeholder:text-[var(--color-ink-400)]"
        placeholder="搜索指标，如 体重 / tz / heart"
        bind:value={q}
      />
      <div class="flex flex-wrap gap-1">
        {#each [{ key: 'all' as const, label: `全部 ${counts.all}` }, { key: 'daily' as const, label: `日更 ${counts.daily}` }, { key: 'regular' as const, label: `定期 ${counts.regular}` }, { key: 'occasional' as const, label: `偶发 ${counts.occasional}` }, { key: 'never' as const, label: `未接入 ${counts.never}` }] as t (t.key)}
          <Chip size="sm" selected={freq === t.key} onclick={() => (freq = t.key)}>
            {t.label}
          </Chip>
        {/each}
      </div>
    </div>

    <div class="mt-1.5 flex flex-wrap items-center gap-1">
      <Chip size="sm" variant="quiet" selected={group === null} onclick={() => (group = null)}>
        全部分类
      </Chip>
      {#each METRIC_GROUPS as g (g.id)}
        <Chip
          size="sm"
          variant="quiet"
          selected={group === g.id}
          onclick={() => (group = group === g.id ? null : g.id)}
        >
          {g.label}
        </Chip>
      {/each}
    </div>

    <p class="mt-2 text-[10px] text-[var(--color-ink-500)]/80">
      频率档按近 {windowDays} 天内有数据的天数划分：日更 ≥ {bounds.daily} 天、定期 {bounds.regular}–{bounds.daily -
        1} 天、偶发 1–{bounds.regular - 1} 天。
    </p>

    <div class="mt-2 space-y-2.5">
      {#each sections as s (s.freq)}
        {@const hidden = s.freq === 'never' && !showNever}
        <div>
          <div class="flex items-baseline justify-between gap-2">
            <span class="text-[11px] text-[var(--color-ink-500)]">
              {FREQ_LABEL[s.freq]}
              <span class="num">{s.items.length}</span>
              项 · {FREQ_HINT[s.freq]}
            </span>
            {#if s.freq === 'never' && s.items.length > 0}
              <span class="flex shrink-0 items-center gap-2">
                <a
                  class="text-[11px] text-[var(--color-brand)] transition-opacity hover:underline"
                  href={resolve('/sync')}>去数据源与同步页接入</a
                >
                <button
                  class="text-[11px] text-[var(--color-ink-500)] transition-colors hover:text-[var(--color-brand)]"
                  onclick={() => (showNever = !showNever)}
                >
                  {showNever ? '收起' : '显示'}
                </button>
              </span>
            {:else if s.items.length > 0}
              <button
                class="shrink-0 text-[11px] text-[var(--color-ink-500)] transition-colors hover:text-[var(--color-brand)]"
                onclick={() => addAll(s.items)}
              >
                全选
              </button>
            {/if}
          </div>
          {#if !hidden}
            <div class="mt-1.5 flex flex-wrap gap-1.5">
              {#each s.items as d (d.metric)}
                {@const c = cov.get(d.metric)}
                {@const isNever = !c || c.freq === 'never'}
                <Chip
                  size="sm"
                  class="flex items-center gap-1.5 text-[12px]"
                  variant={isNever ? 'dashed' : 'outline'}
                  selected={selected.includes(d.metric)}
                  title={isNever ? `${FREQ_HINT.never} · 选中后卡片会给出接入入口` : undefined}
                  onclick={() => toggle(d.metric)}
                >
                  <span class="h-1.5 w-1.5 shrink-0 rounded-full {dotClass(c)}"></span>
                  <span>{d.display_name_zh}</span>
                  {#if c}
                    <span class="num text-[10px] text-[var(--color-ink-500)]/70"
                      >{agoText(c.daysSince) ?? '未接入'}</span
                    >
                  {/if}
                </Chip>
              {/each}
            </div>
          {/if}
        </div>
      {/each}
      {#if sections.length === 0}
        <p class="py-2 text-center text-[11px] text-[var(--color-ink-500)]">没有匹配的指标</p>
      {/if}
    </div>
  {/if}
</div>
