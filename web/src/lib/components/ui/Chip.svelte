<script lang="ts">
  import type { Snippet } from 'svelte';

  /**
   * 多选/单选胶囊。全项目 6 处，选中态原本已经逐字符一致，缺的只是这个组件。
   *
   * 尺寸分裂在 `px-2.5 py-1 text-[12px]`（概览、趋势）与 `px-2 py-0.5 text-[11px]`
   * （指标选择器）两档，`size` 参数覆盖；样式仍有出入的地方用 `class` 兜。
   */
  type Props = {
    selected?: boolean;
    /** outline=通用，quiet=未选中态更弱的一档，dashed=「该指标从未接入」 */
    variant?: 'outline' | 'quiet' | 'dashed';
    size?: 'md' | 'sm';
    disabled?: boolean;
    title?: string;
    onclick?: (event: MouseEvent) => void;
    class?: string;
    children: Snippet;
  };

  let {
    selected = false,
    variant = 'outline',
    size = 'md',
    disabled = false,
    title,
    onclick,
    class: klass = '',
    children
  }: Props = $props();

  const SIZE = { md: 'px-2.5 py-1 text-[12px]', sm: 'px-2 py-0.5 text-[11px]' } as const;

  let rest = $derived(
    selected
      ? 'border-transparent bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
      : variant === 'dashed'
        ? 'border-dashed border-[var(--color-line)] text-[var(--color-ink-400)] hover:bg-[var(--color-line)]/40'
        : variant === 'quiet'
          ? 'border-[var(--color-line)] text-[var(--color-ink-500)] hover:bg-[var(--color-line)]/50'
          : 'border-[var(--color-line)] text-[var(--color-ink-700)] hover:bg-[var(--color-line)]/50'
  );
</script>

<button
  class="rounded-full border transition-colors {SIZE[size]} {rest} {klass}"
  {disabled}
  {title}
  {onclick}
  aria-pressed={selected}
>
  {@render children()}
</button>
