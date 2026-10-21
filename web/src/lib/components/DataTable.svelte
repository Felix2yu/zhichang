<script lang="ts" generics="T">
  import type { Snippet } from 'svelte';

  /**
   * 表格外壳。5 个 `<table>` + 1 个紧凑变体（EcgComparisonCard）里，
   * 表头口径（`bg-surface-2` / `text-[12px]` / `px-4 py-2` / `border-t`）原本就已经一致，
   * 真正不一致的是**行语义**：sync 纯只读、workouts 点击选中、sleep 点击切夜晚、
   * ecg 点击展开详情行。
   *
   * 所以这里只抽外壳：行内容永远由 `children` 渲染，`<tr>` 的点击、键盘可达与选中色
   * 由本组件负责 —— 顺带让三处「裸 `<tr onclick>` 键盘完全不可达」一次性消失。
   *
   * 单元格类名不在本组件里注入： `<td>` 由调用方写（多数是 `px-4 py-2`，compact 用
   * `px-4 py-1.5`），这样跨列合并、行内小标题、进度条之类特殊情况不必开新的 props。
   *
   * 刻意不吸收的能力：ecg 的行内展开走 `expand` 片段。那个展开块挂了 `use:reveal`
   * 滚动 action，是页面专属行为，收进组件会把业务假设带进通用层。
   */
  type Align = 'left' | 'right' | 'center';

  type Column = {
    label: string;
    align?: Align;
    /** 表头的悬浮说明（如「主动能量口径」这类必须写出来的口径） */
    title?: string;
    class?: string;
  };

  type Props = {
    columns: Column[];
    rows: T[];
    rowKey: (row: T) => string;
    /** 选中行的 key；不传表示这张表没有选中态 */
    selectedKey?: string | null;
    /**
     * 行点击回调。传了之后行可获得焦点并响应 Enter/Space。
     * 命中行内的 button / a / input / label 时不触发，调用方因此不必再写 stopPropagation。
     */
    onrowclick?: (row: T) => void;
    rowTitle?: (row: T) => string | undefined;
    /** compact = 12px 字号，对应 EcgComparisonCard 的密度 */
    density?: 'normal' | 'compact';
    /** ecg 的选中行语义更强（这一行对应正在看的波形），用主色底 */
    emphasis?: boolean;
    /** 横向滚动所需的最小宽度，给了就套 overflow-x-auto */
    minWidth?: string;
    /** rows 为空时渲染；通常是 EmptyState variant="row" */
    empty?: Snippet;
    /** 行之后的附加行，用于行内展开 */
    expand?: Snippet<[T]>;
    children: Snippet<[T, number]>;
  };

  let {
    columns,
    rows,
    rowKey,
    selectedKey,
    onrowclick,
    rowTitle,
    density = 'normal',
    emphasis = false,
    minWidth,
    empty,
    expand,
    children
  }: Props = $props();

  let textClass = $derived(density === 'compact' ? 'text-[12px]' : 'text-[13px]');
  let selectedSkin = $derived(
    emphasis ? 'bg-[var(--color-brand-soft)]/60' : 'bg-[var(--color-surface-3)]'
  );

  // 行内还有自己的按钮/链接时不该顺带触发行点击。这个判断原本在 MetricTrendCard
  // 里手写过一遍，放在这里之后所有页面自动拿到。
  function isRowTarget(e: Event) {
    return !(e.target as HTMLElement | null)?.closest('button, a, input, label');
  }

  function handleClick(e: MouseEvent, row: T) {
    if (isRowTarget(e)) onrowclick?.(row);
  }

  function handleKey(e: KeyboardEvent, row: T) {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    onrowclick?.(row);
  }
</script>

<div class={minWidth ? 'overflow-x-auto' : undefined}>
  <table class="w-full {textClass}" style={minWidth ? `min-width:${minWidth}` : undefined}>
    <thead class="bg-[var(--color-surface-2)] text-[12px] text-[var(--color-ink-500)]">
      <tr>
        {#each columns as c (c.label)}
          <th
            class="px-4 py-2 font-normal {c.align === 'right'
              ? 'text-right'
              : c.align === 'center'
                ? 'text-center'
                : 'text-left'} {c.class ?? ''}"
            title={c.title}>{c.label}</th
          >
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each rows as row, i (rowKey(row))}
        {@const active = selectedKey === rowKey(row)}
        <tr
          class="border-t border-[var(--color-line)] {onrowclick
            ? 'cursor-pointer hover:bg-[var(--color-surface-2)]'
            : ''} {active ? selectedSkin : ''}"
          tabindex={onrowclick ? 0 : undefined}
          onclick={onrowclick ? (e) => handleClick(e, row) : undefined}
          onkeydown={onrowclick ? (e) => handleKey(e, row) : undefined}
          title={rowTitle?.(row)}
        >
          {@render children(row, i)}
        </tr>
        {#if expand}{@render expand(row)}{/if}
      {/each}
      {#if rows.length === 0 && empty}{@render empty()}{/if}
    </tbody>
  </table>
</div>
