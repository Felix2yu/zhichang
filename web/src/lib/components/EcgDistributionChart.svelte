<script lang="ts">
  import { codeColor } from '#lib/ecg-levels';
  import type { EcgDistItem } from '#lib/api';

  type Props = {
    items: EcgDistItem[];
    total: number;
    /** 当前筛选的判定码（点击图例切换，null = 全部） */
    activeCode: string | null;
    onfilter: (code: string | null) => void;
  };

  let { items, total, activeCode, onfilter }: Props = $props();

  const R = 40;
  const C = 2 * Math.PI * R;

  // 弧段按比例切环；offset 累加保证彼此相接
  let segs = $derived.by(() => {
    let acc = 0;
    return items.map((it) => {
      const dash = Math.max(it.ratio * C, 0.001); // 至少一笔，避免 0 长度不渲染
      const seg = { ...it, dash, gap: C - dash, offset: -acc * C };
      acc += it.ratio;
      return seg;
    });
  });

  let activeItem = $derived(items.find((it) => it.code === activeCode) ?? null);
</script>

<div class="flex items-center justify-between">
  <div class="text-[13px] font-medium">判定分布</div>
  {#if activeItem}
    <button class="text-[11px] text-[var(--color-brand)]" onclick={() => onfilter(null)}>
      只看「{activeItem.label}」× {activeItem.count} · 取消筛选
    </button>
  {/if}
</div>

<div class="mt-3 flex items-center gap-5">
  <!-- 环图 -->
  <svg viewBox="0 0 100 100" class="h-[120px] w-[120px] shrink-0" role="img" aria-label="判定分布">
    {#each segs as s (s.code)}
      <circle
        cx="50"
        cy="50"
        r={R}
        fill="none"
        stroke={codeColor(s.code)}
        stroke-width="13"
        stroke-dasharray="{s.dash} {s.gap}"
        stroke-dashoffset={s.offset}
        transform="rotate(-90 50 50)"
        opacity={activeCode === null || activeCode === s.code ? 1 : 0.25}
        class="cursor-pointer transition-opacity"
        role="button"
        tabindex="0"
        onclick={() => onfilter(activeCode === s.code ? null : s.code)}
        onkeydown={(e) => {
          if (e.key !== 'Enter' && e.key !== ' ') return;
          e.preventDefault();
          onfilter(activeCode === s.code ? null : s.code);
        }}><title>{s.label} {s.count} 次</title></circle
      >
    {/each}
    <text
      x="50"
      y="47"
      text-anchor="middle"
      class="num"
      fill="var(--color-ink-900)"
      style="font-size:16px;font-weight:500"
    >
      {total}
    </text>
    <text x="50" y="59" text-anchor="middle" fill="var(--color-ink-500)" style="font-size:8px">
      次测量
    </text>
  </svg>

  <!-- 图例 -->
  <div class="min-w-0 flex-1 space-y-1.5">
    {#each items as it (it.code)}
      <button
        class="flex w-full items-center gap-2 rounded px-1.5 py-1 text-left transition-colors hover:bg-[var(--color-surface-2)] {activeCode ===
        it.code
          ? 'bg-[var(--color-surface-2)]'
          : ''}"
        onclick={() => onfilter(activeCode === it.code ? null : it.code)}
      >
        <span class="h-2.5 w-2.5 shrink-0 rounded-full" style="background:{codeColor(it.code)}"
        ></span>
        <span class="min-w-0 flex-1 truncate text-[12px]">{it.label}</span>
        <span class="num shrink-0 text-[12px] text-[var(--color-ink-500)]">
          {it.count} · {Math.round(it.ratio * 100)}%
        </span>
      </button>
    {/each}
    <div class="px-1.5 text-[11px] text-[var(--color-ink-500)]">点击可筛选下方记录列表</div>
  </div>
</div>
