export type MetricDef = {
	metric: string;
	display_name_zh: string;
	display_name: string;
	kind: 'cumulative' | 'instant' | 'interval' | 'categorical';
	day_agg: 'sum' | 'mean' | 'max' | 'min' | 'last' | 'count';
	cross_source_agg: 'max' | 'mean' | 'sum' | 'latest';
	unit: string;
	preferred_unit: string;
	value_field: string;
	chart: string;
	enabled: boolean;
	sort_order: number;
};

export type DailyPoint = {
	date: string;
	metric: string;
	value: number;
	sample_count: number;
	source_count: number;
	sources: string[];
};

export type SummaryItem = {
	metric: string;
	display_name: string;
	unit: string;
	value: number | null;
	prev_value: number | null;
	delta: number | null;
	delta_pct: number | null;
	updated_at?: string;
};

/**
 * 「今日 vs 我的常态」的一行：常态是对比日之前窗口内的中位数。
 * 今日没数据时服务端回落到最后有数据的那天，并把 stale 置真。
 */
export type BaselineItem = {
	metric: string;
	/** 对比值所在日期；窗口内完全没有数据时为空串 */
	date: string;
	value: number | null;
	prev_value: number | null;
	/** 常态（窗口内中位数）；样本不足时为 null */
	baseline: number | null;
	/** 相对常态的偏离百分比；常态缺失或为 0 时为 null */
	deviation_pct: number | null;
	/** 参与常态计算的天数 */
	sample_days: number;
	window_days: number;
	/** 截至对比日的最后若干个有数据的点（日期升序），用于迷你走势 */
	recent: { date: string; value: number }[];
	stale: boolean;
};

/** 一条「值得关注」：偏离常态到值得推给用户看的程度。 */
export type InsightItem = {
	metric: string;
	/** outlier = 单点离群；streak = 连续若干天同向偏离 */
	kind: 'outlier' | 'streak';
	/** high / low：只说方向，不下「好 / 坏」结论 */
	direction: 'high' | 'low';
	date: string;
	value: number;
	baseline: number;
	deviation_pct: number;
	streak_days: number;
};

export type CoverageItem = {
	metric: string;
	/** 窗口内有数据的天数 */
	days: number;
	last_date: string;
	/** 距窗口末端多少天；无数据时为 −1 */
	days_since: number;
};

export type Coverage = {
	from: string;
	to: string;
	window_days: number;
	rows: CoverageItem[];
};

export type SleepRow = {
	local_date: string;
	source: string;
	/** UTC RFC3339 */
	sleep_start?: string;
	/** UTC RFC3339 */
	sleep_end?: string;
	/** UTC RFC3339 · 上床时刻；与 sleep_start 相减即入睡潜伏期 */
	in_bed_start?: string;
	/** UTC RFC3339 · 离床时刻 */
	in_bed_end?: string;
	total_sleep_hr?: number;
	asleep_hr?: number;
	core_hr?: number;
	deep_hr?: number;
	rem_hr?: number;
	/** 夜间清醒总时长（小时） */
	awake_hr?: number;
	in_bed_hr?: number;
	/** 本地时区偏移（分钟），用于把 sleep_start 还原成本地入睡时刻 */
	tz_offset_min?: number;
	/** 夜间清醒「段数」；仅当导出含睡眠分段数据时才有值 */
	awake_count?: number;
	/** 入睡后清醒时长（小时，WASO）：不含入睡前躺着的那段；无分段数据时缺省 */
	waso_hr?: number;
};

export type SleepStageSeg = {
	/** UTC RFC3339 */
	start: string;
	/** UTC RFC3339 */
	end: string;
	/** 归一化后的阶段键：awake/rem/core/deep/asleep/inbed/other */
	stage: string;
	/** 原始 stage 文本，便于排查上游写法 */
	raw: string;
	source: string;
	/** 该段起点在本夜时间轴上的位置（分钟），以睡眠日本地 00:00 为 0，跨零点顺延 */
	local_min: number;
	/** 该段时长（分钟） */
	duration_min: number;
};

export type SleepStages = {
	local_date?: string;
	source?: string;
	tz_offset_min?: number;
	has_session?: boolean;
	sleep_start?: string;
	sleep_end?: string;
	segments: SleepStageSeg[];
	/** 各阶段合计小时数 */
	totals: Record<string, number>;
	/** 同一晚入睡窗口内的生理指标逐点序列（夜间心率等），与 segments 共用本夜时间轴 */
	physiology: NightSeries[];
};

