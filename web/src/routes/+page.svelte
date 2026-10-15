<script lang="ts">
	import { onMount } from 'svelte';
	import {
		api,
		formatValue,
		shiftDays,
		todayStr,
		type BaselineItem,
		type Coverage,
		type DailyPoint,
		type GoalRow,
		type InsightItem,
		type MetricDef,
		type NightMetricRow,
		type SleepRow,
		type SyncStatus,
		type WorkoutRow
	} from '#lib/api';
	import { METRIC_GROUPS, groupOf, unitDigits } from '#lib/metric-groups';
	import {
		FOCUS_STORE_KEY,
		TREND_STORE_KEY,
		formatDuration,
		formatDurationHours,
		prefs
	} from '#lib/prefs.svelte';
	import { dataColor } from '#lib/theme.svelte';
	import FreshnessBar from '#lib/components/FreshnessBar.svelte';
	import GoalProgress from '#lib/components/GoalProgress.svelte';
	import MetricCard from '#lib/components/MetricCard.svelte';
	import TrendChart from '#lib/components/TrendChart.svelte';

	// 概览页的定位：**先看数据到没到，再看今天偏离自己的常态多少，最后给深挖的入口**。
	//
	// 三个数据源的分工：
	//   - /api/baseline —— 「今日 vs 我的常态」，常态是近 30 天中位数（不含当日）；
	//   - /api/insights、/api/coverage —— 服务端算好的偏离提示与数据覆盖度，
	//     回答「值不值得点进去」和「这张卡为什么是空的」；
	//   - /api/sync/status —— 数据新鲜度，排在最上面，它是后面所有数字的可信前提。

	/** 默认关注的指标：挑长期有数据、且日常波动真能说明问题的四项。 */
	const DEFAULT_FOCUS = ['step_count', 'active_energy', 'resting_heart_rate', 'heart_rate_variability'];
	const BASELINE_WINDOW = 30;
	// 概览的小趋势窗口跟本机「默认区间」偏好走；BASELINE_WINDOW 不跟：
	// 30 天中位数是「常态」的定义本身，不是口味，跟着改会让人怀疑数字为什么变了。
	const TREND_DAYS = prefs.rangeDays;

	let defs = $state<MetricDef[]>([]);
	let focus = $state<string[]>([...DEFAULT_FOCUS]);
	let baselineItems = $state<BaselineItem[]>([]);
	let insights = $state<InsightItem[]>([]);
	let coverage = $state<Coverage>({ from: '', to: '', window_days: BASELINE_WINDOW, rows: [] });
	let goals = $state<GoalRow[]>([]);
	let sync = $state<SyncStatus | null>(null);
	let sleep = $state<SleepRow[]>([]);
	let nightHr = $state<NightMetricRow[]>([]);
	let workouts = $state<WorkoutRow[]>([]);
	let trendMetrics = $state<string[]>(['step_count', 'heart_rate']);
	let trendSeries = $state<Record<string, DailyPoint[]>>({});
	let loading = $state(true);
	let focusEditing = $state(false);
	let errors = $state<string[]>([]);

	let byName = $derived(new Map(defs.map((d) => [d.metric, d])));
	let baseByMetric = $derived(new Map(baselineItems.map((i) => [i.metric, i])));
	let coverageByMetric = $derived(new Map(coverage.rows.map((r) => [r.metric, r])));

	function loadFocus(list: MetricDef[]): string[] {
		try {
			const raw = localStorage.getItem(FOCUS_STORE_KEY);
			if (raw) {
				const known = new Set(list.map((d) => d.metric));
				const saved = JSON.parse(raw) as unknown;
				// 存的可能带着已被字典下线的指标（改过字典或版本升级），逐个过筛；
				// 全被筛掉就回落到默认四项——不能因为读档失败给用户一个空页面。
				if (Array.isArray(saved)) {
					const kept = saved.filter((m): m is string => typeof m === 'string' && known.has(m));
					if (kept.length > 0) return kept;
				}
			}
		} catch {
			/* 隐私模式下读不到，按默认四项走 */
		}
		return DEFAULT_FOCUS.filter((m) => list.some((d) => d.metric === m));
	}

	function saveFocus(next: string[]) {
		focus = next;
		try {
			localStorage.setItem(FOCUS_STORE_KEY, JSON.stringify(next));
		} catch {
			/* 存不下不影响本次会话 */
		}
	}

	// 关注的指标一律占位：本期没数据的（如这段时间没称体重）也要渲染成一张卡，
	// 而不是从网格里消失——消失会让后面几张卡整体前移，看起来像布局错位，
	// 也分不清「没测」和「我们没接这个指标」。字典里没有的才真的不占位。
	let cards = $derived(
		focus
			.filter((m) => byName.has(m))
			.map((m) => {
				const item = baseByMetric.get(m);
				const cov = coverageByMetric.get(m);
				const d = byName.get(m);
				return {
					metric: m,
					item,
					label: d?.display_name_zh ?? m,
					unit: d?.preferred_unit ?? '',
					digits: unitDigits(d?.preferred_unit ?? ''),
					// 空卡要解释原因：是「从没接过」还是「N 天没更新」，两者排查方向完全不同
					hint:
						item?.value != null
							? ''
							: cov
								? cov.days_since > 0
									? `${cov.days_since} 天前更新`
									: '近期无数据'
								: '未接到这个指标的数据'
				};
			})
	);

	// 卡片不是今天的数据时必须说清楚，否则用户会把三天前的数据当成今天的看
	let dataNote = $derived.by(() => {
		const today = todayStr();
		const dates = baselineItems.map((i) => i.date).filter((d) => d && d < today);
		if (dates.length === 0) return '';
		return `卡片显示的是 ${dates.sort().at(-1)} 的数据`;
	});

	let latestSleep = $derived(sleep.at(-1) ?? null);
	let night = $derived(nightHr.find((r) => r.local_date === latestSleep?.local_date) ?? null);

	// 分期占比：按深睡/核心/REM 占「睡着时长」的比例铺一条堆叠条。
	// 缺任一段就整条不画——半截的分期结构比没有更像数据出错。
	let sleepStructure = $derived.by(() => {
		const s = latestSleep;
		if (!s) return null;
		const parts = [
			{ key: 'deep', label: '深睡', color: dataColor('#274a7d'), hr: s.deep_hr },
			{ key: 'core', label: '核心', color: dataColor('#3f7fc4'), hr: s.core_hr },
			{ key: 'rem', label: 'REM', color: dataColor('#7cc3e8'), hr: s.rem_hr }
		];
		const missing = parts.some((p) => p.hr == null);
		const total = parts.reduce((a, p) => a + (p.hr ?? 0), 0);
		if (missing || total <= 0) return parts.map((p) => ({ ...p, width: 0, hr: p.hr ?? null }));
		return parts.map((p) => ({ ...p, width: ((p.hr ?? 0) / total) * 100, hr: p.hr ?? null }));
	});

	/** 用记录自带的时区偏移还原钟点：不这么算，跨零点入睡会显示成下午。 */
	function clockOf(iso: string | undefined, tzOffsetMin: number | undefined): string {
		if (!iso || tzOffsetMin === undefined) return '—';
		const t = new Date(iso);
		const local = new Date(t.getTime() + tzOffsetMin * 60_000);
		return `${String(local.getUTCHours()).padStart(2, '0')}:${String(local.getUTCMinutes()).padStart(2, '0')}`;
	}

	// 近 7 日运动，并与前 7 日对比：单看「本周 4 次」不知道是多还是少
	let recentWorkouts = $derived(workouts.filter((r) => r.local_date > shiftDays(-7, todayStr())));
	let prevWorkouts = $derived(workouts.filter((r) => r.local_date <= shiftDays(-7, todayStr())));
	let workoutSum = $derived({
		count: recentWorkouts.length,
		minutes: recentWorkouts.reduce((s, r) => s + r.duration_min, 0),
		energy: recentWorkouts.reduce((s, r) => s + (r.total_energy ?? 0), 0),
		delta: recentWorkouts.length - prevWorkouts.length,
		unit:
			new Set(recentWorkouts.map((r) => r.energy_unit).filter(Boolean)).size === 1
				? (recentWorkouts[0]?.energy_unit ?? '')
				: ''
	});

	// 目标进度：goals 表按指标存一个期望值，这里配上「今天的值」变进度条。
	// 没有目标就整块不渲染——一排空进度条什么也没说。
	let goalRows = $derived.by(() => {
		if (goals.length === 0) return [];
		return goals
			.map((g) => {
				const d = byName.get(g.metric);
				let value = baseByMetric.get(g.metric)?.value ?? null;
				// 睡眠总量走 sleep_sessions，不一定进天级聚合，所以要单独取一次
				if (value === null && g.metric === 'sleep_analysis' && latestSleep?.total_sleep_hr != null) {
					value = latestSleep.total_sleep_hr;
				}
				return {
					metric: g.metric,
					label: d?.display_name_zh ?? g.metric,
					unit: g.unit,
					digits: unitDigits(g.unit),
					target: g.target,
					value
				};
			})
			.filter((g) => byName.has(g.metric) || g.metric === 'sleep_analysis');
	});

	let insightRows = $derived(
		insights.map((i) => {
			const d = byName.get(i.metric);
			const unit = d?.preferred_unit ?? '';
			return { ...i, label: d?.display_name_zh ?? i.metric, unit, digits: unitDigits(unit) };
		})
	);

	function medianOf(points: DailyPoint[]): number | null {
		const vals = points.map((p) => p.value).sort((a, b) => a - b);
		if (vals.length === 0) return null;
		const mid = Math.floor(vals.length / 2);
		return vals.length % 2 === 1 ? vals[mid] : (vals[mid - 1] + vals[mid]) / 2;
	}

	function msg(e: unknown): string {
		return e instanceof Error ? e.message : String(e);
	}

	/** 单个数据源失败不该打挂整页：降级并记录，其余区块照常渲染。 */
	async function safe<T>(label: string, p: Promise<T>, fallback: T): Promise<T> {
		try {
			return await p;
		} catch (e) {
			errors = [...errors, `${label}：${msg(e)}`];
			return fallback;
		}
	}

	async function loadBaseline() {
		const res = await safe('常态对照', api.baseline(focus, todayStr(), BASELINE_WINDOW), {
			date: todayStr(),
			window: BASELINE_WINDOW,
			items: [] as BaselineItem[]
		});
		baselineItems = res.items;
	}

	async function loadTrends() {
		const to = todayStr();
		const res = await safe(
			'趋势数据',
			api.daily(trendMetrics, shiftDays(-(TREND_DAYS - 1), to), to),
			{ series: {} as Record<string, DailyPoint[]>, from: '', to: '' }
		);
		trendSeries = res.series;
		try {
			localStorage.setItem(TREND_STORE_KEY, JSON.stringify(trendMetrics));
		} catch {
			/* 存不下不影响本次会话 */
		}
	}

	async function load() {
		loading = true;
		errors = [];
		const to = todayStr();

		const metricsRes = await safe('指标字典', api.metrics(), {
			metrics: [] as MetricDef[],
			timezone: ''
		});
		defs = metricsRes.metrics.filter((d) => d.enabled);
		focus = loadFocus(defs);
		try {
			const raw = localStorage.getItem(TREND_STORE_KEY);
			if (raw) {
				const known = new Set(defs.map((d) => d.metric));
				const parsed = JSON.parse(raw) as unknown;
				if (Array.isArray(parsed)) {
					const kept = parsed.filter((m): m is string => typeof m === 'string' && known.has(m));
					if (kept.length === 2) trendMetrics = kept;
				}
			}
		} catch {
			/* 读不到就按默认两个 */
		}

		const emptyInsights = { date: to, window: BASELINE_WINDOW, items: [] as InsightItem[] };
		const all = defs.map((d) => d.metric);
		const [cov, ins, gl, sy, sl, nm, wk] = await Promise.all([
			safe('数据覆盖度', api.coverage(BASELINE_WINDOW), coverage),
			all.length
				? safe('偏离提示', api.insights(all, to, BASELINE_WINDOW, 6), emptyInsights)
				: Promise.resolve(emptyInsights),
			safe('目标', api.goals(), [] as GoalRow[]),
			safe(
				'同步状态',
				api.syncStatus(BASELINE_WINDOW),
				{ last_run: null, recent_runs: [], watermarks: [], missing_days: [] } as SyncStatus
			),
			safe('睡眠', api.sleep(shiftDays(-9, to), to), { rows: [] as SleepRow[] }),
			safe('夜间心率', api.sleepNightMetrics(shiftDays(-9, to), to, ['heart_rate']), {
				rows: [] as NightMetricRow[]
			}),
			safe('运动', api.workouts(shiftDays(-13, to), to), { rows: [] as WorkoutRow[] })
		]);
		coverage = cov;
		insights = ins.items;
		goals = gl;
		sync = sy;
		sleep = sl.rows;
		nightHr = nm.rows;
		workouts = wk.rows;

		await Promise.all([loadBaseline(), loadTrends()]);
		loading = false;
	}

	onMount(load);

	function toggleFocus(m: string) {
		const next = focus.includes(m) ? focus.filter((x) => x !== m) : [...focus, m];
		// 全清空会让第一屏彻底空白，至少留一项
		if (next.length === 0) return;
		saveFocus(next);
		void loadBaseline();
	}

	function changeTrend(slot: number, metric: string) {
		const next = [...trendMetrics];
		next[slot] = metric;
		trendMetrics = next;
		void loadTrends();
	}

	// 指标按分组罗列（与趋势页同一套分组口径），组内保持字典顺序
	let focusGroups = $derived(
		METRIC_GROUPS.map((g) => ({
			g,
			items: defs.filter((d) => g.metrics.includes(d.metric) && d.enabled)
		})).filter((v) => v.items.length > 0)
	);
