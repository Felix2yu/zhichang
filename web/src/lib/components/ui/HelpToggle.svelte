<script lang="ts">
  import type { Snippet } from 'svelte';

  /**
   * 「?」说明开关。原本在 EcgIndexCard 与 EcgVerdictCard 里各写了一遍，
   * 连 aria-label 都相同。
   *
   * 展开态用 `bind:open` 交给调用方决定内容放在哪 —— 两张卡的正文位置不同
   * （一个在卡内底部，一个在卡下方另起一张卡），所以这里不能把 children 写死在按钮后面。
   */
  type Props = {
    open?: boolean;
    label?: string;
    class?: string;
    /** 少数场景下说明面板紧跟按钮，多数场景建议用 bind:open */
    children?: Snippet;
  };

  let {
    open = $bindable(false),
    label = '查看指标说明',
    class: klass = '',
    children
  }: Props = $props();
</script>

<button
  class="flex h-4 w-4 shrink-0 items-center justify-center rounded-full border border-[var(--color-line)] text-[10px] text-[var(--color-ink-500)] transition-colors hover:border-[var(--color-ink-500)] hover:text-[var(--color-ink-700)] {klass}"
  aria-label={label}
  aria-expanded={open}
  onclick={() => (open = !open)}
>
  ?
</button>
{#if children && open}
  {@render children()}
{/if}