/** 单夜时间轴上的一个采样点（local_min 与 SleepStageSeg 同一把尺子） */
export type NightPoint = {
	local_min: number;
	value: number;
	min?: number;
	max?: number;
};

/** 某晚入睡窗口内一个生理指标的序列 */
export type NightSeries = {
	metric: string;
	unit: string;
	source: string;
	avg: number;
	min: number;
	max: number;
	/** 最低值在本夜轴上的位置（分钟），用于标出「最低出现在几点」 */
	min_local_min: number;
	sample_count: number;
	points: NightPoint[];
};

/** 某晚入睡窗口内一个生理指标的聚合（/api/sleep/night-metrics 的一行） */
export type NightMetricRow = {
	/** 睡眠日（入睡时刻的本地日期），与 SleepRow.local_date 同口径 */
	local_date: string;
	metric: string;
	source: string;
	avg: number;
	/** 窗口内最低值：心率取每段 value_min 的最小，即 Apple 的「夜间最低心率」 */
	min: number;
	max: number;
	sample_count: number;
	/** 最低值出现时刻（UTC RFC3339） */
	min_at?: string;
	/** 打出最低值的那条样本自带的偏移（分钟），缺省时不还原钟点 */
	min_at_tz_offset_min?: number;
	window_start: string;
	window_end: string;
	/** 窗口时长（分钟） */
	window_min: number;
	unit: string;
};

/** 区间内某一晚的分期序列（睡眠时间表矩阵的一行） */
export type NightStages = {
	local_date: string;
	source: string;
	tz_offset_min: number;
	has_session: boolean;
	sleep_start?: string;
	sleep_end?: string;
	segments: SleepStageSeg[];
};

export type WorkoutRow = {
	id: number;
	local_date: string;
	name: string;
	source: string;
	start: string;
	end: string;
	duration_min: number;
	total_energy?: number;
	energy_unit?: string;
	/** 主动能量（不含基础代谢）；Apple 运动记录里的「消耗」是这个口径 */
	active_energy?: number;
	active_energy_unit?: string;
	total_distance?: number;
	distance_unit?: string;
	avg_hr?: number;
	max_hr?: number;
	effort_score?: number;
};

/** 一条运动的逐点曲线与轨迹摘要（/api/workouts/series） */
export type WorkoutSeries = {
	workout_id: number;
	start: string;
	hr_secs?: number[];
	hr_bpm?: number[];
	recovery_secs?: number[];
	recovery_bpm?: number[];
	splits?: unknown;
	has_route: boolean;
	route_points: number;
};

/** 一条目标（单位与中文名由后端从指标字典取） */
export type GoalRow = {
	metric: string;
	display_name: string;
	unit: string;
	target: number;
	updated_at: string;
};

/** 一条服务端共享的偏好。值一律是文本，语义由键名决定（目前只有 bedtime_target）。 */
export type AppSetting = {
	key: string;
	value: string;
	updated_at: string;
};

/** 一条心电记录（不含波形：波形单独一条接口） */
export type EcgRecord = {
	id: number;
	local_date: string;
	/** UTC RFC3339 */
	start: string;
	/** UTC RFC3339 */
	end: string;
	duration_sec: number;
	/** 上游原文，随手机语言变化（简中「窦性心律」/ 英文 "Sinus Rhythm"） */
	classification: string;
	/** 归一后的稳定码，跨语言可比 */
	classification_code: string;
	/** 归一后的统一措辞；classification_known=false 时它就是上游原文 */
	classification_label: string;
	classification_known: boolean;
	average_heart_rate?: number;
	sampling_frequency?: number;
	voltage_count: number;
	source: string;
	tz_offset_min: number;
};

/** 一条记录的电压波形 */
export type EcgWaveform = {
	id: number;
	/** UTC RFC3339 */
	start: string;
	/** 上游写的单位，原样展示（HAE 实测给的是 mcV） */
	unit: string;
	sampling_frequency?: number;
	/** 由点数与采样率推出的时间跨度（秒） */
	duration_sec: number;
	/** 按时间升序的电压值，第 i 点即 start + i/采样率 */
	voltages: number[];
};

export type SourceRow = {
	source: string;
	device_class: string;
	preferred: boolean;
	first_seen: string;
	last_seen: string;
	samples: number;
};

export type SyncRun = {
	id: number;
	transport: string;
	automation_id?: string;
	session_id?: string;
	received_at: string;
	sample_count: number;
	inserted: number;
	duplicates: number;
	workouts: number;
	sleep: number;
	ecg: number;
	status: string;
	error?: string;
};

