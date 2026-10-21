<script lang="ts">
  import type { Snippet } from 'svelte';

  /**
   * 布局容器层：只负责 .card 的背景 / 描边 / 圆角（见 app.css），零逻辑。
   *
   * 工程约定（docs/ui-convergence.md §6.1）：业务组件不自带 card，容器与留白一律由页面给。
   * 本组件就是「页面不想再手写一遍类名」时的那个 `<div class="card p-4">`，不承载语义。
   */
  type Props = {
    /** 内边距档位。none 用于表格等自带留白的内容 */
    pad?: 'md' | 'sm' | 'none';
    class?: string;
    children: Snippet;
  };

  let { pad = 'md', class: klass = '', children }: Props = $props();

  const PAD = { md: 'p-4', sm: 'px-4 py-3', none: '' } as const;
</script>

<div class="card {PAD[pad]} {klass}">{@render children()}</div>
