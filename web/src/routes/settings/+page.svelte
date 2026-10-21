<script lang="ts">
  import { resolve } from '$app/paths';
  import { onMount } from 'svelte';
  import {
    api,
    shiftDays,
    todayStr,
    type AppSetting,
    type GoalRow,
    type MetricDef
  } from '#lib/api';
  import { unitDigits } from '#lib/metric-groups';
  import { minutesToClock } from '#lib/sleep-score';
  import {
    formatDuration,
    formatDurationHours,
    prefs,
    resetLocalPrefs,
    type DurationFormat
  } from '#lib/prefs.svelte';
  import { theme, type ThemePreference } from '#lib/theme.svelte';
  import Segmented from '#lib/components/Segmented.svelte';
  import Card from '#lib/components/ui/Card.svelte';
  import Button from '#lib/components/ui/Button.svelte';
  import CardHeader from '#lib/components/ui/CardHeader.svelte';
  import ErrorBanner from '#lib/components/ui/ErrorBanner.svelte';
  import PageHeader from '#lib/components/ui/PageHeader.svelte';
  import InfoNote from '#lib/components/ui/InfoNote.svelte';

  // 设置页分两类落点：口味存本机 localStorage，目标存服务端 goals / app_settings 表。
  // 之所以要分：睡眠目标是数据口径，换台设备也该是同一个目标；而暗不暗、区间默认多长
  // 只影响这台设备怎么看，没必要为了它把写接口敞开给一个没有登录态的服务。

  const TOKEN_KEY = 'zhichang.settings-token';
  const BEDTIME_KEY = 'bedtime_target';
  const SLEEP_METRIC = 'sleep_analysis';

  /** 没有目标时输入框里给的参考值——只是个提示，不写进去就不算设过 */
  const DURATION_HINT = 7.5;

  let defs = $state<MetricDef[]>([]);
  let goals = $state<GoalRow[]>([]);
  let settings = $state<AppSetting[]>([]);
  let tzName = $state('');
  let loading = $state(true);
  let error = $state('');

  let token = $state('');
  let durDraft = $state('');
  let bedDraft = $state('');
  let busy = $state('');
  let msg = $state('');

  let exportDays = $state(90);
  let exportBusy = $state(false);
  let exportMsg = $state('');

  let resetArmed = $state(false);

  let sleepGoal = $derived(goals.find((g) => g.metric === SLEEP_METRIC) ?? null);
  let bedtime = $derived(settings.find((s) => s.key === BEDTIME_KEY)?.value ?? '');
  let enabledCount = $derived(defs.filter((d) => d.enabled).length);

  // 预期入睡时间换算成「相对 18:00 的分钟数」给睡眠页当基准；空值不给
  let bedtimeRel = $derived.by(() => {
    if (!bedtime) return null;
    const [h, m] = bedtime.split(':').map(Number);
    if (!Number.isFinite(h) || !Number.isFinite(m)) return null;
    return (((h * 60 + m - 1080) % 1440) + 1440) % 1440;
  });

  function loadToken() {
    try {
      token = sessionStorage.getItem(TOKEN_KEY) ?? '';
    } catch {
      token = '';
    }
  }

  function keepToken() {
    try {
      sessionStorage.setItem(TOKEN_KEY, token.trim());
    } catch {
      /* 隐私模式下写不进会话存储，不影响本次保存 */
    }
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const [metrics, gl, st] = await Promise.all([api.metrics(), api.goals(), api.settings()]);
      defs = metrics.metrics;
      tzName = metrics.timezone;
      goals = gl;
      settings = st;
      durDraft = sleepGoal ? String(sleepGoal.target) : '';
      bedDraft = bedtime;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  /** 一次只让一个写操作在跑，避免两个按钮同时打同一个令牌 */
  async function write(label: string, fn: (t: string) => Promise<unknown>) {
    const t = token.trim();
    if (!t) {
      msg = `${label}需要设置令牌：服务端用它区分「看」和「改」`;
      return;
    }
    busy = label;
    msg = '';
    try {
      await fn(t);
      keepToken();
    } catch (e) {
      msg = e instanceof Error ? e.message : String(e);
    } finally {
      busy = '';
    }
  }

  function saveDuration() {
    const v = Number(durDraft);
    if (!Number.isFinite(v) || v <= 0 || v > 24) {
      msg = '睡眠时长目标要在 0~24 小时之间';
      return;
    }
    void write('睡眠时长目标', async (t) => {
      goals = await api.setGoal(SLEEP_METRIC, Math.round(v * 100) / 100, t);
    });
  }

  function clearDuration() {
    void write('清除时长目标', async (t) => {
      goals = await api.deleteGoal(SLEEP_METRIC, t);
    });
  }

  function saveBedtime() {
    if (!/^\d{2}:\d{2}$/.test(bedDraft)) {
      msg = '入睡时间要写成 HH:MM，比如 22:30';
      return;
    }
    void write('预期入睡时间', async (t) => {
      settings = await api.setSetting(BEDTIME_KEY, bedDraft, t);
    });
  }

  function clearBedtime() {
    void write('清除入睡时间', async (t) => {
      settings = await api.deleteSetting(BEDTIME_KEY, t);
    });
  }

  async function exportCsv() {
    exportBusy = true;
    exportMsg = '';
    try {
      const to = todayStr();
      const from = shiftDays(-(exportDays - 1), to);
      const list = defs.filter((d) => d.enabled).map((d) => d.metric);
      const { series } = await api.daily(list, from, to);

      // 宽表：一行一天，列是启用中的指标。天级值本来就是按本地日收敛过的，
      // 直接摊平即可，不需要在前端再拼一次跨来源口径。
      const dates = new Set<string>();
      for (const pts of Object.values(series)) for (const p of pts) dates.add(p.date);
      const head = ['日期', ...list.map((m) => csvCell(headerOf(m)))];
      const lines = [head.join(',')];
      for (const date of [...dates].sort()) {
        const row = [date];
        for (const m of list) {
          const p = series[m]?.find((x) => x.date === date);
          row.push(
            p == null
              ? ''
              : String(
                  Number(p.value.toFixed(unitDigits(defs.find((d) => d.metric === m)?.unit ?? '')))
                )
          );
        }
        lines.push(row.join(','));
      }
      downloadCsv(`知常-天级数据-${from}至${to}.csv`, '﻿' + lines.join('\r\n') + '\r\n');
      exportMsg =
        dates.size > 0
          ? `已导出 ${dates.size} 天 × ${list.length} 项`
          : '该区间没有天级数据，导出的是只有表头的空表';
    } catch (e) {
      exportMsg = e instanceof Error ? e.message : String(e);
    } finally {
      exportBusy = false;
    }
  }

  function headerOf(metric: string): string {
    const d = defs.find((x) => x.metric === metric);
    return `${d?.display_name_zh ?? metric}（${d?.preferred_unit ?? ''}）`;
  }

  /** Excel 会把裸逗号行里的引号与换行当结构看，所以字段一律带引号、内部引号翻倍 */
  function csvCell(s: string): string {
    return `"${s.replace(/"/g, '""')}"`;
  }

  function downloadCsv(filename: string, text: string) {
    const url = URL.createObjectURL(new Blob([text], { type: 'text/csv;charset=utf-8' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    a.click();
    URL.revokeObjectURL(url);
  }

  function doReset() {
    // 先把主题写回 system，再整批抹掉：反过来的话 set 会把刚删掉的键又写回去，
    // 界面上看是「重置了」，localStorage 里却还留着一条
    theme.preference = 'system';
    resetLocalPrefs();
    exportMsg = '';
    msg = '';
    resetArmed = false;
  }

  onMount(() => {
    loadToken();
    load();
  });
</script>

<svelte:head>
  <title>设置 · 知常</title>
</svelte:head>
<PageHeader class="mb-1" noteClass="mb-4 text-[11px]">
  设置
  {#snippet note()}
    显示相关的偏好存在本机浏览器，换设备要重设；睡眠目标存在服务端，各端一致。
  {/snippet}
</PageHeader>

<ErrorBanner {error} class="mb-3" />

<!-- 1. 外观 -->
<Card class="mb-3">
  <CardHeader class="mb-2.5">外观</CardHeader>
  <div class="flex flex-wrap items-center gap-3">
    <span class="text-[12px] text-[var(--color-ink-500)]">显示模式</span>
    <Segmented
      value={theme.preference}
      options={[
        { label: '明亮', value: 'light' as ThemePreference },
        { label: '暗黑', value: 'dark' as ThemePreference },
        { label: '跟随系统', value: 'system' as ThemePreference }
      ]}
      onchange={(v) => (theme.preference = v)}
    />
    {#if theme.preference === 'system'}
      <span class="text-[11px] text-[var(--color-ink-400)]">
        当前{theme.mode === 'dark' ? '暗黑' : '明亮'}，随系统外观实时切换
      </span>
    {/if}
  </div>
  <InfoNote class="mt-2">图表、分期色与档位色会一起调整亮度，色相与相互次序保持不变。</InfoNote>
</Card>

<!-- 2. 睡眠目标 -->
<Card class="mb-3">
  <div class="mb-2.5 flex flex-wrap items-baseline justify-between gap-2">
    <span class="text-[13px] font-medium">睡眠目标</span>
    <span class="text-[11px] text-[var(--color-ink-500)]">
      {token.trim() ? '本次会话已授权，可直接改' : '未授权：目标只读'}
    </span>
  </div>

  <div class="grid gap-3 lg:grid-cols-2">
    <div class="flex flex-wrap items-baseline gap-2">
      <span class="w-24 shrink-0 text-[12px] text-[var(--color-ink-500)]">睡眠时长</span>
      <input
        class="num w-20 rounded-md border border-[var(--color-line)] px-2 py-1 text-[12px]"
        type="number"
        step="0.5"
        min="0.5"
        max="24"
        placeholder={String(DURATION_HINT)}
        bind:value={durDraft}
      />
      <span class="text-[12px] text-[var(--color-ink-500)]">小时 / 晚</span>
      <Button
        variant="brand"
        busy={busy === '睡眠时长目标'}
        busyText="保存中…"
        disabled={busy !== ''}
        onclick={saveDuration}>保存</Button
      >
      {#if sleepGoal}
        <Button variant="ghost" disabled={busy !== ''} onclick={clearDuration}>清除</Button>
      {/if}
    </div>

    <div class="flex flex-wrap items-baseline gap-2">
      <span class="w-24 shrink-0 text-[12px] text-[var(--color-ink-500)]">预期入睡</span>
      <input
        class="num w-24 rounded-md border border-[var(--color-line)] px-2 py-1 text-[12px]"
        type="time"
        placeholder="22:30"
        bind:value={bedDraft}
      />
      <Button
        variant="brand"
        busy={busy === '预期入睡时间'}
        busyText="保存中…"
        disabled={busy !== ''}
        onclick={saveBedtime}>保存</Button
      >
      {#if bedtime}
        <Button variant="ghost" disabled={busy !== ''} onclick={clearBedtime}>清除</Button>
      {/if}
    </div>
  </div>

  <InfoNote class="mt-2">
    {#if sleepGoal}
      当前时长目标 {formatDurationHours(sleepGoal.target)} ·
    {:else}
      未设时长目标 ·
    {/if}
    {#if bedtimeRel != null}
      当前入睡目标 {minutesToClock(bedtimeRel)}，睡眠评分的「就寝」项按它打分
    {:else}
      未设入睡目标，「就寝」项沿用最近 13 晚入睡中位数（Apple 口径）
    {/if}
  </InfoNote>

  <div class="mt-3 flex flex-wrap items-baseline gap-2">
    <label class="flex items-baseline gap-1.5 text-[12px] text-[var(--color-ink-500)]">
      设置令牌
      <input
        class="w-56 rounded-md border border-[var(--color-line)] px-2 py-1 text-[12px]"
        type="password"
        placeholder="ZHICHANG_SETTINGS_TOKEN"
        bind:value={token}
      />
    </label>
    <InfoNote>令牌由服务端环境变量配置，只留在本次标签页内；未配置时保存会提示只读。</InfoNote>
  </div>

  <ErrorBanner error={msg} bare size="sm" class="mt-2" />
</Card>

<!-- 3. 数据展示 -->
<Card class="mb-3">
  <CardHeader class="mb-2.5">数据展示</CardHeader>

  <div class="mb-3 flex flex-wrap items-center gap-3">
    <span class="w-24 shrink-0 text-[12px] text-[var(--color-ink-500)]">默认区间</span>
    <Segmented
      value={prefs.rangeDays}
      options={[
        { label: '7 天', value: 7 },
        { label: '30 天', value: 30 },
        { label: '90 天', value: 90 },
        { label: '365 天', value: 365 }
      ]}
      onchange={(v) => (prefs.rangeDays = v)}
    />
    <span class="text-[11px] text-[var(--color-ink-400)]"
      >趋势、睡眠、运动、心电图页首次打开时用它</span
    >
  </div>

  <div class="flex flex-wrap items-center gap-3">
    <span class="w-24 shrink-0 text-[12px] text-[var(--color-ink-500)]">时长格式</span>
    <Segmented
      value={prefs.durationFormat}
      options={[
        { label: '小时 + 分', value: 'cn' as DurationFormat },
        { label: '小数小时', value: 'decimal' as DurationFormat },
        { label: 'h / m', value: 'clock' as DurationFormat }
      ]}
      onchange={(v) => (prefs.durationFormat = v)}
    />
    <span class="num text-[12px] text-[var(--color-ink-700)]">
      预览 {formatDuration(552)}
    </span>
    <span class="text-[11px] text-[var(--color-ink-400)]"
      >同一份设置会用在睡眠、运动与概览的时长上</span
    >
  </div>

  <InfoNote class="mt-2">
    数值精度跟随指标单位，不提供开关：同一个指标在前后端只能用一套小数位。
    概览的关注指标、趋势的叠加指标仍在各自页面就地选择。
  </InfoNote>
</Card>

<!-- 4. 数据与隐私 -->
<Card class="mb-3">
  <CardHeader class="mb-2.5">数据与隐私</CardHeader>

  <div class="mb-3 flex flex-wrap items-center gap-3">
    <span class="w-24 shrink-0 text-[12px] text-[var(--color-ink-500)]">导出天级数据</span>
    <Segmented
      value={exportDays}
      options={[
        { label: '30 天', value: 30 },
        { label: '90 天', value: 90 },
        { label: '365 天', value: 365 }
      ]}
      onchange={(v) => (exportDays = v)}
    />
    <Button
      variant="brand"
      busy={exportBusy}
      busyText="导出中…"
      disabled={exportBusy || defs.length === 0}
      onclick={exportCsv}>下载 CSV</Button
    >
    <span class="text-[11px] text-[var(--color-ink-400)]">一行一天，只读接口，不需要令牌</span>
  </div>
  {#if exportMsg}<div class="mb-3 text-[11px] text-[var(--color-ink-500)]">{exportMsg}</div>{/if}

  <div class="flex flex-wrap items-center gap-3">
    <span class="w-24 shrink-0 text-[12px] text-[var(--color-ink-500)]">本机偏好</span>
    <Button
      variant={resetArmed ? 'danger' : 'outline'}
      onclick={() => (resetArmed ? doReset() : (resetArmed = true))}
    >
      {resetArmed ? '确认恢复默认？' : '恢复默认'}
    </Button>
    <span class="text-[11px] text-[var(--color-ink-400)]">
      {resetArmed
        ? '只清显示偏好（外观、区间、时长格式、已选指标），服务端目标不受影响'
        : '再点一次才生效'}
    </span>
  </div>

  <InfoNote class="mt-2">
    原始健康数据由手机自动同步写入，本页不提供删除：删了没法重新生成，
    误删的代价远高于一个按钮带来的方便。
  </InfoNote>
</Card>

<!-- 5. 关于 -->
<Card>
  <CardHeader class="mb-2.5">关于</CardHeader>
  {#if loading}
    <div class="text-[12px] text-[var(--color-ink-500)]">加载中…</div>
  {:else}
    <dl class="grid gap-x-6 gap-y-2 text-[12px] sm:grid-cols-2 lg:grid-cols-3">
      <div class="flex justify-between gap-3 border-b border-[var(--color-line)] pb-1.5">
        <dt class="text-[var(--color-ink-500)]">本机时区</dt>
        <dd class="num">{tzName || '—'}</dd>
      </div>
      <div class="flex justify-between gap-3 border-b border-[var(--color-line)] pb-1.5">
        <dt class="text-[var(--color-ink-500)]">启用指标</dt>
        <dd class="num">{enabledCount} 项</dd>
      </div>
      <div class="flex justify-between gap-3 border-b border-[var(--color-line)] pb-1.5">
        <dt class="text-[var(--color-ink-500)]">服务端目标</dt>
        <dd class="num">{goals.length} 项</dd>
      </div>
    </dl>
    <InfoNote class="mt-2">
      指标口径、跨来源收敛与同步水位在<a
        href={resolve('/sync')}
        class="text-[var(--color-brand)] hover:underline">数据源页</a
      >看； 时区与接入令牌属于部署配置，由环境变量决定，不在本页改。
    </InfoNote>
  {/if}
</Card>