/** 一次投递落下的事件条数（运动 / 睡眠 / 心电）；全为 0 时返回空串。 */
export function eventCounts(r: SyncRun): string {
	const parts: string[] = [];
	if (r.workouts) parts.push(`运动 ${r.workouts}`);
	if (r.sleep) parts.push(`睡眠 ${r.sleep}`);
	if (r.ecg) parts.push(`心电 ${r.ecg}`);
	return parts.join(' · ');
}

/**
 * 一次投递的条数摘要。
 *
 * 运动、睡眠、心电都不产生样本（各自落自己的事件表），只报 sample_count
 * 会让一次纯心电同步显示成「收到 0 条，入库 0」——看着像同步坏了，
 * 实际是几十条心电刚进来。
 */
export function describeRun(r: SyncRun): string {
	const base = `收到 ${r.sample_count} 条，入库 ${r.inserted}`;
	const events = eventCounts(r);
	return events ? `${base} · ${events}` : base;
}

export type SyncStatus = {
	last_run: SyncRun | null;
	recent_runs: SyncRun[];
	watermarks: { automation_id: string; metric: string; last_start_time: string; last_local_date: string }[];
	missing_days: string[];
	home_timezone?: string;
};

async function get<T>(path: string, init?: RequestInit): Promise<T> {
	const res = await fetch(path, { headers: { accept: 'application/json' }, ...init });
	if (!res.ok) {
		throw new Error(`${path} 请求失败: ${res.status}`);
	}
	return (await res.json()) as T;
}

// Go 侧 nil slice 会被编码成 JSON null（历史响应与网关重写都可能带来 null）。
// 页面里到处直接 .map / 展开 / .at()，一处 null 就整页崩成错误页，
// 所以在数据入口统一兜底成空数组。
function arr<T>(v: T[] | null | undefined): T[] {
	return Array.isArray(v) ? v : [];
}

/** 带设置令牌的写入，成功后回读整表（服务端本来就整表返回，省一次往返）。
 *  503 与 401 分开说：前者是服务端没配令牌，用户重试多少次都一样；
 *  后者是令牌打错了，改对就能保存。合成一句「保存失败」只会让人反复试。 */
async function mutate<T>(
	method: 'PUT' | 'DELETE',
	path: string,
	body: string | undefined,
	token: string,
	listKey: string
): Promise<T[]> {
	const res = await fetch(path, {
		method,
		headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
		body
	});
	if (!res.ok) {
		const why =
			res.status === 503
				? '服务端未配置 ZHICHANG_SETTINGS_TOKEN，保持只读'
				: res.status === 401
					? '设置令牌不正确'
					: `${res.status}`;
		throw new Error(`保存失败：${why}`);
	}
	const r = (await res.json()) as Record<string, T[] | null | undefined>;
	return arr(r[listKey]);
}

