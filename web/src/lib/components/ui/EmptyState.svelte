<script lang="ts">
  import type { Snippet } from 'svelte';

  /**
   * 空状态容器。三种容器形态：
   *
   *  - row：表格内的 colspan 行（sync 两张表 / workouts / ecg 筛选态）
   *  - block：图区占位，需要 `height` 撑住布局
   *  - panel：面板型，可承载多行解释（sleep 的若干图卡）
   *
   * 刻意**不**吸收 MetricTrendCard 的 never/pending/stale 三分支判断：
   * 那三条分支是数据语义（从未接入 / 今天还没同步 / 区间内没有但历史有），
   * 判断留在调用方，组件只给容器和片段。
   */
  type Props = {
    variant?: 'block' | 'row' | 'panel';
    /** variant='row' 时必给 */
    colspan?: number;
    /** block 的高度（px） */
    height?: number;
    /** 主文案；也可整段用 children 覆盖 */
    title?: string;
    desc?: Snippet;
    action?: Snippet;
    class?: string;
    children?: Snippet;
  };

  let {
    variant = 'block',
    colspan,
    height,
    title = '',
    desc,
    action,
    class: klass = '',
    children
  }: Props = $props();
</script>

{#snippet body()}
  {#if children}
    {@render children()}
  {:else}
    <div class="text-[13px] text-[var(--color-ink-500)]">{title}</div>
    {#if desc}
      <div class="mt-1 text-[11px] text-[var(--color-ink-500)]/70">{@render desc()}</div>
    {/if}
    {#if action}
      <div class="mt-2 text-[11px]">{@render action()}</div>
    {/if}
  {/if}
{/snippet}

{#if variant === 'row'}
  <tr>
    <td {colspan} class="px-4 py-6 text-center text-[var(--color-ink-500)] {klass}">
      {@render body()}
    </td>
  </tr>
{:else}
  <div
    class="flex flex-col items-center justify-center text-center text-[var(--color-ink-500)] {klass}"
    style={height ? `height:${height}px` : undefined}
  >
    {@render body()}
  </div>
{/if}
