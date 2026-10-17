<script lang="ts">
  import { dataColor } from '#lib/theme.svelte';

  // 卡片里的小走势图：只有几十像素高，用 ECharts 太重（一张卡一个实例，
  // 首屏七八张卡就是七八个 canvas + 各自的 resize 监听）。这里用原生 SVG 画，
  // 靠 viewBox + non-scaling-stroke 拉伸，线宽不跟着变形。
  type Point = { date: string; value: number };

  type Props = {
    points: Point[];
    height?: number;
    color?: string;
    /** 常态参考线：与极值纳入同一坐标系，"高于常态"在图上也能看出来 */
    baseline?: number | null;
  };

  let { points, height = 26, color = '#8aa4bf', baseline = null }: Props = $props();

  // viewBox 用固定宽度，实际尺寸交给 CSS 拉满：点数不同也不会改变线宽。
  const VW = 100;
  const pad = 3;

  let geom = $derived.by(() => {
    const pts = points.filter((p) => Number.isFinite(p.value));
    if (pts.length < 2) return null;
    const values = pts.map((p) => p.value);
    let min = Math.min(...values);
    let max = Math.max(...values);
    if (baseline !== null && Number.isFinite(baseline)) {
      min = Math.min(min, baseline);
      max = Math.max(max, baseline);
    }
    // 全相等的序列（从未变过的指标）退化成一条中线，别除以 0
    const span = max - min || 1;
    const step = (VW - pad * 2) / (pts.length - 1);
    const yOf = (v: number) => pad + (1 - (v - min) / span) * (height - pad * 2);
    return {
      line: pts
        .map((p, i) => `${(pad + i * step).toFixed(2)},${yOf(p.value).toFixed(2)}`)
        .join(' '),
      lastX: pad + (pts.length - 1) * step,
      lastY: yOf(pts[pts.length - 1].value),
      baseY: baseline !== null && Number.isFinite(baseline) ? yOf(baseline) : null
    };
  });
</script>

{#if geom}
  <svg
    class="block w-full"
    style="height:{height}px"
    viewBox="0 0 {VW} {height}"
    preserveAspectRatio="none"
    aria-hidden="true"
  >
    {#if geom.baseY !== null}
      <!-- 常态参考线是结构色，写在 style 里才能用 var()（SVG 呈现属性不解析变量） -->
      <line
        x1="0"
        x2={VW}
        y1={geom.baseY}
        y2={geom.baseY}
        style="stroke: var(--color-line-strong)"
        stroke-width="1"
        stroke-dasharray="3 2"
        vector-effect="non-scaling-stroke"
      />
    {/if}
    <polyline
      points={geom.line}
      fill="none"
      stroke={dataColor(color)}
      stroke-width="1.5"
      stroke-linejoin="round"
      stroke-linecap="round"
      vector-effect="non-scaling-stroke"
    />
    <circle
      cx={geom.lastX}
      cy={geom.lastY}
      r="1.8"
      fill={dataColor(color)}
      vector-effect="non-scaling-stroke"
    />
  </svg>
{:else}
  <!-- 点数不足时留出同样的高度，避免有/无走势时卡片高度不一致造成网格错位 -->
  <div style="height:{height}px"></div>
{/if}
