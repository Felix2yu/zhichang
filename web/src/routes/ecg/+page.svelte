<script lang="ts">
	import { onMount } from 'svelte';
	import { api, shiftDays, todayStr, type EcgRecord, type EcgWaveform } from '#lib/api';
	import RangePicker from '#lib/components/RangePicker.svelte';
	import EcgStripChart from '#lib/components/EcgStripChart.svelte';

	let days = $state(90);
	let records = $state<EcgRecord[]>([]);
	let loading = $state(true);
	let error = $state('');

	// 波形按需加载：一次测量 15360 个点（约 120KB），
	// 列表页不该为了「可能有人点」就把每一次的波形都拉下来。
	let selected = $state<EcgRecord | null>(null);
	let waveform = $state<EcgWaveform | null>(null);
	let waveLoading = $state(false);
	let waveError = $state('');

	let latest = $derived(records[0] ?? null);
	let avgHr = $derived.by(() => {
		const vals = records.map((r) => r.average_heart_rate).filter((v): v is number => typeof v === 'number');
		return vals.length ? vals.reduce((a, b) => a + b, 0) / vals.length : null;
	});
	// 判定分布：按归一后的措辞统计，而不是上游原文——
	// 原文随手机语言变化，拿它分组会把同一类拆成两组。
	let byLabel = $derived.by(() => {
		const m = new Map<string, number>();
		for (const r of records) {
			const k = r.classification_label || '（未给判定）';
			m.set(k, (m.get(k) ?? 0) + 1);
		}
		return [...m.entries()].sort((a, b) => b[1] - a[1]);
	});

	async function load() {
		loading = true;
		try {
			const to = todayStr();
			const r = await api.ecg(shiftDays(-days + 1, to), to);
			records = r.records;
			error = '';
			await select(records[0] ?? null);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function select(rec: EcgRecord | null) {
		selected = rec;
		waveform = null;
		waveError = '';
		if (!rec) return;
		waveLoading = true;
		try {
			waveform = await api.ecgWaveform(rec.id);
		} catch (e) {
			waveError = e instanceof Error ? e.message : String(e);
		} finally {
			waveLoading = false;
		}
	}

	onMount(load);

	// 时刻按「这条记录自带的偏移」还原，不跟浏览器时区走——
	// 与睡眠页同一约定（sleep-night-metrics.ts 的 nightMinClock）。
	function fmtTime(iso: string, tzOffsetMin: number): string {
		const t = new Date(iso);
		if (Number.isNaN(t.getTime())) return '—';
		const d = new Date(t.getTime() + tzOffsetMin * 60_000);
		const p = (n: number) => String(n).padStart(2, '0');
		return `${d.getUTCFullYear()}-${p(d.getUTCMonth() + 1)}-${p(d.getUTCDate())} ${p(d.getUTCHours())}:${p(d.getUTCMinutes())}`;
	}

	let waveStats = $derived.by(() => {
		const v = waveform?.voltages ?? [];
		if (v.length === 0) return null;
		let min = v[0];
		let max = v[0];
		for (const x of v) {
			if (x < min) min = x;
			if (x > max) max = x;
		}
		return { min, max, peakToPeak: max - min };
	});
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-baseline justify-between gap-2">
		<h1 class="text-[15px] font-medium">心电图</h1>
		<RangePicker value={days} onchange={(d) => ((days = d), load())} />
	</div>

	{#if error}
		<div class="card px-4 py-3 text-[13px] text-[#a32d2d]">{error}</div>
	{/if}

	<div class="grid grid-cols-3 gap-3">
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">记录数</div>
			<div class="num mt-1 text-[20px] font-medium">{records.length}</div>
		</div>
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">最近一次</div>
			<div class="mt-1 text-[20px] font-medium">
				{latest ? latest.classification_label || '（未给判定）' : '—'}
			</div>
			{#if latest}
				<div class="num text-[12px] text-[var(--color-ink-500)]">{fmtTime(latest.start, latest.tz_offset_min)}</div>
			{/if}
		</div>
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">平均心率</div>
			<div class="num mt-1 text-[20px] font-medium">
				{avgHr === null ? '—' : `${Math.round(avgHr)} bpm`}
			</div>
			<div class="text-[12px] text-[var(--color-ink-500)]">各次测量的均值</div>
		</div>
	</div>

	{#if byLabel.length > 0}
		<div class="card flex flex-wrap items-baseline gap-x-5 gap-y-1 px-4 py-3 text-[13px]">
			<span class="font-medium">判定分布</span>
			{#each byLabel as [label, n] (label)}
				<span class="text-[var(--color-ink-700)]">
					{label}
					<span class="num text-[var(--color-ink-500)]">{n} 次</span>
				</span>
			{/each}
		</div>
	{/if}

	<div class="card overflow-hidden">
		<table class="w-full text-[13px]">
			<thead class="bg-[#fafbfc] text-[12px] text-[var(--color-ink-500)]">
				<tr>
					<th class="px-4 py-2 text-left font-normal">测量时刻</th>
					<th class="px-4 py-2 text-left font-normal">判定</th>
					<th class="px-4 py-2 text-right font-normal">平均心率</th>
					<th class="px-4 py-2 text-right font-normal">采样率</th>
					<th class="px-4 py-2 text-right font-normal">点数</th>
					<th class="px-4 py-2 text-left font-normal">来源</th>
				</tr>
			</thead>
			<tbody>
				{#each records as r (r.id)}
					<tr
						class="cursor-pointer border-t border-[var(--color-line)] transition-colors {selected?.id === r.id
							? 'bg-[var(--color-brand-soft)]/60'
							: 'hover:bg-[#fafbfc]'}"
						onclick={() => select(r)}
					>
						<td class="num px-4 py-2">{fmtTime(r.start, r.tz_offset_min)}</td>
						<td class="px-4 py-2">
							<!-- 归一没认出来的判定照原样展示，并标注它的来路，别让人误以为是我们的措辞 -->
							<span class={r.classification_known ? '' : 'text-[var(--color-ink-500)]'}>
								{r.classification_label || '（未给判定）'}
							</span>
							{#if !r.classification_known && r.classification}
								<span class="text-[11px] text-[var(--color-ink-500)]"> · 上游原文</span>
							{/if}
						</td>
						<td class="num px-4 py-2 text-right">
							{r.average_heart_rate === undefined ? '—' : `${Math.round(r.average_heart_rate)} bpm`}
						</td>
						<td class="num px-4 py-2 text-right">
							{r.sampling_frequency ? `${r.sampling_frequency} Hz` : '—'}
						</td>
						<td class="num px-4 py-2 text-right">{r.voltage_count}</td>
						<td class="px-4 py-2">{r.source}</td>
					</tr>
				{:else}
					<tr>
						<td colspan="6" class="px-4 py-6 text-center text-[var(--color-ink-500)]">
							{loading ? '加载中…' : '暂无心电记录'}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>

	{#if selected}
		<div class="card p-4">
			<div class="mb-1 flex flex-wrap items-baseline justify-between gap-2">
				<div class="text-[13px] font-medium">
					波形
					<span class="num text-[12px] font-normal text-[var(--color-ink-500)]">
						{fmtTime(selected.start, selected.tz_offset_min)} · {selected.classification_label || '（未给判定）'}
					</span>
				</div>
				{#if waveform && waveStats}
					<div class="num flex flex-wrap gap-x-4 text-[12px] text-[var(--color-ink-500)]">
						<span>{waveform.voltages.length} 点</span>
						<span>{waveform.duration_sec.toFixed(1)} 秒</span>
						{#if waveform.sampling_frequency}
							<span>{waveform.sampling_frequency} Hz</span>
						{/if}
						{#if waveform.unit}
							<span>单位 {waveform.unit}</span>
						{/if}
						<span>峰-峰 {waveStats.peakToPeak.toFixed(1)}</span>
						<span>最低 {waveStats.min.toFixed(1)} / 最高 {waveStats.max.toFixed(1)}</span>
					</div>
				{/if}
			</div>
			{#if waveLoading}
				<div class="py-8 text-center text-[13px] text-[var(--color-ink-500)]">波形加载中…</div>
			{:else if waveError}
				<div class="py-3 text-[13px] text-[#a32d2d]">{waveError}</div>
			{:else if waveform && waveform.voltages.length > 0}
				<EcgStripChart
					voltages={waveform.voltages}
					unit={waveform.unit}
					samplingFrequency={waveform.sampling_frequency ?? null}
				/>
			{:else}
				<div class="py-8 text-center text-[13px] text-[var(--color-ink-500)]">
					这条记录没有波形数据（本次导出只带了判定与心率）
				</div>
			{/if}
		</div>
	{/if}
</div>