</script>

<div class="space-y-4">
	<div class="flex items-baseline justify-between">
		<h1 class="text-[15px] font-medium">今日概览</h1>
		<span class="text-[12px] text-[var(--color-ink-500)]">
			{loading ? '加载中…' : todayStr()}
		</span>
	</div>

	{#each errors as e (e)}
		<div class="card px-4 py-2.5 text-[12px] text-[var(--color-danger)]">{e}</div>
	{/each}

	<!-- 1. 数据新鲜度：自托管管线会静默失效，它是后面所有数字的可信前提 -->
	<FreshnessBar status={sync} {loading} {dataNote} />

	<!-- 2. 今日 vs 我的常态 -->
	<div class="flex items-baseline justify-between gap-2">
		<h2 class="text-[13px] font-medium">今天离自己的常态有多远</h2>
		<button
			class="text-[12px] text-[var(--color-brand)] hover:underline"
			onclick={() => (focusEditing = !focusEditing)}
		>
			{focusEditing ? '完成' : '调整指标'}
		</button>
	</div>

	{#if focusEditing}
		<div class="card space-y-2 p-3">
			<p class="text-[11px] text-[var(--color-ink-500)]">
				选中的指标常驻这张卡片组；选择存在本机浏览器，换设备要重新选。
			</p>
			{#each focusGroups as { g, items } (g.id)}
				<div class="flex flex-col gap-1.5 sm:flex-row sm:items-start sm:gap-3">
					<div class="flex w-full shrink-0 items-center gap-1.5 sm:w-[68px] sm:pt-0.5">
						<span class="h-2 w-2 rounded-full" style="background:{g.tint}"></span>
						<span class="text-[11px] text-[var(--color-ink-500)]">{g.label}</span>
					</div>
					<div class="flex flex-wrap gap-1.5">
						{#each items as d (d.metric)}
							<button
								class="rounded-full border px-2.5 py-1 text-[12px] transition-colors {focus.includes(
									d.metric
								)
									? 'border-transparent bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
									: 'border-[var(--color-line)] text-[var(--color-ink-700)] hover:bg-[var(--color-line)]/50'}"
								onclick={() => toggleFocus(d.metric)}
							>
								{d.display_name_zh}
							</button>
						{/each}
					</div>
				</div>
			{/each}
		</div>
	{/if}

	<div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
		{#each cards as c (c.metric)}
			<MetricCard
				metric={c.metric}
				label={c.label}
				unit={c.unit}
				digits={c.digits}
				value={c.item?.value ?? null}
				baseline={c.item?.baseline ?? null}
				deviationPct={c.item?.deviation_pct ?? null}
				recent={c.item?.recent ?? []}
				windowDays={BASELINE_WINDOW}
				dateNote={c.item?.stale ? `${c.item.date} 的数据` : ''}
				hint={c.hint}
				{loading}
			/>
		{/each}
	</div>

	{#if !loading && cards.every((c) => c.item?.value == null)}
		<div class="card px-4 py-3 text-[12px] text-[var(--color-ink-500)]">
			关注的指标暂无数据。先看上面的同步状态——多半是今天还没同步，而不是指标没接。
		</div>
	{/if}

	<!-- 3. 目标进度：没有设目标就不占版面 -->
	{#if goalRows.length > 0}
		<div class="card p-4">
			<div class="mb-2.5 flex items-baseline justify-between">
				<span class="text-[13px] font-medium">今日进度</span>
				<a href="/settings" class="text-[11px] text-[var(--color-brand)] hover:underline">目标在设置页修改</a>
			</div>
			<GoalProgress rows={goalRows} />
		</div>
	{/if}

	<!-- 4. 昨夜睡眠 / 近 7 日运动：两张「结论卡」，各自带去详情页的入口 -->
	<div class="grid gap-3 lg:grid-cols-2">
		<div class="card p-4">
			<div class="mb-2 flex items-baseline justify-between">
				<span class="text-[13px] font-medium">昨夜睡眠</span>
				<a href="/sleep" class="text-[11px] text-[var(--color-brand)] hover:underline">睡眠详情</a>
			</div>
			{#if latestSleep}
				<div class="flex items-baseline gap-2">
					<span class="num text-[22px] font-medium">
						{formatDurationHours(latestSleep.total_sleep_hr)}
					</span>
					<span class="text-[12px] text-[var(--color-ink-500)]">
						{clockOf(latestSleep.sleep_start, latestSleep.tz_offset_min)} → {clockOf(
							latestSleep.sleep_end,
							latestSleep.tz_offset_min
						)}
					</span>
				</div>
				{#if sleepStructure && sleepStructure.some((p) => p.width > 0)}
					<div class="mt-2 flex h-2 overflow-hidden rounded-full bg-[var(--color-line)]">
						{#each sleepStructure as p (p.key)}
							<div style="width:{p.width}%;background:{p.color}"></div>
						{/each}
					</div>
					<div class="mt-1.5 flex flex-wrap gap-x-4 gap-y-0.5 text-[11px] text-[var(--color-ink-500)]">
						{#each sleepStructure as p (p.key)}
							<span>
								{p.label}
								<span class="num">{formatDurationHours(p.hr)}</span>
							</span>
						{/each}
					</div>
				{:else}
					<div class="mt-2 text-[11px] text-[var(--color-ink-500)]">
						这一晚没有分期数据（需要导出里带上分段）
					</div>
				{/if}
				<div class="mt-1.5 flex flex-wrap gap-x-4 text-[11px] text-[var(--color-ink-500)]">
					<span>{latestSleep.local_date}</span>
					{#if latestSleep.awake_count != null}
						<span class="num">醒来 {latestSleep.awake_count} 次</span>
					{/if}
					{#if night}
						<span class="num">夜间最低心率 {Math.round(night.min)} {night.unit}</span>
					{/if}
				</div>
			{:else}
				<div class="text-[12px] text-[var(--color-ink-500)]">
					{loading ? '加载中…' : '近 10 天没有睡眠记录'}
				</div>
			{/if}
		</div>

		<div class="card p-4">
			<div class="mb-2 flex items-baseline justify-between">
				<span class="text-[13px] font-medium">近 7 日运动</span>
				<a href="/workouts" class="text-[11px] text-[var(--color-brand)] hover:underline">运动记录</a>
			</div>
			<div class="flex items-baseline gap-2">
				<span class="num text-[22px] font-medium">{workoutSum.count} 次</span>
				<span class="num text-[12px] text-[var(--color-ink-500)]">
					共 {formatDuration(workoutSum.minutes)}{#if workoutSum.energy > 0} · {Math.round(
							workoutSum.energy
						)}{workoutSum.unit ? ` ${workoutSum.unit}` : ''}{/if}
				</span>
			</div>
			<div class="mt-1.5 text-[11px] text-[var(--color-ink-500)]">
				{#if loading}
					加载中…
				{:else if workoutSum.count === 0}
					近 7 天没有运动记录
				{:else if workoutSum.delta !== 0}
					比前 7 天 {workoutSum.delta > 0 ? '多' : '少'} {Math.abs(workoutSum.delta)} 次
				{:else}
					与前 7 天持平
				{/if}
			</div>
			{#if recentWorkouts.length > 0}
				{@const last = recentWorkouts[recentWorkouts.length - 1]}
				<div class="mt-2 truncate text-[11px] text-[var(--color-ink-500)]">
					最近一次：{last.local_date} {last.name} {formatDuration(last.duration_min)}
				</div>
			{/if}
		</div>
	</div>

	<!-- 5. 趋势：默认常驻两张，指标可换；虚线是常态，回答「这段时间整体在什么水平」 -->
	<div class="grid gap-3 lg:grid-cols-2">
		{#each trendMetrics as metric, slot (slot)}
			{@const d = byName.get(metric)}
			{@const series = trendSeries[metric] ?? []}
			{@const baseline = medianOf(series)}
			<div class="card p-4">
				<div class="mb-2 flex items-baseline justify-between gap-2">
					<select
						class="min-w-0 shrink rounded-md border border-[var(--color-line)] bg-[var(--color-surface)] px-2 py-1 text-[12px] text-[var(--color-ink-700)]"
						value={metric}
						onchange={(e) => changeTrend(slot, e.currentTarget.value)}
					>
						{#each defs as o (o.metric)}
							<option value={o.metric}>{o.display_name_zh}</option>
						{/each}
					</select>
					<span class="shrink-0 text-[11px] text-[var(--color-ink-500)]">近 {TREND_DAYS} 天</span>
				</div>
				<TrendChart
					series={[
						{
							name: d?.display_name_zh ?? metric,
							type: d?.kind === 'instant' ? 'line' : 'bar',
							points: series.map((p) => ({ date: p.date, value: p.value })),
							color: groupOf(metric).tint,
							unit: d?.preferred_unit ?? '',
							digits: unitDigits(d?.preferred_unit ?? ''),
							metric
						}
					]}
					height={220}
					unit={d?.preferred_unit ?? ''}
					digits={unitDigits(d?.preferred_unit ?? '')}
					targetLine={baseline === null ? null : { value: baseline, label: '常态' }}
				/>
				<div class="mt-1 text-[11px] text-[var(--color-ink-500)]">
					近 {TREND_DAYS} 天{#if baseline !== null} · 虚线为区间中位数{/if}
				</div>
			</div>
		{/each}
	</div>

	<!-- 6. 值得关注 + 覆盖度：由服务端判定，前端不重复实现（免得首页与提示口径分裂） -->
	<div class="card p-4">
		<div class="mb-2 flex items-baseline justify-between gap-2">
			<span class="text-[13px] font-medium">值得关注</span>
			<span class="text-[11px] text-[var(--color-ink-500)]">
				相对近 {BASELINE_WINDOW} 天常态 · 近 {coverage.window_days} 天有 {coverage.rows.length} /
				{defs.length} 项指标有数据
			</span>
		</div>
		{#if loading}
			<div class="text-[12px] text-[var(--color-ink-500)]">加载中…</div>
		{:else if insightRows.length === 0}
			<div class="text-[12px] text-[var(--color-ink-500)]">
				近期没有明显偏离常态的指标——该吃吃该睡睡。
			</div>
		{:else}
			<ul class="space-y-1.5">
				{#each insightRows as i (i.metric)}
					<li class="flex flex-wrap items-baseline gap-x-2 text-[13px]">
						<a href="/trends" class="font-medium hover:underline">{i.label}</a>
						<span class="text-[var(--color-ink-700)]">
							{i.direction === 'high' ? '高于' : '低于'}常态 {Math.abs(i.deviation_pct).toFixed(1)}%
						</span>
						<span class="num text-[12px] text-[var(--color-ink-500)]">
							{formatValue(i.value, i.unit, i.digits)} · 常态 {formatValue(
								i.baseline,
								i.unit,
								i.digits
							)}
						</span>
						<span
							class="rounded bg-[var(--color-line)] px-1.5 py-0.5 text-[11px] text-[var(--color-ink-500)]"
						>
							{i.kind === 'streak' ? `连续 ${i.streak_days} 天` : '单日离群'}
						</span>
						<span class="text-[11px] text-[var(--color-ink-500)]">{i.date}</span>
					</li>
				{/each}
			</ul>
		{/if}
	</div>
</div>
