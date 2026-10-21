<script lang="ts">
  import type { Snippet } from 'svelte';

  /**
   * 卡片区块标题：13px 中粗 + 右侧 action 槽。
   *
   * 现状里有 22 处 `text-[13px] font-medium`，容器写法分 `mb-2` / `mb-2.5` 两档、
   * 标签分 `<h2>` / `<span>` 两种。这里统一用 `<h2>`（语义更好），间距差异由 `class` 覆盖。
   */
  type Props = {
    children: Snippet;
    /** 右上角：次级说明或「查看更多」链接 */
    action?: Snippet;
    class?: string;
  };

  let { children, action, class: klass = '' }: Props = $props();
</script>

<div class="mb-2 flex flex-wrap items-baseline justify-between gap-2 {klass}">
  <h2 class="text-[13px] font-medium">{@render children()}</h2>
  {#if action}
    <div class="text-[11px]">{@render action()}</div>
  {/if}
</div>
