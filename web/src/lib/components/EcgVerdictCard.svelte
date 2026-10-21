<script lang="ts">
  import { codeColor } from '#lib/ecg-levels';
  import HelpToggle from '#lib/components/ui/HelpToggle.svelte';

  type Props = {
    /** 众数判定（如「窦性心律」） */
    dominantLabel: string;
    /** 众数占比 0-1 */
    dominantRatio: number;
    count: number;
    /** 有有效指数的次数 */
    withMetrics: number;
    /** 异常/测不准次数 */
    abnormalCount: number;
    /** 最近一次：日期 + 时刻 + 判定 */
    latestDate: string;
    latestClock: string;
    latestLabel: string;
    /** 最近若干次判定的时间点（旧 → 新） */
    dots: { code: string; date: string; clock: string; label: string }[];
    /** 测量天数 */
    measuredDays: number;
  };

  let {
    dominantLabel,
    dominantRatio,
    count,
    withMetrics,
    abnormalCount,
    latestDate,
    latestClock,
    latestLabel,
    dots,
    measuredDays
  }: Props = $props();

  let open = $state(false);
</script>

<div class="card flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
  <!-- 左：判定印象 -->
  <div class="min-w-0">
    <div class="flex items-center gap-2">
      <span class="text-[13px] font-medium">心电分析印象</span>
      <HelpToggle bind:open />
    </div>
    <div class="mt-1.5 flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
      <span class="text-[22px] leading-none font-medium" style="color:{codeColor('sinus_rhythm')}">
        {dominantLabel}
      </span>
      <span class="num text-[12px] text-[var(--color-ink-500)]">
        {Math.round(dominantRatio * count)}/{count} 次
      </span>
    </div>
    <div class="num mt-1.5 text-[12px] text-[var(--color-ink-500)]">
      {measuredDays} 天里测了 {count} 次 · 最近一次 {latestDate.slice(5)}
      {latestClock} · {latestLabel}
    </div>
    {#if abnormalCount > 0}
      <div class="mt-1 text-[12px] text-[var(--color-warn)]">
        另有 {abnormalCount} 次测不出结论、记录欠佳或未识别
      </div>
    {:else}
      <div class="mt-1 text-[12px] text-[var(--color-ink-500)]">没有异常或测不准的提示</div>
    {/if}
  </div>

  <!-- 右：最近判定的时间点 -->
  <div class="shrink-0">
    <div class="text-[11px] text-[var(--color-ink-500)]">最近 {dots.length} 次判定</div>
    <div class="mt-1.5 flex items-center gap-1.5">
      {#each dots as d (d.date + d.clock)}
        <span
          class="h-2.5 w-2.5 rounded-full"
          style="background:{codeColor(d.code)}"
          title="{d.date.slice(5)} {d.clock} · {d.label}"
        ></span>
      {/each}
    </div>
  </div>
</div>

{#if open}
  <div class="card mt-2 space-y-1.5 p-4 text-[12px] leading-relaxed text-[var(--color-ink-700)]">
    <p>
      <b class="font-medium">含义</b>：这是所选时间段里占比最高的判定，来自 iPhone/Apple Watch
      心电测量后 App 自动给出的结论，我们把它收敛成了跨手机语言稳定的统一措辞。
    </p>
    <p>
      <b class="font-medium">可信度</b>：高——判定出自设备官方算法。但它只覆盖每次测量那 约 30
      秒，抓不到间歇性异常；测了 {withMetrics} 次波形可计算指数的测量也只是抽样。
    </p>
    <p>
      <b class="font-medium">怎么看</b>：右侧每个圆点是一次测量（颜色对应判定）。出现非绿色圆点
      值得点开对应记录看波形；房颤提示请就医。
    </p>
  </div>
{/if}
