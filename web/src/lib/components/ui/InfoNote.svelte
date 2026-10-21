<script lang="ts">
  import type { Snippet } from 'svelte';

  /**
   * 口径脚注：区块底部那一行小字，说明算法口径或数据来源。全项目 19 处。
   *
   * 现状分裂在字号（11px vs 10px）和透明度（`/80` vs 纯色）上。透明度后缀的收敛
   * 属于 P2 的视觉决策，本组件保持现状可选：`dim` 默认 true（= `/80`）。
   */
  type Props = {
    /** md=11px（默认） / sm=10px */
    size?: 'md' | 'sm';
    dim?: boolean;
    class?: string;
    children: Snippet;
  };

  let { size = 'md', dim = true, class: klass = '', children }: Props = $props();
</script>

<div
  class="{size === 'md' ? 'text-[11px]' : 'text-[10px]'} leading-relaxed {dim
    ? 'text-[var(--color-ink-500)]/80'
    : 'text-[var(--color-ink-500)]'} {klass}"
>
  {@render children()}
</div>
