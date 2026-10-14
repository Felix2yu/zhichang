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

export type WorkoutRow = {
	local_date: string;
	name: string;
	source: string;
	start: string;
	end: string;
	duration_min: number;
	total_energy?: number;
	energy_unit?: string;
	total_distance?: number;
	distance_unit?: string;
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
	status: string;
	error?: string;
};

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
	sleep: async (from: string, to: string) => {
		const r = await get<{ rows: SleepRow[] }>(`/api/sleep?from=${from}&to=${to}`);
		return { ...r, rows: arr(r.rows) };
	},
	sleepStages: async (date?: string) => {
		const q = date ? `?date=${date}` : '';
		const r = await get<SleepStages>(`/api/sleep/stages${q}`);
		return { ...r, segments: arr(r.segments), totals: r.totals ?? {}, physiology: arr(r.physiology) };
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
