<script lang="ts">
	import { onMount } from 'svelte';
	import { api, shiftDays, todayStr, type SleepRow, type SleepStages } from '#lib/api';
	import {
		COMPONENT_MAX,
		bandOf,
		collapseNights,
		computeSleepScores,
		minutesToClock,
		type SleepScore
	} from '#lib/sleep-score';
	import { STAGE_META, stageMeta } from '#lib/sleep-stages';
	import {
		computeRhythm,
		efficiencyBand,
		summarizeStructure,
		wakeClock,
		type RhythmNight
	} from '#lib/sleep-rhythm';
	import RangePicker from '#lib/components/RangePicker.svelte';
	import TrendChart from '#lib/components/TrendChart.svelte';
	import SleepTimeline from '#lib/components/SleepTimeline.svelte';
	import SleepRhythmChart from '#lib/components/SleepRhythmChart.svelte';

	/** 评分要用你自己的历史做基准（时长 30 晚、就寝 13 晚），故额外多取一段前置窗口 */
	const LEAD_IN_DAYS = 45;

	let days = $state(30);
	let allRows = $state<SleepRow[]>([]);
	let loading = $state(true);
	let error = $state('');

	let from = $derived(shiftDays(-days + 1, todayStr()));
	/** 展示范围：前置窗口只用来算基准，不进图表与表格 */
	let rows = $derived(allRows.filter((r) => r.local_date >= from));

	let nights = $derived(collapseNights(allRows));
	let scores = $derived(computeSleepScores(nights).filter((s) => s.local_date >= from));
	/** 有分数的夜晚（范围最前面几天可能因历史不足没有分数） */
	let scored = $derived(scores.filter((s): s is SleepScore & { score: number } => s.score !== null));
	let latest = $derived(scored.length ? scored[scored.length - 1] : null);
	let avgScore = $derived(
		scored.length ? scored.reduce((a, s) => a + s.score, 0) / scored.length : null
	);

	let stageSeries = $derived([
		{ name: '深睡', type: 'bar' as const, stack: 'sleep', color: '#0c447c', points: rows.map((r) => ({ date: r.local_date, value: r.deep_hr ?? 0 })) },
		{ name: '核心', type: 'bar' as const, stack: 'sleep', color: '#378add', points: rows.map((r) => ({ date: r.local_date, value: r.core_hr ?? 0 })) },
		{ name: 'REM', type: 'bar' as const, stack: 'sleep', color: '#85b7eb', points: rows.map((r) => ({ date: r.local_date, value: r.rem_hr ?? 0 })) }
	]);
	let totalSeries = $derived([
		{ name: '总睡眠', points: rows.map((r) => ({ date: r.local_date, value: r.total_sleep_hr ?? 0 })) }
	]);
	let scoreSeries = $derived([
		{
			name: '睡眠评分',
			color: '#5a6b8c',
			unit: '分',
			digits: 0,
			points: scored.map((s) => ({ date: s.local_date, value: s.score }))
		}
	]);
	let avg = $derived(
		rows.length
			? rows.reduce((s, r) => s + (r.total_sleep_hr ?? 0), 0) / rows.length
			: null
	);

	/** 三项分解：把评分拆开，让「为什么是这个分」可见 */
	let components = $derived.by(() => {
		const s = latest;
		if (!s) return [];
		return [
			{
				key: 'duration',
				label: '睡眠时长',
				value: s.duration ?? 0,
				max: COMPONENT_MAX.duration,
				color: '#5a6b8c',
				note: `睡 ${fmtH(s.sleepHr)} · 基准 ${fmtH(s.baselineHr)}`
			},
			{
				key: 'bedtime',
				label: '就寝规律性',
				value: s.bedtime ?? 0,
				max: COMPONENT_MAX.bedtime,
				color: '#7a6a9c',
				note:
					s.bedtimeMin == null || s.bedtimeBaselineMin == null
						? '无入睡时刻'
						: `${minutesToClock(s.bedtimeMin)} 入睡 · 习惯 ${minutesToClock(s.bedtimeBaselineMin)}（差 ${s.bedtimeDeviationMin} 分钟）`
			},
			{
				key: 'interruption',
				label: '中断',
				value: s.interruption ?? 0,
				max: COMPONENT_MAX.interruption,
				color: '#3f7d6b',
				note: interruptionNote(s)
			}
		];
	});

	function interruptionNote(s: SleepScore): string {
		if (s.awakeHr == null) return '无清醒数据，按满分计';
		const mins = `${Math.round(s.awakeHr * 60)} 分钟`;
		const head = s.awakeCount != null ? `醒来 ${s.awakeCount} 次 · 共 ${mins}` : `清醒 ${mins}`;
		return s.interruptionSource === 'in-bed-diff' ? `${head}（在床 − 睡着推算）` : head;
	}

	function fmtH(v: number | null | undefined): string {
		return v == null ? '—' : `${v.toFixed(1)} h`;
	}
	function cell(v: number | null | undefined, d = 1): string {
		return v == null ? '—' : v.toFixed(d);
	}

	// —— 睡眠分期时间线（hypnogram）——
	// 聚合图只能说「这一晚深睡 1.5 小时」，时间线才看得出「深睡都堆在前半夜」、
	// 「后半夜 REM 变密」这类结构信息。
	let stages = $state<SleepStages>({ segments: [], totals: {} });
	let stageLoading = $state(true);
	let stageError = $state('');
	/** 当前查看的睡眠日；留空表示由后端挑最近一晚有分期的日期 */
	let stageDate = $state('');

	/** 可选夜晚：范围内有记录的睡眠日，倒序（新的在前） */
	let nightChoices = $derived([...rows].reverse().map((r) => r.local_date));
	let stageSegs = $derived(stages.segments.filter((s) => stageMeta(s.stage).drawn));
	let hasTimeline = $derived(stageSegs.length > 0);

	/** 夜间窗口：从第一段起、到最后一段止（后端已按入睡/起床时刻裁剪） */
	let winBounds = $derived.by(() => {
		const segs = stages.segments;
		if (segs.length === 0) return { lo: 0, hi: 24 * 60 };
		return {
			lo: Math.min(...segs.map((s) => s.local_min)),
			hi: Math.max(...segs.map((s) => s.local_min + s.duration_min))
		};
	});

	/** 图例 + 各阶段合计（只列出实际出现的阶段） */
	let stageLegend = $derived(
		STAGE_META.filter((m) => m.drawn && (stages.totals[m.key] ?? 0) > 0).map((m) => ({
			...m,
			hours: stages.totals[m.key] ?? 0
		}))
	);
	let stageTotalHr = $derived(stageLegend.reduce((a, s) => a + s.hours, 0));

	async function loadStages(date: string) {
		stageLoading = true;
		try {
			stages = await api.sleepStages(date || undefined);
			stageError = '';
			// 后端在 date 缺省时会自己挑最近一晚，回填以便日期步进器与标题一致
			if (!date && stages.local_date) stageDate = stages.local_date;
		} catch (e) {
			stageError = e instanceof Error ? e.message : String(e);
		} finally {
			stageLoading = false;
		}
	}

	/** 日期步进：切到相邻的「有记录的夜晚」 */
	function stepNight(dir: 1 | -1) {
		const list = nightChoices;
		if (list.length === 0) return;
		const cur = stageDate || list[0];
		const idx = list.indexOf(cur);
		const next = list[idx === -1 ? 0 : Math.min(list.length - 1, Math.max(0, idx + dir))];
		if (next && next !== stageDate) loadStages(next);
	}

	function fmtDur(min: number): string {
		const m = Math.round(min);
		if (m < 60) return `${m} 分钟`;
		const h = Math.floor(m / 60);
		const r = m % 60;
		return r === 0 ? `${h} 小时` : `${h} 小时 ${r} 分`;
	}

	// —— 周期性节律 & 睡眠结构 ——
	// 全部基于已透出的字段计算，不新增后端接口。
	let rhythm = $derived(computeRhythm(rows as RhythmNight[]));
	let rhythmLanes = $derived(rhythm.byWeekday.filter((w) => w.nights > 0 && w.bedtimeMin !== null));
	let structure = $derived(summarizeStructure(rows as RhythmNight[]));
	let effBand = $derived(efficiencyBand(structure.efficiency));

	/** 就寝时刻（相对 18:00 的分钟）→ 钟点文案 */
	function bedClock(rel: number | null): string {
		if (rel === null) return '—';
		const m = (((Math.round(rel) % 1440) + 1440) % 1440 + 1080) % 1440;
		return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
	}

	function pct(v: number | null): string {
		return v === null ? '—' : `${(v * 100).toFixed(1)}%`;
	}

	/** 社交时差的人话描述 */
	function jetlagText(): string {
		const v = rhythm.socialJetlagMin;
		if (v === null) return '样本不足';
		if (Math.abs(v) < 20) return '周末与工作日基本一致';
		return v > 0 ? `周末比工作日晚 ${fmtDur(v)}` : `周末比工作日早 ${fmtDur(-v)}`;
	}

	/** 规律性的人话描述 */
	function spreadText(): string {
		const v = rhythm.bedtimeSpreadMin;
		if (v === null) return '—';
		if (v <= 20) return '很规律';
		if (v <= 40) return '比较规律';
		if (v <= 75) return '有波动';
		return '很不规律';
	}

	/**
	 * 结构比：三个阶段共用一把尺子（都映射到 0~SCALE_MAX%）才能横向比较。
	 * 之前按各自倍数缩放会让「核心 58%」顶满整条，看着像异常，其实很正常。
	 * 灰色竖线是文献里的典型值，用来一眼看出「我比典型高还是低」。
	 */
	const STRUCTURE_SCALE_MAX = 0.6;
	let structureRows = $derived.by(() => {
		const items = [
			{ key: 'deep', label: '深睡', color: '#274a7d', v: structure.deepRatio, typical: 0.2 },
			{ key: 'core', label: '核心', color: '#3f7fc4', v: structure.coreRatio, typical: 0.5 },
			{ key: 'rem', label: 'REM', color: '#7cc3e8', v: structure.remRatio, typical: 0.22 }
		];
		return items.map((it) => {
			// 浮点噪声别当成差异：差 0.1 个百分点不算「高于典型」
			const diff = it.v === null ? null : it.v - it.typical;
			const note =
				diff === null || Math.abs(diff) < 0.02
					? ''
					: diff > 0
						? '· 高于典型'
						: '· 低于典型';
			return {
				...it,
				note,
				pctText: pct(it.v),
				width: it.v === null ? 0 : Math.min(100, (it.v / STRUCTURE_SCALE_MAX) * 100),
				tickPos: (it.typical / STRUCTURE_SCALE_MAX) * 100
			};
		});
	});

	async function load() {
		loading = true;
		try {
			const to = todayStr();
			// 多取一段前置窗口：评分基准要用到当前范围之前的历史
			allRows = (await api.sleep(shiftDays(-(days + LEAD_IN_DAYS), to), to)).rows;
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
		// 时间线跟着范围走：先试当前选中的日期，越界则让后端挑最近一晚
		const inRange = stageDate && stageDate >= from;
		await loadStages(inRange ? stageDate : '');
	}

	onMount(load);
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-baseline justify-between gap-2">
		<h1 class="text-[15px] font-medium">睡眠</h1>
		<RangePicker value={days} onchange={(d) => ((days = d), load())} />
	</div>

	{#if error}
		<div class="card px-4 py-3 text-[13px] text-[#a32d2d]">{error}</div>
	{/if}

	<!-- 睡眠评分：按 Apple 公开的三项权重复刻（时长 50 + 就寝 30 + 中断 20） -->
	<div class="card p-4">
		{#if loading}
			<div class="text-[12px] text-[var(--color-ink-500)]">睡眠评分</div>
			<div class="mt-1.5 h-[32px] w-24 animate-pulse rounded bg-[var(--color-line)]"></div>
		{:else if latest && latest.score !== null}
			{@const band = bandOf(latest.score)}
			<div class="flex flex-wrap items-start justify-between gap-3">
				<div>
					<div class="text-[12px] text-[var(--color-ink-500)]">睡眠评分 · {latest.local_date}</div>
					<div class="mt-0.5 flex items-baseline gap-2">
						<span class="num text-[32px] leading-none font-medium">{latest.score}</span>
						<span
							class="rounded px-1.5 py-0.5 text-[11px]"
							style="background:{band.color}1a;color:{band.color}">{band.label}</span
						>
						<span class="text-[11px] text-[var(--color-ink-500)]">/ 100</span>
					</div>
				</div>
				<p class="max-w-[300px] text-[10px] leading-relaxed text-[var(--color-ink-500)]/80">
					<span class="block"
						>自算口径：睡眠时长 50 + 就寝规律性 30 + 中断 20；基准取自你自己的历史（时长 30 晚、就寝 13 晚）。</span
					>
					<span class="block">Apple 未开放评分接口，本分值与 iPhone 上显示的数字不保证一致。</span>
				</p>
			</div>

			<div class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
				{#each components as c (c.key)}
					<div>
						<div class="flex items-baseline justify-between gap-2 text-[11px]">
							<span class="text-[var(--color-ink-700)]">{c.label}</span>
							<span class="num shrink-0 text-[var(--color-ink-500)]"
								>{Math.round(c.value)} <span class="opacity-60">/ {c.max}</span></span
							>
						</div>
						<div class="mt-1 h-1.5 overflow-hidden rounded-full bg-[var(--color-line)]/60">
							<div
								class="h-full rounded-full"
								style="width:{Math.max(0, Math.min(100, (c.value / c.max) * 100))}%;background:{c.color}"
							></div>
						</div>
						<div class="mt-1 text-[10px] text-[var(--color-ink-500)]">{c.note}</div>
					</div>
				{/each}
			</div>
		{:else}
			<div class="text-[12px] text-[var(--color-ink-500)]">睡眠评分</div>
			<div class="mt-1 text-[13px]">历史不足，暂时无法评分</div>
			<div class="mt-1 text-[11px] text-[var(--color-ink-500)]/80">
				评分要与自己的历史比较，至少需要 5 晚带入睡时刻的记录
			</div>
		{/if}
	</div>

	<div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">平均睡眠评分</div>
			<div class="num mt-1 text-[20px] font-medium">
				{avgScore === null ? '—' : Math.round(avgScore)}
			</div>
		</div>
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">平均总睡眠</div>
			<div class="num mt-1 text-[20px] font-medium">
				{avg === null ? '—' : `${avg.toFixed(1)} h`}
			</div>
		</div>
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">记录天数</div>
			<div class="num mt-1 text-[20px] font-medium">{rows.length}</div>
		</div>
	</div>

	<div class="card p-4">
		<div class="mb-2 text-[13px] font-medium">评分趋势</div>
		{#if loading}
			<div class="flex h-[220px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">加载中…</div>
		{:else if scored.length === 0}
			<div class="flex h-[220px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">
				暂无评分数据
			</div>
		{:else}
			<TrendChart series={scoreSeries} height={220} yUnit="分" />
		{/if}
	</div>

	<!-- 周期性节律：工作日 vs 周末，睡眠结构：效率与各阶段占比 -->
	<div class="grid grid-cols-1 items-start gap-3 lg:grid-cols-2">
		<div class="card p-4">
			<div class="mb-2 text-[13px] font-medium">睡眠效率与结构</div>
			{#if loading}
				<div class="flex h-[200px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">
					加载中…
				</div>
			{:else if structure.sleepHr === null}
				<div class="py-10 text-center text-[13px] text-[var(--color-ink-500)]">暂无睡眠结构数据</div>
			{:else}
				<div class="flex items-baseline justify-between gap-2">
					<div class="flex items-baseline gap-2">
						<span class="num text-[28px] leading-none font-medium">{pct(structure.efficiency)}</span>
						{#if effBand}
							<span
								class="rounded px-1.5 py-0.5 text-[11px]"
								style="background:{effBand.color}1a;color:{effBand.color}">{effBand.label}</span
							>
						{/if}
					</div>
					<span class="text-[10px] text-[var(--color-ink-500)]/80">
						在床时长里真正睡着的比例 · 基于 {structure.samples.efficiency} 晚
					</span>
				</div>

				<div class="mt-3 space-y-2.5">
					{#each structureRows as s (s.key)}
						<div>
							<div class="flex items-baseline justify-between gap-2 text-[11px]">
								<span class="text-[var(--color-ink-700)]">
									{s.label}
									{#if s.note}<span class="text-[var(--color-ink-500)]/70">{s.note}</span>{/if}
								</span>
								<span class="num text-[var(--color-ink-500)]">{s.pctText}</span>
							</div>
							<div class="relative mt-1 h-1.5 rounded-full bg-[var(--color-line)]/60">
								<div class="h-full rounded-full" style="width:{s.width}%;background:{s.color}"></div>
								<!-- 典型值参考线 -->
								<span
									class="absolute top-1/2 h-2.5 w-px -translate-y-1/2 bg-[#9aa0a6]"
									style="left:{s.tickPos}%"
								></span>
							</div>
						</div>
					{/each}
				</div>
				<div class="mt-3 text-[10px] leading-relaxed text-[var(--color-ink-500)]/80">
					三条共用 0–60% 的尺子，可直接横向比较；灰色竖线为典型值（深睡 20% · 核心 50% · REM 22%）。
				</div>
			{/if}
		</div>

		<div class="card p-4">
			<div class="mb-2 text-[13px] font-medium">周期性节律</div>
			{#if loading}
				<div class="flex h-[200px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">
					加载中…
				</div>
			{:else if rhythmLanes.length === 0}
				<div class="py-10 text-center text-[13px] text-[var(--color-ink-500)]">暂无就寝时刻记录</div>
			{:else}
				<div class="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-[var(--color-ink-500)]">
					<span>
						工作日就寝 <span class="num text-[var(--color-ink-700)]">{bedClock(rhythm.weekdayBedtimeMin)}</span>
					</span>
					<span>
						周末就寝 <span class="num text-[var(--color-ink-700)]">{bedClock(rhythm.weekendBedtimeMin)}</span>
					</span>
					<span>社交时差 {jetlagText()}</span>
					<span>规律性 {spreadText()}</span>
				</div>
				<div class="mt-1">
					<SleepRhythmChart byWeekday={rhythm.byWeekday} height={Math.max(200, rhythmLanes.length * 30 + 60)} />
				</div>
				<div class="mt-1 text-[10px] leading-relaxed text-[var(--color-ink-500)]/80">
					每个星期几的就寝与起床时刻中位数，灰色区间为睡眠时长。悬停查看各晚离散度。
				</div>
			{/if}
		</div>
	</div>

	<!-- 分期时间线：看清每一段的时序结构，聚合柱状图看不到这个 -->
	<div class="card p-4">
		<div class="mb-2 flex flex-wrap items-center justify-between gap-2">
			<div class="flex items-baseline gap-2">
				<span class="text-[13px] font-medium">睡眠分期时间线</span>
				{#if stages.local_date}
					<span class="num text-[11px] text-[var(--color-ink-500)]">{stages.local_date}</span>
				{/if}
			</div>
			{#if nightChoices.length > 1 && hasTimeline}
				<div class="flex items-center gap-1">
					<button
						class="rounded border border-[var(--color-line)] px-2 py-0.5 text-[11px] text-[var(--color-ink-700)] disabled:opacity-40"
						disabled={stageLoading || nightChoices.indexOf(stageDate || nightChoices[0]) <= 0}
						onclick={() => stepNight(-1)}>← 前一晚</button
					>
					<button
						class="rounded border border-[var(--color-line)] px-2 py-0.5 text-[11px] text-[var(--color-ink-700)] disabled:opacity-40"
						disabled={stageLoading
							|| nightChoices.indexOf(stageDate || nightChoices[0]) >= nightChoices.length - 1}
						onclick={() => stepNight(1)}>后一晚 →</button
					>
				</div>
			{/if}
		</div>

		{#if stageError}
			<div class="text-[12px] text-[#a32d2d]">{stageError}</div>
		{:else if stageLoading}
			<div class="flex h-[240px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">
				加载中…
			</div>
		{:else if !hasTimeline}
			<div class="flex flex-col items-center justify-center gap-1 py-10 text-center">
				<div class="text-[13px] text-[var(--color-ink-700)]">暂无分期数据</div>
				<div class="max-w-[340px] text-[11px] leading-relaxed text-[var(--color-ink-500)]">
					这一晚只有睡眠总时长等聚合值，没有逐段的深睡 / 核心 / REM 记录。<br />
					需要导出端在 <span class="num">sleep_analysis</span> 里带上分段数据。
				</div>
			</div>
		{:else}
			<SleepTimeline
				segments={stages.segments}
				winStart={winBounds.lo}
				winEnd={winBounds.hi}
				height={240}
			/>
			<div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-[var(--color-ink-500)]">
				{#each stageLegend as s (s.key)}
					<span class="inline-flex items-center gap-1.5">
						<span class="inline-block h-2 w-2 rounded-[2px]" style="background:{s.color}"></span>
						{s.label} <span class="num">{fmtDur(s.hours * 60)}</span>
					</span>
				{/each}
				<span class="ml-auto">
					分期合计 <span class="num">{stageTotalHr.toFixed(1)} h</span>
					{#if stages.source}· 来源 {stages.source}{/if}
				</span>
			</div>
		{/if}
	</div>

	<div class="card p-4">
		<div class="mb-2 text-[13px] font-medium">睡眠分期</div>
		{#if loading}
			<div class="flex h-[260px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">加载中…</div>
		{:else}
			<TrendChart series={stageSeries} height={260} unit="h" digits={1} />
		{/if}
	</div>

	<div class="card p-4">
		<div class="mb-2 text-[13px] font-medium">总睡眠时长</div>
		{#if !loading}
			<TrendChart series={totalSeries} height={220} unit="h" digits={1} />
		{/if}
	</div>

	<div class="card overflow-hidden">
		<div class="overflow-x-auto">
			<table class="w-full min-w-[620px] text-[13px]">
				<thead class="bg-[#fafbfc] text-[12px] text-[var(--color-ink-500)]">
					<tr>
						<th class="px-4 py-2 text-left font-normal">日期</th>
						<th class="px-4 py-2 text-right font-normal">总睡眠 (h)</th>
						<th class="px-4 py-2 text-right font-normal">深睡</th>
						<th class="px-4 py-2 text-right font-normal">核心</th>
						<th class="px-4 py-2 text-right font-normal">REM</th>
						<th class="px-4 py-2 text-right font-normal">清醒</th>
						<th class="px-4 py-2 text-right font-normal">在床</th>
						<th class="px-4 py-2 text-left font-normal">来源</th>
					</tr>
				</thead>
				<tbody>
					{#each [...rows].reverse() as r (r.local_date + r.source)}
						<tr class="border-t border-[var(--color-line)]">
							<td class="num px-4 py-2">{r.local_date}</td>
							<td class="num px-4 py-2 text-right">{(r.total_sleep_hr ?? 0).toFixed(1)}</td>
							<td class="num px-4 py-2 text-right">{(r.deep_hr ?? 0).toFixed(1)}</td>
							<td class="num px-4 py-2 text-right">{(r.core_hr ?? 0).toFixed(1)}</td>
							<td class="num px-4 py-2 text-right">{(r.rem_hr ?? 0).toFixed(1)}</td>
							<td class="num px-4 py-2 text-right">{cell(r.awake_hr)}</td>
							<td class="num px-4 py-2 text-right">{(r.in_bed_hr ?? 0).toFixed(1)}</td>
							<td class="px-4 py-2">{r.source}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</div>
</div>
