<script lang="ts" generics="T extends string | number">
  /** 分段选择器：RangePicker（天数）与设置页的若干项共用这一套外观。 */
  type Props = {
    value: T;
    options: { label: string; value: T }[];
    onchange: (value: T) => void;
  };

  let { value, options, onchange }: Props = $props();
</script>

<div
  class="inline-flex overflow-hidden rounded-md border border-[var(--color-line)] bg-[var(--color-surface)]"
>
  {#each options as it (it.value)}
    <button
      class="px-3 py-1.5 text-[12px] transition-colors {value === it.value
        ? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
        : 'text-[var(--color-ink-700)] hover:bg-[var(--color-line)]/50'}"
      onclick={() => onchange(it.value)}
    >
      {it.label}
    </button>
  {/each}
</div>
