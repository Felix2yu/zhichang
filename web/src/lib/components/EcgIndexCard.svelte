<script lang="ts">
  import type { EcgMetricKey } from '#lib/ecg-levels';
  import { ECG_METRICS, levelColor } from '#lib/ecg-levels';
  import HelpToggle from '#lib/components/ui/HelpToggle.svelte';

  type Props = {
    metric: EcgMetricKey;
    /** 区间均值 */
    score: number | null;
    /** 最近一次 */
    latest: number | null;
    /** 档位文案 */
    level: string;
    /** 「n 次有效测量」 */
    basis: string;
    /** 触发项（仅风险卡有） */
    drivers?: string[];
  };

  let { metric, score, latest, level, basis, drivers = [] }: Props = $props();
  // 卡片挂载后 metric 不会变，但用 $derived 读是更稳的写法：
  // 万一将来复用同一张卡切换指标，释义不会停留在旧值上。
  let meta = $derived(ECG_METRICS[metric]);

  // 说明面板默认收起：卡片本体保持紧凑，想看口径的人自己展开。
  let open = $state(false);
</script>

<div class="card flex flex-col p-3">
  <!-- 卡头：指标名 + 说明开关 -->
  <div class="flex items-start justify-between gap-1">
    <span class="text-[13px] leading-tight font-medium">{meta.title}</span>
    <HelpToggle bind:open class="mt-px" />
  </div>

  <!-- 数值 + 档位 -->
  <div class="mt-1.5 flex flex-wrap items-baseline gap-x-2 gap-y-1">
    <span class="num text-[26px] leading-none font-medium">
      {score === null ? '—' : score}
    </span>
    {#if level}
      <span
        class="rounded px-1.5 py-0.5 text-[11px]"
        style="color:{levelColor(metric, level)}; background:{levelColor(metric, level)}1f"
      >
        {level}
      </span>
    {/if}
  </div>

  <!-- 口径脚注：有效测量次数 + 最近一次 -->
  <div class="mt-1 text-[11px] text-[var(--color-ink-500)]">
    {basis}{#if latest !== null && latest !== score}
      · 最近一次 {latest}{/if}
  </div>

  <!-- 仪表条：0-100 轨道 + 档位色标 -->
  {#if score !== null}
    <div class="mt-2.5">
      <div
        class="relative h-1.5 rounded-full"
        style="background:linear-gradient(90deg,{meta.higherIsBetter
          ? 'var(--color-danger), var(--color-warn), var(--color-brand)'
          : 'var(--color-brand), var(--color-warn), var(--color-danger)'})"
      >
        <div
          class="absolute top-1/2 h-3 w-3 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-[var(--color-surface)]"
          style="left:{score}%; background:{levelColor(metric, level)}"
        ></div>
      </div>
      <div class="num mt-1 flex justify-between text-[10px] text-[var(--color-ink-400)]">
        <span>0</span><span>50</span><span>100</span>
      </div>
    </div>
  {:else}
    <div
      class="mt-2.5 rounded bg-[var(--color-surface-2)] px-2 py-1.5 text-[11px] text-[var(--color-ink-500)]"
    >
      信号质量达标的测量不足，暂无法计算
    </div>
  {/if}

  <!-- 触发项（风险卡）：透明规则的输出，原样展示 -->
  {#if metric === 'risk' && drivers.length > 0}
    <div
      class="mt-2 border-t border-[var(--color-line)] pt-2 text-[11px] leading-relaxed text-[var(--color-ink-700)]"
    >
      {#each drivers as d (d)}
        <div>· {d}</div>
      {/each}
    </div>
  {/if}

  <!-- 说明面板：含义 / 怎么算 / 可信度 / 怎么看 -->
  {#if open}
    <div
      class="mt-2 space-y-1.5 border-t border-[var(--color-line)] pt-2 text-[11px] leading-relaxed text-[var(--color-ink-700)]"
    >
      <p><b class="font-medium">含义</b>：{meta.meaning}</p>
      <p><b class="font-medium">怎么算</b>：{meta.formula}</p>
      <p><b class="font-medium">可信度</b>：{meta.trust}</p>
      <p><b class="font-medium">怎么看</b>：{meta.hint}</p>
    </div>
  {/if}
</div>
