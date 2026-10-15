<script lang="ts">
	import { onMount } from 'svelte';
	import { flip } from 'svelte/animate';
	import { api, shiftDays, todayStr, type DailyPoint, type MetricDef } from '#lib/api';
	import { METRIC_GROUPS, unitDigits, type MetricGroup } from '#lib/metric-groups';
	import { prefs } from '#lib/prefs.svelte';
	import {
		ACTIVITY_WINDOW_DAYS,
		NO_ACTIVITY,
		computeActivity,
		isVeryStale,
		meanScore,
		rankByActivity,
		staleLabel,
		type ActivityStats
	} from '#lib/metric-activity';
	import RangePicker from '#lib/components/RangePicker.svelte';
	import MetricTrendCard from '#lib/components/MetricTrendCard.svelte';

	let defs = $state<MetricDef[]>([]);
	let selected = $state<string[]>(['step_count', 'heart_rate']);
	let days = $state(prefs.rangeDays);
	let series = $state<Record<string, DailyPoint[]>>({});
	// 活跃度：按固定 90 天窗口单独评估，与页面时间范围无关（见 metric-activity.ts）。
	let activity = $state<Record<string, ActivityStats>>({});
	let loading = $state(true);
	let error = $state('');

	let byName = $derived(new Map(defs.map((d) => [d.metric, d])));

	function aggLabel(a: string | undefined): string {
		switch (a) {
			case 'sum':
				return '求和';
			case 'mean':
				return '均值';
			case 'max':
				return '最大';
			case 'min':
				return '最小';
			case 'last':
				return '最近一次';
			case 'count':
				return '计数';
			default:
				return a ?? '—';
		}
	}
	function crossLabel(a: string | undefined): string {
		switch (a) {
			case 'max':
				return '跨来源取最大';
			case 'mean':
				return '跨来源加权均值';
			case 'sum':
				return '跨来源求和';
			case 'latest':
				return '跨来源最近';
			default:
				return a ?? '—';
		}
	}

	// 选择面板上的悬停提示：所选指标的更新新鲜度，帮助预判它会被排到哪里。
	function chipHint(m: string): string {
		const a = activity[m];
		if (!a || a.daysSince === null) return `近 ${ACTIVITY_WINDOW_DAYS} 天无同步记录`;
		if (a.daysSince === 0) return '今天有数据';
		return `最近一次更新：${a.daysSince} 天前`;
	}

	// 单指标卡片的数据视图：一个指标 => 一张图。
	function cardOf(metric: string, group: MetricGroup) {
		const d = byName.get(metric);
		const unit = d?.preferred_unit || '';
		return {
			metric,
			name: d?.display_name_zh ?? metric,
			unit,
			digits: unitDigits(unit),
			// 累计量 / 类别量 / 区间量用柱，瞬时量用线
			chartType: (d?.kind === 'instant' ? 'line' : 'bar') as 'bar' | 'line',
			color: group.tint,
			aggNote: `日内${aggLabel(d?.day_agg)} · ${crossLabel(d?.cross_source_agg)}`,
			points: (series[metric] ?? []).map((p) => ({ date: p.date, value: p.value })),
			// —— 排序依据 ——
			order: d?.sort_order ?? 999, // 同分时回落到指标字典顺序
			activity: activity[metric] ?? NO_ACTIVITY
		};
	}

	// 分区视图：分组 => 若干「各自独立成图」的指标卡片。分组只是版面归属。
	//
	// 卡片动态排序：不是所有指标每天都有数据，因此卡片的先后不按固定字典顺序，
	// 而是按「活跃度」——最近更新、更新得勤的排前；长时间没新数据的逐步下移。
	// 分区间同样按组内平均活跃度排序：整组都陈旧的会被整体推到后面。
	// 分组本身不参与图形合成，只决定卡片归属哪条版式分区。
	type Card = ReturnType<typeof cardOf>;
	let sections = $derived(
		METRIC_GROUPS.map((group, gi) => {
			const ms = selected.filter((m) => group.metrics.includes(m));
			if (ms.length === 0) return null;
			const cards = rankByActivity(ms.map((m) => cardOf(m, group)));
			return { group, gi, cards, score: meanScore(cards) };
		})
			.filter((v): v is { group: MetricGroup; gi: number; cards: Card[]; score: number } => v !== null)
			.sort((a, b) => b.score - a.score || a.gi - b.gi)
	);

	// 指标选择面板：按分类罗列可选指标
	let selectableGroups = $derived(
		METRIC_GROUPS.map((group) => ({
			group,
			items: defs.filter((d) => group.metrics.includes(d.metric) && d.enabled)
		})).filter((v) => v.items.length > 0)
	);

	function toggle(m: string) {
		selected = selected.includes(m) ? selected.filter((x) => x !== m) : [...selected, m];
		load();
	}
	function clearAll() {
		selected = [];
		series = {};
	}

	async function load() {
		if (selected.length === 0) {
			series = {};
			return;
		}
		loading = true;
		try {
			const to = todayStr();
			const res = await api.daily(selected, shiftDays(-days + 1, to), to);
			series = res.series;
			error = '';
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	// 活跃度单独取一次固定窗口的数据：即使用户把展示范围调到 7 天，
	// 排序依据仍来自 90 天的更新历史，位置才不会随范围切换而乱跳。
	async function loadActivity() {
		if (defs.length === 0) return;
		try {
			const to = todayStr();
			const from = shiftDays(-(ACTIVITY_WINDOW_DAYS - 1), to);
			const res = await api.daily(
				defs.map((d) => d.metric),
				from,
				to
			);
			activity = Object.fromEntries(
				defs.map((d) => [d.metric, computeActivity(res.series[d.metric] ?? [], to)])
			);
		} catch {
			// 评估失败不阻断页面：退回指标字典顺序，排序结果依然确定。
			activity = {};
		}
	}

	onMount(async () => {
		try {
			defs = (await api.metrics()).metrics.filter((d) => d.enabled);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
		await Promise.all([load(), loadActivity()]);
	});
</script>

<div class="space-y-4">
	<!-- 工具条：标题 + 区间 -->
	<div class="flex flex-wrap items-baseline justify-between gap-2">
		<h1 class="text-[15px] font-medium">趋势</h1>
		<RangePicker value={days} onchange={(d) => ((days = d), load())} />
	</div>

	{#if error}
		<div class="card px-4 py-3 text-[13px] text-[var(--color-danger)]">{error}</div>
	{/if}

	{#if defs.length === 0}
		{#if !error}
			<div class="card flex h-[160px] items-center justify-center text-[13px] text-[var(--color-ink-500)]">
				加载中…
			</div>
		{/if}
	{:else}
		<!-- 指标选择：按苹果健康分类罗列，选中即以「每指标一图」的形式出现在下方 -->
		<div class="card p-3">
			<div class="mb-1.5 flex items-center justify-between">
				<span class="text-[12px] font-medium text-[var(--color-ink-700)]">指标选择</span>
				<div class="flex items-center gap-2 text-[11px] text-[var(--color-ink-500)]">
					<span>已选 {selected.length} 项</span>
					{#if selected.length > 0}
						<button class="transition-colors hover:text-[var(--color-brand)]" onclick={clearAll}>
							清空
						</button>
					{/if}
				</div>
			</div>
			<p class="mb-2.5 text-[11px] text-[var(--color-ink-500)]/80">
				卡片与分区均按更新活跃度排列（近 {ACTIVITY_WINDOW_DAYS} 天）：更新得勤、最近有数据的在前，长期没新数据的自动下移。
			</p>
			<div class="space-y-2">
				{#each selectableGroups as { group, items } (group.id)}
					<div class="flex flex-col gap-1.5 sm:flex-row sm:items-start sm:gap-3">
						<div class="flex w-full shrink-0 items-center gap-1.5 sm:w-[68px] sm:pt-0.5">
							<span class="h-2 w-2 rounded-full" style="background:{group.tint}"></span>
							<span class="text-[11px] text-[var(--color-ink-500)]">{group.label}</span>
						</div>
						<div class="flex flex-wrap gap-1.5">
							{#each items as d (d.metric)}
								<button
									class="rounded-full border px-2.5 py-1 text-[12px] transition-colors {selected.includes(
										d.metric
									)
										? 'border-transparent bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
										: 'border-[var(--color-line)] text-[var(--color-ink-700)] hover:bg-[var(--color-line)]/50'}"
									title={chipHint(d.metric)}
									onclick={() => toggle(d.metric)}
								>
									{d.display_name_zh}
								</button>
							{/each}
						</div>
					</div>
				{/each}
			</div>
		</div>

		{#if selected.length === 0}
			<div class="card flex h-[160px] flex-col items-center justify-center gap-1 text-center">
				<span class="text-[13px] text-[var(--color-ink-500)]">尚未选择指标</span>
				<span class="text-[11px] text-[var(--color-ink-500)]/70"
					>在上方按分类挑选，每个指标会生成一张独立的趋势图</span
				>
			</div>
		{:else}
			<div class="space-y-6">
				{#each sections as section (section.group.id)}
					<section animate:flip={{ duration: 420 }}>
						<!-- 分区表头：分组只决定版面归属 -->
						<div class="flex items-baseline gap-2">
							<span
								class="h-3.5 w-1 self-center rounded-full"
								style="background:{section.group.tint}"
							></span>
							<h2 class="text-[13px] font-medium">{section.group.label}</h2>
							<span class="min-w-0 flex-1 truncate text-[11px] text-[var(--color-ink-500)]"
								>{section.group.desc}</span
							>
							<span class="shrink-0 text-[11px] text-[var(--color-ink-500)]"
								>{section.cards.length} 项</span
							>
						</div>
						<!-- 每张卡片 = 一个指标 = 一张独立图表；顺序由活跃度决定，变化时平滑位移 -->
						<div class="mt-2.5 grid grid-cols-1 gap-3 md:grid-cols-2 2xl:grid-cols-3">
							{#each section.cards as c (c.metric)}
								<div class="min-w-0" animate:flip={{ duration: 420 }}>
									<MetricTrendCard
										name={c.name}
										unit={c.unit}
										points={c.points}
										chartType={c.chartType}
										color={c.color}
										digits={c.digits}
										aggNote={c.aggNote}
										staleText={staleLabel(c.activity)}
										staleStrong={isVeryStale(c.activity)}
										class="h-full"
										{loading}
										{days}
									/>
								</div>
							{/each}
						</div>
					</section>
				{/each}
			</div>
		{/if}
	{/if}
</div>
