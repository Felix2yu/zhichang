<script lang="ts">
  import { todayStr } from '#lib/api';
  import {
    matchPreset,
    normalizeRange,
    presetRange,
    spanDays,
    THIS_YEAR,
    type Range
  } from '#lib/range';
  import Segmented from './Segmented.svelte';

  /**
   * 区间选择器：预设 + 自定义起止。
   *
   * 值是一对闭区间日期（含两端），不再是「最近 N 天」的整数——
   * 后者锚死在今天，问不出「3 月那两周」或「整个自然年」。
   * 天数由区间派生（分桶粒度、卡片上的「近 N 天」都取它）。
   */
  type Props = {
    value: Range;
    presets?: { label: string; preset: number }[];
    onchange: (range: Range) => void;
  };

  let { value, presets, onchange }: Props = $props();

  const defaults = [
    { label: '7 天', preset: 7 },
    { label: '30 天', preset: 30 },
    { label: '90 天', preset: 90 },
    { label: '365 天', preset: 365 },
    { label: '今年', preset: THIS_YEAR }
  ];

  /** 「自定义」在分段控件里的键：真实预设都是正数或 THIS_YEAR，取一个不可能的负数。 */
  const CUSTOM_KEY = -1;

  let items = $derived(presets ?? defaults);
  let opts = $derived<{ label: string; value: number }[]>([
    ...items.map((it) => ({ label: it.label, value: it.preset })),
    { label: '自定义', value: CUSTOM_KEY }
  ]);
  // 命中预设时高亮对应按钮；都没命中说明这是一段自定义区间
  let hit = $derived(
    matchPreset(
      value,
      items.map((it) => it.preset)
    )
  );
  let custom = $derived(hit === undefined);
  let segValue = $derived<number>(hit ?? CUSTOM_KEY);
  let editing = $state(false);

  // 输入框的草稿值：只填了一半或起止颠倒时不该把页面打进空区间，
  // 所以只在两端都合法时才提交（颠倒时 normalizeRange 替他换回来）。
  // 初值留空，由下面的 $effect 从 value 同步——直接写 value.from 只会取到首帧的值。
  let draftFrom = $state('');
  let draftTo = $state('');
  $effect(() => {
    draftFrom = value.from;
    draftTo = value.to;
  });

  function pick(preset: number) {
    editing = false;
    onchange(presetRange(preset));
  }

  function commit() {
    const next = normalizeRange({ from: draftFrom, to: draftTo }, value);
    if (next.from === value.from && next.to === value.to) return;
    onchange(next);
  }

  function onSegment(v: number) {
    if (v === CUSTOM_KEY) {
      editing = true;
      return;
    }
    pick(v);
  }
</script>

<div class="flex flex-wrap items-center gap-2">
  <Segmented value={segValue} options={opts} onchange={onSegment} />
  {#if custom || editing}
    <div class="flex items-center gap-1.5 text-[12px] text-[var(--color-ink-500)]">
      <input
        type="date"
        class="rounded-md border border-[var(--color-line)] bg-[var(--color-surface)] px-2 py-1 text-[12px] text-[var(--color-ink-700)]"
        max={draftTo || todayStr()}
        bind:value={draftFrom}
        onchange={commit}
      />
      <span>~</span>
      <input
        type="date"
        class="rounded-md border border-[var(--color-line)] bg-[var(--color-surface)] px-2 py-1 text-[12px] text-[var(--color-ink-700)]"
        min={draftFrom}
        max={todayStr()}
        bind:value={draftTo}
        onchange={commit}
      />
      <span class="num">{spanDays(value)} 天</span>
    </div>
  {/if}
</div>
