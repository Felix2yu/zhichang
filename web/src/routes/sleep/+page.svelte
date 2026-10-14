<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import {
		api,
		shiftDays,
		todayStr,
		type NightMetricRow,
		type NightStages,
		type SleepRow,
		type SleepStages
	} from '#lib/api';
	import {
		COMPONENT_MAX,
		bandOf,
		collapseNights,
		computeSleepScores,
		bedtimeMinutes,
		minutesToClock,
		type SleepScore
	} from '#lib/sleep-score';
	import { STAGE_META, stageMeta } from '#lib/sleep-stages';
	import { GRAIN_LABEL, bucketMean, grainFor } from '#lib/sleep-buckets';
	import {
		DEFAULT_NIGHT_METRIC,
		availableNightMetrics,
		indexNightMetrics,
		nightKey,
		nightMeta,
		nightMinClock,
		seriesFor
	} from '#lib/sleep-night-metrics';
	import {
		computeRhythm,
		coverageStats,
		efficiencyBand,
		summarizeStructure,
		wakeClock,
		type RhythmNight
	} from '#lib/sleep-rhythm';
	import RangePicker from '#lib/components/RangePicker.svelte';
	import TrendChart from '#lib/components/TrendChart.svelte';
	import SleepTimeline from '#lib/components/SleepTimeline.svelte';
	import SleepScheduleChart from '#lib/components/SleepScheduleChart.svelte';
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

	// —— 夜间生理指标（按入睡时间窗关联，非 local_date）——
	let nightRows = $state<NightMetricRow[]>([]);
	let nightIdx = $derived(indexNightMetrics(nightRows));
	/** 图表横轴的分桶粒度：范围越长，一根柱代表越多夜晚 */
	let grain = $derived(grainFor(days));
	let dates = $derived(rows.map((r) => r.local_date));
	/** 每晚一行的心率聚合；没有样本的夜晚是 null */
	let nightHr = $derived(rows.map((r) => nightIdx.get(nightKey(DEFAULT_NIGHT_METRIC, r.local_date)) ?? null));
	let hasNightHr = $derived(nightHr.some((r) => r !== null));
	let nightHrSeries = $derived([
		{
			name: '夜间最低',
			color: '#993556',
			unit: 'bpm',
			digits: 0,
			points: bucketMean(
				dates,
				nightHr.map((r) => r?.min ?? null),
				grain
			)
		},
		{
			name: '夜间平均',
			color: '#5a6b8c',
			unit: 'bpm',
			digits: 0,
			points: bucketMean(
				dates,
				nightHr.map((r) => r?.avg ?? null),
				grain
			)
		}
	]);
	/** 有分数的夜晚（范围最前面几天可能因历史不足没有分数） */
	let scored = $derived(scores.filter((s): s is SleepScore & { score: number } => s.score !== null));

	// —— 睡眠时间表矩阵 ——
	let scheduleRows = $state<NightStages[]>([]);
	/** 矩阵最多画最近多少晚：再多就看不出单晚了 */
	const SCHEDULE_MAX_NIGHTS = 90;
	let schedule = $derived.by(() => {
		const byDate = new Map(scheduleRows.map((n) => [n.local_date, n.segments]));
		// 终点取「最后一个有睡眠记录的夜晚」，不然今天的空行会被读成断档
		const last = rows.length ? rows[rows.length - 1].local_date : '';
		const all: { date: string; segments: NightStages['segments'] }[] = [];
		if (last) {
			for (let d = from; d <= last; d = shiftDays(1, d)) {
				all.push({ date: d, segments: byDate.get(d) ?? [] });
			}
		}
		return {
			rows: all.slice(-SCHEDULE_MAX_NIGHTS),
			hidden: Math.max(0, all.length - SCHEDULE_MAX_NIGHTS)
		};
	});
	let avgScore = $derived(
		scored.length ? scored.reduce((a, s) => a + s.score, 0) / scored.length : null
	);

	let stageSeries = $derived([
		{ name: '深睡', type: 'bar' as const, stack: 'sleep', color: '#0c447c', points: bucketMean(dates, rows.map((r) => r.deep_hr), grain) },
		{ name: '核心', type: 'bar' as const, stack: 'sleep', color: '#378add', points: bucketMean(dates, rows.map((r) => r.core_hr), grain) },
		{ name: 'REM', type: 'bar' as const, stack: 'sleep', color: '#85b7eb', points: bucketMean(dates, rows.map((r) => r.rem_hr), grain) }
	]);
	let totalSeries = $derived([
		{ name: '总睡眠', points: bucketMean(dates, rows.map((r) => r.total_sleep_hr), grain) }
	]);
	let scoreSeries = $derived([
		{
			name: '睡眠评分',
			color: '#5a6b8c',
			unit: '分',
			digits: 0,
			points: bucketMean(
				scores.map((s) => s.local_date),
				scores.map((s) => s.score),
				grain
			)
		}
	]);
	let avg = $derived(
		rows.length
			? rows.reduce((s, r) => s + (r.total_sleep_hr ?? 0), 0) / rows.length
			: null
	);

	/** 三项分解：把评分拆开，让「为什么是这个分」可见（跟着当前查看的夜晚） */
	let components = $derived.by(() => {
		const s = nightScore;
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
	let stages = $state<SleepStages>({ segments: [], totals: {}, physiology: [] });
	let stageLoading = $state(true);
	let stageError = $state('');
	/**
	 * 当前查看的睡眠日：整屏的「详情主键」。
	 * 评分卡、夜间心率卡、分期时间线都跟着它走，不然翻到 10-08 那晚，
	 * 上面的数字还是最新一晚的。
	 */
	let night = $state('');
	/** 时间线下方叠加哪个夜间指标（只有一项时不出现切换条） */
	let phyMetric = $state(DEFAULT_NIGHT_METRIC);

	/** 这一晚的会话 / 评分 / 夜间心率：详情视图的三个数据源 */
	let nightRow = $derived(rows.find((r) => r.local_date === night) ?? null);
	let nightScore = $derived(scores.find((s) => s.local_date === night) ?? null);
	let nightHrRow = $derived(nightIdx.get(nightKey(DEFAULT_NIGHT_METRIC, night)) ?? null);

	/**
	 * 这一晚的时刻概览：入睡 / 起床 / 睡眠中点 / 入睡后清醒。
	 * 时刻都用「相对 18:00 的分钟」表达，跨零点自然顺延（23:30→330、07:00→780）。
	 */
	let nightClockLine = $derived.by(() => {
		const r = nightRow;
		if (!r) return [] as { k: string; v: string; hint?: string }[];
		const tz = r.tz_offset_min ?? 0;
		const bed = r.sleep_start ? bedtimeMinutes(r.sleep_start, tz) : null;
		const wake = r.sleep_end ? bedtimeMinutes(r.sleep_end, tz) : null;
		const items: { k: string; v: string; hint?: string }[] = [];
		if (bed !== null) items.push({ k: 'bed', v: `${minutesToClock(bed)} 入睡` });
		if (wake !== null) items.push({ k: 'wake', v: `${minutesToClock(wake)} 起床` });
		if (bed !== null && wake !== null && wake > bed) {
			items.push({
				k: 'mid',
				v: `中点 ${minutesToClock((bed + wake) / 2)}`,
				hint: '入睡与起床的正中间：中点越晚说明睡眠时段整体后移'
			});
		}
		if (r.waso_hr != null) {
			const mins = Math.round(r.waso_hr * 60);
			items.push({
				k: 'waso',
				v: `入睡后清醒 ${mins} 分${r.awake_count != null ? ` · ${r.awake_count} 次` : ''}`,
				hint: '只算睡着之后的清醒，不含躺下到睡着之间的那段'
			});
		} else if (r.awake_hr != null) {
			items.push({
				k: 'awake',
				v: `清醒 ${Math.round(r.awake_hr * 60)} 分`,
				hint: '导出未含分段数据，只能给整晚清醒合计（含入睡前）'
			});
		}
		return items;
	});

	/** URL 是这一屏的真相：?date= 看哪一晚、?days= 看多大范围，链接可直接分享 */
	function readUrl() {
		const q = page.url.searchParams;
		const d = Number(q.get('days'));
		if (d === 7 || d === 30 || d === 90 || d === 365) days = d;
		const date = q.get('date');
		if (date && /^\d{4}-\d{2}-\d{2}$/.test(date)) night = date;
	}

	/** URL 还没读进来之前不许回写，否则首帧会把 ?date= 深链当成脏参数抹掉 */
	let ready = $state(false);

	$effect(() => {
		if (!ready) return;
		const q = page.url.searchParams;
		if (q.get('date') === (night || null) && q.get('days') === String(days)) return;
		const u = new URL(page.url.href);
		if (night) u.searchParams.set('date', night);
		else u.searchParams.delete('date');
		u.searchParams.set('days', String(days));
		// shallow + replace：只改查询串，不触发导航、也不攒历史记录
		void goto(`${u.pathname}${u.search}`, { replace: true, shallow: true });
	});

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

	/** 这一晚真实有数据的夜间指标（决定要不要出切换条） */
	let phyOptions = $derived(availableNightMetrics(stages.physiology));
	/** 当前叠加项：选中的没了数据就退回第一个有数据的 */
	let phyActive = $derived(
		phyOptions.includes(phyMetric) ? phyMetric : (phyOptions[0] ?? DEFAULT_NIGHT_METRIC)
	);
	let phyOverlay = $derived(seriesFor(stages.physiology, phyActive));

	async function loadStages(date: string) {
		stageLoading = true;
		try {
			stages = await api.sleepStages(date || undefined);
			stageError = '';
			// 后端在 date 缺省时会自己挑最近一晚，回填以便标题与步进器一致
			if (!date && stages.local_date) night = stages.local_date;
		} catch (e) {
			stageError = e instanceof Error ? e.message : String(e);
		} finally {
			stageLoading = false;
		}
	}

	/** 切到某一晚：详情区（评分、夜间心率、时间线）整屏跟着走 */
	function selectNight(date: string) {
		if (!date || date === night) return;
		night = date;
		void loadStages(date);
	}

	/** 日期步进：切到相邻的「有记录的夜晚」 */
	function stepNight(dir: 1 | -1) {
		const list = nightChoices;
		if (list.length === 0) return;
		const cur = night || list[0];
		const idx = list.indexOf(cur);
		const next = list[idx === -1 ? 0 : Math.min(list.length - 1, Math.max(0, idx + dir))];
		selectNight(next);
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
	/** 记录覆盖：连续与断档（只看首末记录之间，今天的觉还没睡不算断档） */
	let coverage = $derived(coverageStats(rows.map((r) => r.local_date)));

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
			// 夜间指标只要展示范围，不需要前置基准，与图表同一区间才对得齐
			const [sl, nm, stg] = await Promise.all([
				api.sleep(shiftDays(-(days + LEAD_IN_DAYS), to), to),
				api.sleepNightMetrics(from, to),
				api.sleepStagesRange(from, to)
			]);
			allRows = sl.rows;
			nightRows = nm.rows;
			scheduleRows = stg.rows;
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
		// 整屏跟着这一晚走：URL 里带来的日期越界（换了范围）就回落到范围内最新一晚
		if (!night || night < from || !rows.some((r) => r.local_date === night)) {
			night = rows.length ? rows[rows.length - 1].local_date : '';
		}
		await loadStages(night);
	}

	onMount(() => {
		readUrl();
		ready = true;
		void load();
	});
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
		{:else if nightScore && nightScore.score !== null}
			{@const band = bandOf(nightScore.score)}
			<div class="flex flex-wrap items-start justify-between gap-3">
				<div>
					<div class="text-[12px] text-[var(--color-ink-500)]">睡眠评分 · {nightScore.local_date}</div>
					<div class="mt-0.5 flex items-baseline gap-2">
						<span class="num text-[32px] leading-none font-medium">{nightScore.score}</span>
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
			<div class="text-[12px] text-[var(--color-ink-500)]">睡眠评分{night ? ` · ${night}` : ''}</div>
			<div class="mt-1 text-[13px]">
				{scores.length ? '这一晚没有评分' : '历史不足，暂时无法评分'}
			</div>
			<div class="mt-1 text-[11px] text-[var(--color-ink-500)]/80">
				{scores.length
					? '评分要有入睡时刻、且基准够（时长 30 晚、就寝 13 晚），这一晚缺其中一项'
					: '评分要与自己的历史比较，至少需要 5 晚带入睡时刻的记录'}
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
			<div class="text-[12px] text-[var(--color-ink-500)]">平均入睡潜伏期</div>
			<div class="num mt-1 text-[20px] font-medium">
				{structure.latencyMin === null ? '—' : `${Math.round(structure.latencyMin)} 分`}
			</div>
			<div class="mt-0.5 text-[10px] text-[var(--color-ink-500)]/70">
				{#if structure.latencyMin === null}
					导出未含上床时刻
				{:else}
					上床到睡着 · 中位数 · 基于 {structure.samples.latency} 晚
				{/if}
			</div>
		</div>
		<div class="card px-4 py-3">
			<div class="text-[12px] text-[var(--color-ink-500)]">记录天数</div>
			<div class="num mt-1 text-[20px] font-medium">{rows.length}</div>
			<div class="mt-0.5 text-[10px] {coverage.missing.length > 0 ? 'text-[#8a6d1f]' : 'text-[var(--color-ink-500)]/70'}"
				title={coverage.missing.length > 0 ? `断档：${coverage.missing.join('、')}` : ''}>
				{#if coverage.current > 0}
					当前连续 {coverage.current} 晚 · 最长 {coverage.longest} 晚{coverage.missing.length > 0 ? ` · 断档 ${coverage.missing.length} 晚` : ''}
				{:else}
					暂无记录
				{/if}
			</div>
		</div>
	</div>

	<div class="card p-4">
		<div class="mb-2 flex flex-wrap items-baseline gap-2 text-[13px] font-medium">评分趋势{#if grain !== 'night'}<span class="text-[11px] font-normal text-[var(--color-ink-500)]">· {GRAIN_LABEL[grain]}</span>{/if}</div>
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

	<!-- 夜间心率：睡眠内部指标之外的生理交叉验证。整屏范围内一项样本都没有才不渲染 -->
	{#if hasNightHr}
		{@const meta = nightMeta(DEFAULT_NIGHT_METRIC)}
		<div class="card p-4">
			<div class="mb-2 flex flex-wrap items-baseline justify-between gap-2">
				<span class="text-[13px] font-medium">{meta.label}{night ? ` · ${night}` : ''}</span>
				{#if grain !== 'night'}
					<span class="text-[11px] text-[var(--color-ink-500)]">趋势 {GRAIN_LABEL[grain]}</span>
				{/if}
				{#if nightHrRow}
					<span class="text-[10px] text-[var(--color-ink-500)]/80">
						来源 {nightHrRow.source} · {nightHrRow.sample_count} 点
					</span>
				{/if}
			</div>
			{#if nightHrRow}
				<div class="flex flex-wrap items-baseline gap-x-6 gap-y-2">
					<span class="flex items-baseline gap-1.5">
						<span class="num text-[28px] leading-none font-medium" style="color:{meta.color}">
							{Math.round(nightHrRow.min)}
						</span>
						<span class="text-[11px] text-[var(--color-ink-500)]">{meta.unit} 最低</span>
						{#if nightMinClock(nightHrRow)}
							<span class="text-[11px] text-[var(--color-ink-500)]">
								· {nightMinClock(nightHrRow)} 出现
							</span>
						{/if}
					</span>
					<span class="flex items-baseline gap-1.5">
						<span class="num text-[20px] leading-none font-medium">
							{Math.round(nightHrRow.avg)}
						</span>
						<span class="text-[11px] text-[var(--color-ink-500)]">{meta.unit} 平均</span>
					</span>
					<span class="flex items-baseline gap-1.5">
						<span class="num text-[20px] leading-none font-medium">
							{Math.round(nightHrRow.min)}–{Math.round(nightHrRow.max)}
						</span>
						<span class="text-[11px] text-[var(--color-ink-500)]">区间</span>
					</span>
				</div>
			{:else}
				<div class="text-[12px] text-[var(--color-ink-500)]">
					这一晚的入睡窗口内没有心率样本（没戴手表，或导出未含该项）
				</div>
			{/if}
			<div class="mt-2">
				<TrendChart series={nightHrSeries} height={200} yUnit={meta.unit} />
			</div>
			<div class="mt-1 text-[10px] leading-relaxed text-[var(--color-ink-500)]/80">
				取入睡到起床窗口内的心率样本（跨零点的后半夜也算这一晚，不按记录日期）；
				「最低」是窗口内各段最小值里的最小，与 iPhone 睡眠详情页同口径，跟白天的静息心率不是一回事。
			</div>
		</div>
	{/if}

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

	<!-- 睡眠时间表：逐晚的分期铺在同一把「相对 18:00」的横轴上，就寝早晚一眼看出来 -->
	<div class="card p-4">
		<div class="mb-2 text-[13px] font-medium">睡眠时间表</div>
		{#if loading}
			<div class="flex h-[200px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">
				加载中…
			</div>
		{:else}
			<SleepScheduleChart nights={schedule.rows} onselect={selectNight} />
			<div class="mt-1 text-[10px] leading-relaxed text-[var(--color-ink-500)]/80">
				每行一晚（新夜在上），横轴是「相对 18:00 的本夜时间轴」，所以凌晨的时刻不会把行切断；
				留白 = 那一晚没有分段记录。点某行查看那一晚。
				{#if schedule.hidden > 0}当前只显示最近 {SCHEDULE_MAX_NIGHTS} 晚，另有 {schedule.hidden} 晚未画。{/if}
			</div>
		{/if}
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
						disabled={stageLoading || nightChoices.indexOf(night || nightChoices[0]) <= 0}
						onclick={() => stepNight(-1)}>← 前一晚</button
					>
					<button
						class="rounded border border-[var(--color-line)] px-2 py-0.5 text-[11px] text-[var(--color-ink-700)] disabled:opacity-40"
						disabled={stageLoading
							|| nightChoices.indexOf(night || nightChoices[0]) >= nightChoices.length - 1}
						onclick={() => stepNight(1)}>后一晚 →</button
					>
				</div>
			{/if}
		</div>

		{#if nightClockLine.length > 0}
			<div class="mb-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-[var(--color-ink-500)]">
				{#each nightClockLine as it (it.k)}
					<span {...(it.hint ? { title: it.hint } : {})}>{it.v}</span>
				{/each}
			</div>
		{/if}
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
			{#if phyOptions.length > 1}
				<div class="mb-1.5 flex flex-wrap items-center gap-x-1.5 gap-y-1">
					<span class="text-[11px] text-[var(--color-ink-500)]">叠加</span>
					{#each phyOptions as m (m)}
						<button
							class="rounded border px-2 py-0.5 text-[11px]"
							class:border-[var(--color-line)]={m !== phyActive}
							class:border-[#9aa0a6]={m === phyActive}
							class:bg-[#fafbfc]={m === phyActive}
							class:text-[var(--color-ink-700)]={m !== phyActive}
							class:font-medium={m === phyActive}
							onclick={() => (phyMetric = m)}>{nightMeta(m).label}</button
						>
					{/each}
				</div>
			{/if}
			<SleepTimeline
				segments={stages.segments}
				winStart={winBounds.lo}
				winEnd={winBounds.hi}
				height={phyOverlay ? 312 : 240}
				overlay={phyOverlay}
				overlayLabel={phyOverlay
					? `${nightMeta(phyOverlay.metric).label} ${nightMeta(phyOverlay.metric).unit}`
					: ''}
			/>
			<div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-[var(--color-ink-500)]">
				{#each stageLegend as s (s.key)}
					<span class="inline-flex items-center gap-1.5">
						<span class="inline-block h-2 w-2 rounded-[2px]" style="background:{s.color}"></span>
						{s.label} <span class="num">{fmtDur(s.hours * 60)}</span>
					</span>
				{/each}
				{#if phyOverlay}
					{@const m = nightMeta(phyOverlay.metric)}
					<span class="inline-flex items-center gap-1.5">
						<span class="inline-block h-2 w-2 rounded-full" style="background:#993556"></span>
						{m.label}
						<span class="num">
							{Math.round(phyOverlay.min)}–{Math.round(phyOverlay.max)} {m.unit}
						</span>
					</span>
				{/if}
				<span class="ml-auto">
					分期合计 <span class="num">{stageTotalHr.toFixed(1)} h</span>
					{#if stages.source}· 来源 {stages.source}{/if}
				</span>
			</div>
		{/if}
	</div>

	<div class="card p-4">
		<div class="mb-2 flex flex-wrap items-baseline gap-2 text-[13px] font-medium">睡眠分期{#if grain !== 'night'}<span class="text-[11px] font-normal text-[var(--color-ink-500)]">· {GRAIN_LABEL[grain]}</span>{/if}</div>
		{#if loading}
			<div class="flex h-[260px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">加载中…</div>
		{:else}
			<TrendChart series={stageSeries} height={260} unit="h" digits={1} />
		{/if}
	</div>

	<div class="card p-4">
		<div class="mb-2 flex flex-wrap items-baseline gap-2 text-[13px] font-medium">总睡眠时长{#if grain !== 'night'}<span class="text-[11px] font-normal text-[var(--color-ink-500)]">· {GRAIN_LABEL[grain]}</span>{/if}</div>
		{#if !loading}
			<TrendChart series={totalSeries} height={220} unit="h" digits={1} />
		{/if}
	</div>

	<div class="card overflow-hidden">
		<div class="overflow-x-auto">
			<table class="w-full text-[13px] {hasNightHr ? 'min-w-[700px]' : 'min-w-[620px]'}">
				<thead class="bg-[#fafbfc] text-[12px] text-[var(--color-ink-500)]">
					<tr>
						<th class="px-4 py-2 text-left font-normal">日期</th>
						<th class="px-4 py-2 text-right font-normal">总睡眠 (h)</th>
						<th class="px-4 py-2 text-right font-normal">深睡</th>
						<th class="px-4 py-2 text-right font-normal">核心</th>
						<th class="px-4 py-2 text-right font-normal">REM</th>
						<th class="px-4 py-2 text-right font-normal">清醒</th>
						<th class="px-4 py-2 text-right font-normal">在床</th>
						{#if hasNightHr}
							<th class="px-4 py-2 text-right font-normal">夜间最低 (bpm)</th>
						{/if}
						<th class="px-4 py-2 text-left font-normal">来源</th>
					</tr>
				</thead>
				<tbody>
					{#each [...rows].reverse() as r (r.local_date + r.source)}
						<tr
							class="cursor-pointer border-t border-[var(--color-line)] hover:bg-[#fafbfc]"
							class:bg-[#f2f6fb]={r.local_date === night}
							title="查看这一晚"
							onclick={() => selectNight(r.local_date)}
						>
							<td class="num px-4 py-2">{r.local_date}</td>
							<td class="num px-4 py-2 text-right">{(r.total_sleep_hr ?? 0).toFixed(1)}</td>
							<td class="num px-4 py-2 text-right">{(r.deep_hr ?? 0).toFixed(1)}</td>
							<td class="num px-4 py-2 text-right">{(r.core_hr ?? 0).toFixed(1)}</td>
							<td class="num px-4 py-2 text-right">{(r.rem_hr ?? 0).toFixed(1)}</td>
							<td class="num px-4 py-2 text-right">{cell(r.awake_hr)}</td>
							<td class="num px-4 py-2 text-right">{(r.in_bed_hr ?? 0).toFixed(1)}</td>
							{#if hasNightHr}
								{@const hr = nightIdx.get(nightKey(DEFAULT_NIGHT_METRIC, r.local_date))}
								<td class="num px-4 py-2 text-right">{hr ? Math.round(hr.min) : '—'}</td>
							{/if}
							<td class="px-4 py-2">{r.source}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</div>
</div>
