<script lang="ts">
  import * as echarts from 'echarts';
  import { api, type WorkoutRow, type WorkoutRoute, type WorkoutSeries } from '#lib/api';
  import { dataColor, palette } from '#lib/theme.svelte';
  import { formatDuration } from '#lib/prefs.svelte';
  import RouteMap from './RouteMap.svelte';

  /** 一次运动的详情：摘要 + 轨迹 + 逐点心率。 */
  type Props = {
    workout: WorkoutRow;
    onclose: () => void;
  };

  let { workout, onclose }: Props = $props();

  let route = $state<WorkoutRoute | null>(null);
  let series = $state<WorkoutSeries | null>(null);
  let loading = $state(true);
  let error = $state('');

  // 逐点心率：图表容器只在数据到位后才存在，所以由 action 负责建与销毁
  let hrSecs = $derived(series?.hr_secs ?? []);
  let hrBpm = $derived(series?.hr_bpm ?? []);

  async function load(id: number) {
    loading = true;
    error = '';
    try {
      // 两个请求并发；轨迹缺失是 404→null，不算错误
      const [r, s] = await Promise.all([api.workoutRoute(id), api.workoutSeries(id)]);
      route = r;
      series = s;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    void load(workout.id);
  });

  function fmt(v: number | undefined | null, digits = 0): string {
    return v == null || !Number.isFinite(v) ? '—' : v.toFixed(digits);
  }

  /** 相对运动起点的钟点偏移，与轨迹图同一把时轴。 */
  function clock(sec: number): string {
    const m = Math.round(sec / 60);
    return `${Math.floor(m / 60)}:${String(Math.abs(m % 60)).padStart(2, '0')}`;
  }

  function buildOption(): echarts.EChartsOption {
    const p = palette();
    const data = hrSecs.map((s, i) => [s / 60, hrBpm[i]] as [number, number]);
    return {
      animation: false,
      backgroundColor: 'transparent',
      grid: { left: 44, right: 14, top: 14, bottom: 26 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: p.surface,
        borderColor: p.line,
        textStyle: { color: p.ink, fontSize: 12 },
        formatter: (raw: unknown) => {
          const arr = raw as { data?: [number, number] }[];
          const d = arr?.[0]?.data;
          if (!d) return '';
          return `开始后 ${clock(d[0] * 60)} · ${Math.round(d[1])} bpm`;
        }
      },
      xAxis: {
        type: 'value',
        axisLabel: {
          color: p.inkSoft,
          fontSize: 11,
          formatter: (v: number) => `${Math.round(v)} 分`
        },
        axisLine: { lineStyle: { color: p.line } },
        splitLine: { show: false }
      },
      yAxis: {
        type: 'value',
        scale: true,
        axisLabel: { color: p.inkSoft, fontSize: 11 },
        splitLine: { lineStyle: { color: p.split } }
      },
      series: [
        {
          type: 'line',
          data,
          showSymbol: false,
          sampling: 'lttb',
          lineStyle: { width: 1.6, color: dataColor('#993556') },
          areaStyle: { opacity: 0.08, color: dataColor('#993556') }
        }
      ]
    };
  }

  function mountChart(node: HTMLDivElement) {
    const c = echarts.init(node, undefined, { renderer: 'canvas' });
    c.setOption(buildOption());
    const onResize = () => c.resize();
    window.addEventListener('resize', onResize);
    return {
      destroy() {
        window.removeEventListener('resize', onResize);
        c.dispose();
      }
    };
  }

  let stats = $derived([
    { label: '时长', value: formatDuration(workout.duration_min) },
    {
      label: '主动消耗',
      value:
        workout.active_energy != null
          ? `${Math.round(workout.active_energy)} ${workout.active_energy_unit ?? 'kcal'}`
          : workout.total_energy != null
            ? `${Math.round(workout.total_energy)} kcal（总）`
            : '—'
    },
    {
      label: '距离',
      value: workout.total_distance ? `${workout.total_distance.toFixed(2)} km` : '—'
    },
    { label: '平均心率', value: fmt(workout.avg_hr) },
    { label: '最大心率', value: fmt(workout.max_hr) },
    { label: '体力消耗评分', value: fmt(workout.effort_score) }
  ]);
</script>

<div class="card overflow-hidden">
  <div
    class="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--color-line)] px-4 py-2.5"
  >
    <div class="flex items-baseline gap-2">
      <span class="text-[13px] font-medium">{workout.name}</span>
      <span class="num text-[12px] text-[var(--color-ink-500)]">{workout.local_date}</span>
      <span class="text-[12px] text-[var(--color-ink-500)]">{workout.source}</span>
    </div>
    <button
      class="rounded-md border border-[var(--color-line)] px-2 py-1 text-[12px] text-[var(--color-ink-700)] hover:bg-[var(--color-surface-2)]"
      onclick={onclose}>收起</button
    >
  </div>

  <div class="grid grid-cols-2 gap-x-4 gap-y-2 px-4 py-3 text-[12px] sm:grid-cols-3 lg:grid-cols-6">
    {#each stats as s (s.label)}
      <div>
        <div class="text-[var(--color-ink-500)]">{s.label}</div>
        <div class="num mt-0.5 text-[14px]">{s.value}</div>
      </div>
    {/each}
  </div>

  {#if error}
    <div
      class="border-t border-[var(--color-line)] px-4 py-3 text-[13px] text-[var(--color-danger)]"
    >
      {error}
    </div>
  {:else if loading}
    <div
      class="border-t border-[var(--color-line)] px-4 py-6 text-[13px] text-[var(--color-ink-500)]"
    >
      读取轨迹与心率…
    </div>
  {:else}
    {#if route}
      <RouteMap {route} />
    {:else}
      <div
        class="border-t border-[var(--color-line)] px-4 py-3 text-[12px] text-[var(--color-ink-500)]"
      >
        这次运动没有轨迹。室内运动本就不定位；户外运动则要在 HAE 导出时勾选路线（JSON 里的
        <code class="num">route</code> 会随摄入一起入库），或用 <code
          class="num">zhichangd import-routes &lt;GPX 目录&gt;</code
        > 从本机补那份 GPX。
      </div>
    {/if}
    {#if hrBpm.length > 1}
      <div class="card mt-3 overflow-hidden">
        <div class="border-b border-[var(--color-line)] px-4 py-2.5 text-[13px] font-medium">
          逐点心率
          <span class="num ml-1 text-[12px] font-normal text-[var(--color-ink-500)]">
            {hrBpm.length} 点
          </span>
        </div>
        <div use:mountChart style="height:200px"></div>
      </div>
    {/if}
  {/if}
</div>
