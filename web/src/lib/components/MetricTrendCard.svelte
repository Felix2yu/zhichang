<script lang="ts">
  import * as echarts from 'echarts';
  import type { MetricDef } from '#lib/api';
  import { dataColor, palette } from '#lib/theme.svelte';
  import { bucketSeries, grainFor, type Grain } from '#lib/trend-buckets';
  import {
    TIER_CHART_HEIGHT,
    cadenceText,
    gapDays,
    tierOfPoints,
    type Tier
  } from '#lib/metric-tiers';
  import { resolve } from '$app/paths';
  import { stepStyle, type EmptyKind } from '#lib/metric-render';
  import DensityStrip from './DensityStrip.svelte';

  // 单指标趋势卡：一张卡只承载一个指标、一套坐标轴与单位。
  //
  // 与旧版的关键差别是**图形形态由区间内实际点数决定**，不再所有指标共用一张折线图：
  //   · 密集（>=24 点）画连续趋势，跨度长时自动按周/月聚合；
  //   · 中等（6–23 点）画点线图，符号常显、关闭平滑；
  //   · 稀疏（2–5 点）画阶梯或点列，标出每次的数值与相隔天数；
  //   · 只有 1 次记录不画图，只给数值与日期；
  //   · 没有数据给可行动的空态。
  //
  // 另一个关键差别是 X 轴一律用真实时间轴。旧版用 category 轴直接铺后端返回的点，
  // 而后端只返回有数据的日期——于是「1 月、3 月、8 月各测一次」画出来是相邻三个点，
  // 「中间两个月没测」这个最重要的事实被抹平了。

  type Point = { date: string; value: number };

  type Props = {
    metric: string;
    name: string;
    unit: string;
    /** 区间内的数据点（无需排序） */
    points: Point[];
    /** 指标 kind：累计量画柱，其余画线 */
    kind: MetricDef['kind'];
    /** 日内聚合口径：决定长跨度分桶时是「日均」还是「均值」 */
    dayAgg: MetricDef['day_agg'];
    /** 主色（取自所属分类的 tint） */
    color: string;
    /** 数值小数位 */
    digits: number;
    /** 口径说明：日内聚合 · 跨来源聚合 */
    aggNote: string;
    loading?: boolean;
    /** 统计区间天数，用于粒度判定与空态文案 */
    days?: number;
    /** 常态（30 天中位数）；仅连续档画参考线 */
    baseline?: number | null;
    /** 久未更新的提示文案；null 表示不提示 */
    staleText?: string | null;
    /** 陈旧提示是否加强 */
    staleStrong?: boolean;
    /** 回溯信息：区间内点数不足时已自动扩展取数窗口 */
    backfill?: { count: number; days: number } | null;
    /** 近一年的测量日期，用于底部密度条；与图表区间无关 */
    strip?: string[];
    /** 空态成因：从未接入 / 今天还没同步 / 区间内没有但历史有 */
    emptyKind?: EmptyKind;
    /** 最近一次测量的日期与距今多久，空态时展示 */
    lastText?: string;
    /** 点了这张卡要做什么（网格里用于进入聚焦视图） */
    onselect?: () => void;
    /** 空态里点「显示最近 6 次」 */
    onbackfill?: () => void;
    /** 点「恢复区间」关掉回溯 */
    onbackfilloff?: () => void;
    /** 聚焦视图：图区高度翻倍 */
    tall?: boolean;
    /** 透传类名（网格项用 h-full 拉平同排卡片高度） */
    class?: string;
  };

  let {
    metric,
    name,
    unit,
    points,
    kind,
    dayAgg,
    color,
    digits,
    aggNote,
    loading = false,
    days = 30,
    baseline = null,
    staleText = null,
    staleStrong = false,
    backfill = null,
    strip = [],
    emptyKind = 'stale',
    lastText = '',
    onselect = undefined,
    onbackfill = undefined,
    onbackfilloff = undefined,
    tall = false,
    class: klass = ''
  }: Props = $props();

  let el: HTMLDivElement | undefined = $state();
  // 用 $state.raw 让「实例就绪」本身成为依赖：卡片通常在数据到达前就挂载了，
  // 若 chart 是普通变量，下面的重绘 effect 会在 chart 尚不存在时早退且不再被唤醒。
  let chart = $state.raw<echarts.ECharts | undefined>(undefined);

  // —— 数值格式化 ——
  function fmt(v: number): string {
    if (!Number.isFinite(v)) return '—';
    if (digits === 0 && Math.abs(v) >= 1000) return Math.round(v).toLocaleString('zh-CN');
    return v.toFixed(digits);
  }
  // Y 轴刻度：按本图数据最大值统一量纲——上万整轴用「万」，否则千分位。
  function axisFmt(maxAbs: number): (v: number) => string {
    if (maxAbs >= 10000) {
      return (v: number) => {
        if (v === 0) return '0';
        const n = v / 10000;
        return `${Number.isInteger(n) ? n : +n.toFixed(1)}万`;
      };
    }
    return (v: number) => Math.round(v).toLocaleString('zh-CN');
  }

  // —— 排序后的点与档位 ——
  let pts = $derived(
    [...points]
      .filter((p) => p?.date && Number.isFinite(p.value))
      .sort((a, b) => (a.date < b.date ? -1 : a.date > b.date ? 1 : 0))
  );
  let dates = $derived(pts.map((p) => p.date));
  let tier = $derived<Tier>(tierOfPoints(pts.length, dates));
  let hasData = $derived(tier !== 'empty' && tier !== 'single');
  let chartHeight = $derived(TIER_CHART_HEIGHT[tier] * (tall ? 2 : 1));

  // 长跨度只聚合连续档：稀疏档本来就没几根，再按周聚合就只剩一两个点了
  let grain = $derived<Grain>(grainFor(days));
  let buckets = $derived(tier === 'continuous' && grain !== 'day' ? bucketSeries(pts, grain) : []);
  /** 分桶键 → 时间戳用的日期：月桶键是 YYYY-MM，补成当月 1 号 */
  function keyToDate(key: string): string {
    return grain === 'month' ? `${key}-01` : key;
  }

  let cadence = $derived(cadenceText(dates));

  // —— 统计量 ——
  let stats = $derived.by(() => {
    if (pts.length === 0) return null;
    const vs = pts.map((p) => p.value);
    const sum = vs.reduce((s, v) => s + v, 0);
    const latest = pts[pts.length - 1];
    const prev = pts.length >= 2 ? pts[pts.length - 2] : null;
    return {
      avg: sum / vs.length,
      max: Math.max(...vs),
      min: Math.min(...vs),
      latest: latest.value,
      latestDate: latest.date,
      prev: prev ? prev.value : null,
      prevDate: prev ? prev.date : null,
      delta: prev ? latest.value - prev.value : null,
      gap: prev ? gapDays(prev.date, latest.date) : null
    };
  });
  let maxAbs = $derived(pts.reduce((m, p) => Math.max(m, Math.abs(p.value)), baseline ?? 0) || 0);

  // 稀疏档：相邻两次测量的间隔，画在阶梯横线上。
  // 点太多时只画阶梯不标数字——19 个「42 天」标签会盖住图本身。
  let gapMarks = $derived.by(() => {
    if (tier !== 'step' || pts.length < 2 || pts.length > 8) return [];
    const out: { from: string; to: string; value: number; days: number }[] = [];
    for (let i = 1; i < pts.length; i++) {
      const g = gapDays(pts[i - 1].date, pts[i].date);
      if (g === null || g <= 0) continue;
      out.push({ from: pts[i - 1].date, to: pts[i].date, value: pts[i - 1].value, days: g });
    }
    return out;
  });

  // 辅助线：连续档画常态参考线；稀疏档在阶梯横线上标「这个值保持了多久」。
  // 两者互斥（档位互斥），合成一个配置避免同一 series 上出现两个 markLine。
  let markLine = $derived.by(() => {
    const p = palette();
    if (gapMarks.length > 0) {
      return {
        silent: true,
        symbol: 'none' as const,
        // 线本身由 series 的阶梯画出来，这里只借 markLine 挂标签
        lineStyle: { color: 'transparent', width: 0 },
        label: {
          show: true,
          position: 'insideMiddleTop' as const,
          color: p.inkSoft,
          fontSize: 10,
          formatter: (a: { dataIndex?: number }) => {
            const g = gapMarks[a.dataIndex ?? 0];
            return g ? `${g.days} 天` : '';
          }
        },
        // markLine 的二维数据项必须是定长二元组，map 推断出来的数组类型对不上
        data: gapMarks.map(
          (g) =>
            [
              { xAxis: g.from, yAxis: g.value },
              { xAxis: g.to, yAxis: g.value }
            ] as [{ xAxis: string; yAxis: number }, { xAxis: string; yAxis: number }]
        )
      };
    }
    if (baseline !== null && Number.isFinite(baseline) && tier === 'continuous') {
      return {
        silent: true,
        symbol: 'none' as const,
        lineStyle: { color: p.inkFaint, type: 'dashed' as const, width: 1 },
        label: {
          show: true,
          position: 'insideEndTop' as const,
          formatter: '常态',
          color: p.inkFaint,
          fontSize: 10
        },
        data: [{ yAxis: baseline }]
      };
    }
    return undefined;
  });

  function buildOption(): echarts.EChartsOption {
    const p = palette();
    const af = axisFmt(maxAbs);
    const main = dataColor(color);
    const useBuckets = buckets.length > 0;
    const data: [string, number][] = useBuckets
      ? buckets.map((b) => [keyToDate(b.key), b.value])
      : pts.map((q) => [q.date, q.value]);
    const bar = kind === 'cumulative' || kind === 'categorical';
    const { step, lineType } = stepStyle(metric);
    const useStep = tier === 'step' && step === 'end';

    return {
      animationDuration: 260,
      grid: { left: 4, right: 8, top: 10, bottom: 4, containLabel: true },
      tooltip: {
        trigger: 'axis',
        backgroundColor: p.surface,
        borderColor: p.line,
        borderWidth: 1,
        textStyle: { color: p.ink, fontSize: 12 },
        axisPointer: { type: bar ? 'shadow' : 'line', lineStyle: { color: p.line } },
        formatter: (params) => {
          const arr = Array.isArray(params) ? params : [params];
          if (arr.length === 0) return '';
          const pp = arr[0] as { value?: unknown; marker?: string; dataIndex?: number };
          const v = Array.isArray(pp.value) ? Number(pp.value[1]) : Number(pp.value ?? 0);
          const ts = Array.isArray(pp.value) ? Number(pp.value[0]) : NaN;
          const d = new Date(ts);
          const ymd = Number.isFinite(ts)
            ? `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
            : '';
          const b = useBuckets ? buckets[pp.dataIndex ?? 0] : null;
          // 分桶后必须说明这一根是 N 天的日均，否则周桶会被读成 7 倍的日值
          const scope = b
            ? grain === 'month'
              ? `${b.key} · ${b.n} 天${dayAgg === 'sum' ? '日均' : '均值'}`
              : `${ymd} 起那周 · ${b.n} 天${dayAgg === 'sum' ? '日均' : '均值'}`
            : ymd;
          return `<div style="font-size:11px;color:${p.inkSoft};margin-bottom:2px">${scope}</div>
						<div style="font-size:12px;color:${p.ink}">${pp.marker ?? ''}${name} <b>${fmt(v)}${unit ? ' ' + unit : ''}</b></div>`;
        }
      },
      xAxis: {
        type: 'time',
        axisLine: { lineStyle: { color: p.line } },
        axisTick: { show: false },
        axisLabel: {
          color: p.inkFaint,
          fontSize: 10,
          hideOverlap: true,
          formatter: (v: number) => {
            const d = new Date(v);
            const mm = String(d.getMonth() + 1).padStart(2, '0');
            const dd = String(d.getDate()).padStart(2, '0');
            // 稀疏档跨月甚至跨年，只写 MM-DD 看不出间隔有多长
            return tier === 'continuous'
              ? `${mm}-${dd}`
              : `${String(d.getFullYear()).slice(2)}-${mm}-${dd}`;
          }
        }
      },
      yAxis: {
        type: 'value',
        scale: !bar,
        splitNumber: 3,
        axisLine: { show: false },
        axisTick: { show: false },
        splitLine: { lineStyle: { color: p.split } },
        axisLabel: { color: p.inkFaint, fontSize: 10, formatter: (v: number) => af(v) }
      },
      series: [
        {
          type: bar ? 'bar' : 'line',
          data,
          step: useStep ? 'end' : false,
          // 平滑连线在 3 个点的序列上完全是编出来的，全档位关闭
          smooth: false,
          symbol: 'circle',
          symbolSize: tier === 'step' ? 5 : 3,
          showSymbol: tier === 'step' || tier === 'points',
          lineStyle: {
            width: 1.8,
            color: main,
            type: tier === 'step' && lineType === 'dashed' ? 'dashed' : 'solid'
          },
          itemStyle: { color: main, borderRadius: bar ? [3, 3, 0, 0] : 0 },
          barMaxWidth: 14,
          areaStyle: !bar && tier === 'continuous' ? { color: main, opacity: 0.08 } : undefined,
          label:
            tier === 'step' && pts.length <= 6
              ? {
                  show: true,
                  position: 'top',
                  fontSize: 10,
                  color: p.inkSoft,
                  formatter: (a: { value?: unknown }) =>
                    Array.isArray(a.value) ? fmt(Number(a.value[1])) : ''
                }
              : undefined,
          markLine
        }
      ]
    };
  }

  // 图表容器是条件渲染的（单次记录与空态不画图），所以初始化不能挂在 onMount 上：
  // 卡片先以空态挂载、数据到达后才切到图形区时，onMount 早已跑完，图表永远不会创建。
  // 这里跟着 el 的挂载/卸载走，天然覆盖了档位来回切换的情况。
  $effect(() => {
    if (!el) return;
    const c = echarts.init(el, undefined, { renderer: 'canvas' });
    chart = c;
    const onResize = () => c.resize();
    window.addEventListener('resize', onResize);
    // 档位切换会改图区高度（step 104 / 连续 150 / 聚焦翻倍），窗口 resize 收不到这种变化
    const ro = new ResizeObserver(onResize);
    ro.observe(el);
    return () => {
      ro.disconnect();
      window.removeEventListener('resize', onResize);
      c.dispose();
      if (chart === c) chart = undefined;
    };
  });

  // 数据/档位变化时重绘（不画图时不初始化图形）
  $effect(() => {
    // 先读 hasData 再读 chart：两个依赖都要被订阅，
    // 否则「先有数据、后有实例」的挂载顺序下会漏画。
    if (!hasData || !chart) return;
    chart.setOption(buildOption(), { notMerge: true });
  });

  // 整卡点击用于进聚焦，卡内的按钮（回溯开关、显示最近 6 次）必须截住事件，
  // 否则点「恢复区间」会顺带跳到大图
  function runInner(fn: (() => void) | undefined) {
    return (e: MouseEvent) => {
      e.stopPropagation();
      fn?.();
    };
  }
  function handleCardKey(e: KeyboardEvent) {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    onselect?.();
  }

  // 卡内还有「恢复区间」「显示最近 6 次」等按钮，根元素用 div + role 而不是 button：
  // button 里嵌 button 是非法 HTML，浏览器的点击分发会变得不可靠
  function handleCardClick(e: MouseEvent) {
    const t = e.target as HTMLElement | null;
    if (t?.closest('button, a')) return;
    onselect?.();
  }
</script>

<!-- 网格里整张卡可点：列表行能进聚焦，卡片反而进不去的话，
     聚焦这个视图就成了只有切到列表才够得着的死角 -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div
  class="card flex flex-col p-3 {klass} {onselect
    ? 'cursor-pointer transition-colors hover:bg-[var(--color-surface-2)]'
    : ''}"
  role={onselect ? 'button' : undefined}
  tabindex={onselect ? 0 : undefined}
  aria-label={onselect ? `查看 ${name} 的大图` : undefined}
  onclick={onselect ? handleCardClick : undefined}
  onkeydown={onselect ? handleCardKey : undefined}
>
  <!-- 卡头：指标名 + 单位徽标 · 最新值 + 记录日期 -->
  <div class="flex items-start justify-between gap-2">
    <div class="flex min-w-0 items-center gap-1.5">
      <span class="truncate text-[13px] font-medium">{name}</span>
      <span
        class="shrink-0 rounded bg-[var(--color-line)]/60 px-1.5 py-0.5 text-[10px] text-[var(--color-ink-500)]"
        >{unit || '—'}</span
      >
    </div>
    <div class="shrink-0 text-right">
      {#if loading}
        <div class="ml-auto h-[16px] w-14 animate-pulse rounded bg-[var(--color-line)]"></div>
      {:else if stats}
        <div class="num text-[15px] leading-tight font-medium">{fmt(stats.latest)}</div>
        {#if stats.delta !== null}
          <div class="num text-[11px] text-[var(--color-ink-500)]">
            {stats.delta > 0 ? '▲' : stats.delta < 0 ? '▼' : '—'}
            {fmt(Math.abs(stats.delta))}
          </div>
        {/if}
      {:else}
        <div class="text-[13px] text-[var(--color-ink-500)]">—</div>
      {/if}
    </div>
  </div>

  <!-- 稀疏指标必须标出「这个值是哪天记的」：40 天前的体重不该看起来跟今天的步数一样新鲜 -->
  {#if stats && !loading}
    <div class="mt-0.5 flex items-center justify-between gap-2">
      <span class="num text-[10px] text-[var(--color-ink-500)]/80">
        {stats.latestDate}
        {#if tier === 'step' || tier === 'single'}记录{/if}
        {#if cadence}· {cadence}{/if}
      </span>
      {#if backfill}
        <span class="flex shrink-0 items-center gap-1">
          <span
            class="rounded bg-[var(--color-warn)]/15 px-1.5 py-0.5 text-[10px] text-[var(--color-warn)]"
            title="区间内记录太少，已自动回溯取数">{backfill.days} 天 {backfill.count} 次</span
          >
          {#if onbackfilloff}
            <!-- 回溯改写了点数与卡脚统计，用户必须能退回区间口径 -->
            <button
              class="text-[10px] text-[var(--color-ink-500)] transition-colors hover:text-[var(--color-brand)]"
              onclick={runInner(onbackfilloff)}>恢复区间</button
            >
          {/if}
        </span>
      {/if}
    </div>
  {/if}

  <!-- 图形区：只有 2 个点以上才画 -->
  {#if tier === 'single'}
    <div class="mt-2 rounded-md border border-dashed border-[var(--color-line)] px-3 py-4">
      <div class="text-center text-[12px] text-[var(--color-ink-500)]">
        区间内仅 1 次记录，画不出趋势
      </div>
      {#if stats}
        <div class="num mt-1 text-center text-[11px] text-[var(--color-ink-500)]/80">
          {stats.latestDate} · {fmt(stats.latest)}{unit ? ' ' + unit : ''}
        </div>
      {/if}
      <!-- 单次档同样信息不足。用户手动关掉回溯后常常就停在这一档，
           这里不再给入口的话，「关掉」就成了单向操作，再也回不去 -->
      {#if onbackfill && !backfill}
        <div class="mt-1.5 text-center">
          <button
            class="rounded border border-[var(--color-line)] px-2 py-0.5 text-[11px] text-[var(--color-brand)] transition-colors hover:bg-[var(--color-line)]/50"
            onclick={runInner(onbackfill)}>显示最近 6 次</button
          >
        </div>
      {/if}
    </div>
  {:else if tier === 'empty'}
    <!-- 空态三分支：成因不同，下一步也完全不同，混成一句「无记录」用户无从判断 -->
    <div class="mt-2 rounded-md border border-dashed border-[var(--color-line)] px-3 py-4">
      {#if emptyKind === 'never'}
        <div class="text-center text-[12px] text-[var(--color-ink-500)]">该指标尚未同步过数据</div>
        <div class="mt-1 text-center text-[11px]">
          <a
            class="text-[var(--color-brand)] hover:underline"
            href={resolve('/sync')}
            onclick={(e) => e.stopPropagation()}>去数据源与同步页接入</a
          >
        </div>
      {:else if emptyKind === 'pending'}
        <div class="text-center text-[12px] text-[var(--color-ink-500)]">今日数据尚未同步</div>
        <div class="mt-1 text-center text-[11px] text-[var(--color-ink-500)]/70">
          下次同步后自动出现，无需处理
        </div>
      {:else}
        <div class="text-center text-[12px] text-[var(--color-ink-500)]">近 {days} 天无记录</div>
        {#if lastText}
          <div class="num mt-1 text-center text-[11px] text-[var(--color-ink-500)]/70">
            上次测量 {lastText}
          </div>
        {/if}
        {#if onbackfill}
          <div class="mt-1.5 text-center">
            <button
              class="rounded border border-[var(--color-line)] px-2 py-0.5 text-[11px] text-[var(--color-brand)] transition-colors hover:bg-[var(--color-line)]/50"
              onclick={runInner(onbackfill)}>显示最近 6 次</button
            >
          </div>
        {/if}
      {/if}
    </div>
  {:else}
    <div class="relative mt-2">
      <div
        bind:this={el}
        style="height:{chartHeight}px;width:100%;"
        class={loading ? 'invisible' : ''}
      ></div>
      {#if loading}
        <div class="absolute inset-0 flex items-center justify-center">
          <div class="h-[110px] w-full animate-pulse rounded bg-[var(--color-line)]/50"></div>
        </div>
      {/if}
    </div>
  {/if}

  <!-- 密度条：只给稀疏档。日更指标画它是 365 根刻度挤成的实线，没有信息量 -->
  {#if strip.length > 0 && (tier === 'step' || tier === 'single' || tier === 'empty')}
    <div class="mt-2">
      <DensityStrip dates={strip} {color} height={12} />
      <div class="num mt-0.5 text-[10px] text-[var(--color-ink-500)]/70">
        近一年 {strip.length} 次{cadence ? ` · ${cadence}` : ''}
      </div>
    </div>
  {/if}

  <!-- 卡脚：统计量随档位切换 -->
  {#if stats}
    <div class="mt-2 border-t border-[var(--color-line)] pt-2">
      {#if tier === 'continuous' || tier === 'points'}
        <div class="num flex items-center justify-between text-[11px] text-[var(--color-ink-500)]">
          <span>均 <b class="font-medium text-[var(--color-ink-700)]">{fmt(stats.avg)}</b></span>
          <span>高 <b class="font-medium text-[var(--color-ink-700)]">{fmt(stats.max)}</b></span>
          <span>低 <b class="font-medium text-[var(--color-ink-700)]">{fmt(stats.min)}</b></span>
        </div>
      {:else if tier === 'step'}
        <!-- 3 个点的「最高/最低」是噪声；稀疏指标要看的是本次与上次差多少、隔了多久 -->
        <div class="num flex items-center justify-between text-[11px] text-[var(--color-ink-500)]">
          <span
            >上次 <b class="font-medium text-[var(--color-ink-700)]">{fmt(stats.prev ?? 0)}</b
            ></span
          >
          <span
            >变化
            <b class="font-medium text-[var(--color-ink-700)]"
              >{stats.delta !== null ? fmt(stats.delta) : '—'}</b
            ></span
          >
          <span
            >间隔 <b class="font-medium text-[var(--color-ink-700)]">{stats.gap ?? '—'} 天</b></span
          >
        </div>
      {:else}
        <div class="num text-[11px] text-[var(--color-ink-500)]">
          记录于 {stats.latestDate}
        </div>
      {/if}
      <!-- 口径说明 + 陈旧提示 -->
      <div class="mt-1 flex items-center gap-2">
        <div
          class="min-w-0 flex-1 truncate text-[10px] text-[var(--color-ink-500)]/80"
          title={aggNote}
        >
          {aggNote}{grain !== 'day' && tier === 'continuous'
            ? ` · ${grain === 'week' ? '按周' : '按月'}${dayAgg === 'sum' ? '日均' : '均值'}`
            : ''}
        </div>
        {#if staleText}
          <span
            class="shrink-0 rounded px-1.5 py-0.5 text-[10px] {staleStrong
              ? 'bg-[var(--color-warn)]/15 text-[var(--color-warn)]'
              : 'bg-[var(--color-line)]/60 text-[var(--color-ink-500)]'}"
            title="该指标久未更新，活跃度低，排序时自动后移">{staleText}</span
          >
        {/if}
      </div>
    </div>
  {/if}
</div>
