<script lang="ts">
  import Card from '#lib/components/ui/Card.svelte';
  import type { Snippet } from 'svelte';

  /**
   * 统计小卡。sleep 4 张（平均评分 / 平均总睡眠 / 平均入睡潜伏期 / 记录天数）
   * 与 workouts 3 张（次数 / 总时长 / 总消耗）结构逐字一致，
   * 唯一差别在第三行注脚：sleep 是 10px 弱化级（有断档时转 warn 色），
   * workouts 是 11px。注脚允许整个 snippet 传入，就是为了保住这个差别。
   */
  type Props = {
    label: string;
    value: string | number;
    /** 第三行注脚；不传则不渲染 */
    note?: Snippet;
    /** 注脚自身的类名：sleep 是 10px 弱化级，workouts 是 11px + mt-0.5 */
    noteClass?: string;
    class?: string;
  };

  let { label, value, note, noteClass = '', class: klass = '' }: Props = $props();
</script>

<Card pad="sm" class={klass}>
  <div class="text-[12px] text-[var(--color-ink-500)]">{label}</div>
  <div class="num mt-1 text-[20px] font-medium">{value}</div>
  {#if note}
    <div class="text-[11px] text-[var(--color-ink-500)] {noteClass}">{@render note()}</div>
  {/if}
</Card>