export const api = {
	metrics: async () => {
		const r = await get<{ timezone: string; metrics: MetricDef[] }>('/api/metrics');
		return { ...r, metrics: arr(r.metrics) };
	},
	summary: async (date?: string) => {
		const r = await get<{ date: string; today: string; items: SummaryItem[] }>(
			`/api/summary${date ? `?date=${date}` : ''}`
		);
		return { ...r, items: arr(r.items) };
	},
	daily: async (metrics: string[], from: string, to: string) => {
		const r = await get<{ from: string; to: string; series: Record<string, DailyPoint[]> }>(
			`/api/daily?metrics=${encodeURIComponent(metrics.join(','))}&from=${from}&to=${to}`
		);
		const series: Record<string, DailyPoint[]> = {};
		for (const [k, v] of Object.entries(r.series ?? {})) series[k] = arr(v);
		return { ...r, series };
	},
	baseline: async (metrics: string[], date: string, window = 30) => {
		const r = await get<{ date: string; window: number; items: BaselineItem[] }>(
			`/api/baseline?metrics=${encodeURIComponent(metrics.join(','))}&date=${date}&window=${window}`
		);
		const items = arr(r.items).map((i) => ({ ...i, recent: arr(i.recent) }));
		return { ...r, items };
	},
	insights: async (metrics: string[], date: string, window = 30, limit = 6) => {
		const r = await get<{ date: string; window: number; items: InsightItem[] }>(
			`/api/insights?metrics=${encodeURIComponent(metrics.join(','))}&date=${date}&window=${window}&limit=${limit}`
		);
		return { ...r, items: arr(r.items) };
	},
	coverage: async (days = 30) => {
		const r = await get<Coverage>(`/api/coverage?days=${days}`);
		return { ...r, rows: arr(r.rows) };
	},
	sleep: async (from: string, to: string) => {
		const r = await get<{ rows: SleepRow[] }>(`/api/sleep?from=${from}&to=${to}`);
		return { ...r, rows: arr(r.rows) };
	},
	sleepStages: async (date?: string) => {
		const q = date ? `?date=${date}` : '';
		const r = await get<SleepStages>(`/api/sleep/stages${q}`);
		return { ...r, segments: arr(r.segments), totals: r.totals ?? {}, physiology: arr(r.physiology) };
	},
	sleepStagesRange: async (from: string, to: string) => {
		const r = await get<{ rows: NightStages[] }>(`/api/sleep/stages/range?from=${from}&to=${to}`);
		return { ...r, rows: arr(r.rows).map((n) => ({ ...n, segments: arr(n.segments) })) };
	},
	sleepNightMetrics: async (from: string, to: string, metrics?: string[]) => {
		const q = metrics?.length ? `&metrics=${encodeURIComponent(metrics.join(','))}` : '';
		const r = await get<{ rows: NightMetricRow[] }>(`/api/sleep/night-metrics?from=${from}&to=${to}${q}`);
		return { ...r, rows: arr(r.rows) };
	},
	workouts: async (from: string, to: string) => {
		const r = await get<{ rows: WorkoutRow[] }>(`/api/workouts?from=${from}&to=${to}`);
		return { ...r, rows: arr(r.rows) };
	},
	/** 一条运动的逐点心率 / 恢复心率 / 分段 / 轨迹摘要（没有曲线时后端回空数组） */
	workoutSeries: async (id: number) => {
		const r = await get<WorkoutSeries>(`/api/workouts/series?id=${id}`);
		return {
			...r,
			hr_secs: arr(r.hr_secs),
			hr_bpm: arr(r.hr_bpm),
			recovery_secs: arr(r.recovery_secs),
			recovery_bpm: arr(r.recovery_bpm)
		};
	},
	ecg: async (from: string, to: string) => {
		const r = await get<{ from: string; to: string; records: EcgRecord[] }>(
			`/api/ecg?from=${from}&to=${to}`
		);
		return { ...r, records: arr(r.records) };
	},
	ecgWaveform: async (id: number) => {
		const r = await get<EcgWaveform>(`/api/ecg/waveform?id=${id}`);
		return { ...r, voltages: arr(r.voltages) };
	},
	goals: async () => {
		const r = await get<{ goals: GoalRow[] }>('/api/goals');
		return arr(r.goals);
	},
	/** 写入目标：必须带设置令牌（服务端未配置时回 503） */
	setGoal: (metric: string, target: number, token: string) =>
		mutate<GoalRow>('PUT', '/api/goals', JSON.stringify({ metric, target }), token, 'goals'),
	/** 清掉某个指标的目标；服务端是幂等的，不存在也回整表 */
	deleteGoal: (metric: string, token: string) =>
		mutate<GoalRow>(
			'DELETE',
			`/api/goals?metric=${encodeURIComponent(metric)}`,
			undefined,
			token,
			'goals'
		),
	settings: async () => {
		const r = await get<{ settings: AppSetting[] }>('/api/settings');
		return arr(r.settings);
	},
	setSetting: (key: string, value: string, token: string) =>
		mutate<AppSetting>('PUT', '/api/settings', JSON.stringify({ key, value }), token, 'settings'),
	deleteSetting: (key: string, token: string) =>
		mutate<AppSetting>(
			'DELETE',
			`/api/settings?key=${encodeURIComponent(key)}`,
			undefined,
			token,
			'settings'
		),
	sources: async () => {
		const r = await get<{ sources: SourceRow[] }>('/api/sources');
		return { ...r, sources: arr(r.sources) };
	},
	syncStatus: async (days = 30) => {
		const r = await get<SyncStatus>(`/api/sync/status?days=${days}`);
		return {
			...r,
			recent_runs: arr(r.recent_runs),
			watermarks: arr(r.watermarks),
			missing_days: arr(r.missing_days)
		};
	}
};

export function formatValue(v: number | null | undefined, unit?: string, digits = 0): string {
	if (v === null || v === undefined || Number.isNaN(v)) return '—';
	const n = Math.abs(v) >= 1000 && digits === 0 ? Math.round(v).toLocaleString('zh-CN') : v.toFixed(digits);
	return unit ? `${n} ${unit}` : String(n);
}

export function todayStr(): string {
	const d = new Date();
	return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

export function shiftDays(days: number, base = todayStr()): string {
	const d = new Date(`${base}T00:00:00`);
	d.setDate(d.getDate() + days);
	return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}
