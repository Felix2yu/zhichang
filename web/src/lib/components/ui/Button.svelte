<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { MouseEventHandler } from 'svelte/elements';

  /**
   * 按钮。现状有 5 种手写口径（见 docs/ui-convergence.md §1.9），
   * 其中 settings 的 busy 反馈是逐处手写的 `busy === 'X' ? '保存中…' : '保存'`，
   * 每加一个按钮就复制一遍 —— 这里把它收成 `busy` 参数。
   *
   * variant 对应四种现有用法：outline（描边+次级文字）/ brand（描边+主色文字，
   * settings 的「导出」「保存」）/ ghost（无边框，hover 出底色）/ link（纯文字链接）
   * / danger（危险操作，含两段式确认的第二段）。
   */
  type Props = {
    variant?: 'outline' | 'brand' | 'ghost' | 'link' | 'danger';
    /** md = px-2.5 py-1 / sm = px-2 py-0.5，正文都是 11px；link 无内边距、12px */
    size?: 'md' | 'sm';
    busy?: boolean;
    /** busy 时显示的文案，缺省保留原文案 */
    busyText?: string;
    disabled?: boolean;
    title?: string;
    type?: 'button' | 'submit';
    onclick?: MouseEventHandler<HTMLButtonElement>;
    class?: string;
    children: Snippet;
  };

  let {
    variant = 'outline',
    size = 'md',
    busy = false,
    busyText = '',
    disabled = false,
    title,
    type = 'button',
    onclick,
    class: klass = '',
    children
  }: Props = $props();

  const SIZE = { md: 'px-2.5 py-1 text-[11px]', sm: 'px-2 py-0.5 text-[11px]' } as const;

  const SKIN = {
    outline: 'border border-[var(--color-line)] text-[var(--color-ink-700)]',
    brand: 'border border-[var(--color-line)] text-[var(--color-brand)]',
    ghost: 'text-[var(--color-ink-500)] hover:bg-[var(--color-surface-3)]',
    link: 'text-[12px] text-[var(--color-brand)] hover:underline',
    danger: 'border border-[var(--color-danger)] text-[var(--color-danger)]'
  } as const;
</script>

<button
  {type}
  {title}
  class="{variant === 'link' ? '' : 'rounded-md '}transition-colors {SIZE[size]} {SKIN[
    variant
  ]} disabled:opacity-40 {klass}"
  {disabled}
  aria-busy={busy}
  onclick={(e) => (busy ? e.preventDefault() : onclick?.(e))}
>
  {#if busy && busyText}
    {busyText}
  {:else}
    {@render children()}
  {/if}
</button>
