<script lang="ts">
  import type { Snippet } from 'svelte';

  /**
   * 页头：15px 标题 + 右侧工具槽（区间选择器 / 视图切换 / 日期）+ 可选副说明行。
   *
   * 7 个页面的 `<h1 class="text-[15px] font-medium">` 字号口径原本已经自发一致，
   * 差别只在右侧内容的有无，所以组件只管布局，不碰数据流。
   */
  type Props = {
    children: Snippet;
    /** 右侧工具；多数页面是 RangePicker，概览是日期文案，两个页面没有 */
    actions?: Snippet;
    /** 标题下方的副说明行（设置页的用法） */
    note?: Snippet;
    noteClass?: string;
    class?: string;
  };

  let { children, actions, note, noteClass = '', class: klass = '' }: Props = $props();
</script>

<div class="flex flex-wrap items-baseline justify-between gap-2 {klass}">
  <h1 class="text-[15px] font-medium">{@render children()}</h1>
  {#if actions}
    <div class="text-[12px] text-[var(--color-ink-500)]">{@render actions()}</div>
  {/if}
</div>
{#if note}
  <div class="mb-1 text-[12px] text-[var(--color-ink-500)] {noteClass}">{@render note()}</div>
{/if}
