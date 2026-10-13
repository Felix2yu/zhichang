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
	sleep_start?: string;
	sleep_end?: string;
	total_sleep_hr?: number;
	asleep_hr?: number;
	core_hr?: number;
	deep_hr?: number;
	rem_hr?: number;
	in_bed_hr?: number;
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

export type SyncStatus = {
	last_run: {
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
	} | null;
	recent_runs: SyncStatus['last_run'][];
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

export const api = {
	metrics: () => get<{ timezone: string; metrics: MetricDef[] }>('/api/metrics'),
	summary: (date?: string) =>
		get<{ date: string; today: string; items: SummaryItem[] }>(
			`/api/summary${date ? `?date=${date}` : ''}`
		),
	daily: (metrics: string[], from: string, to: string) =>
		get<{ from: string; to: string; series: Record<string, DailyPoint[]> }>(
			`/api/daily?metrics=${encodeURIComponent(metrics.join(','))}&from=${from}&to=${to}`
		),
	sleep: (from: string, to: string) => get<{ rows: SleepRow[] }>(`/api/sleep?from=${from}&to=${to}`),
	workouts: (from: string, to: string) =>
		get<{ rows: WorkoutRow[] }>(`/api/workouts?from=${from}&to=${to}`),
	sources: () => get<{ sources: SourceRow[] }>('/api/sources'),
	syncStatus: (days = 30) => get<SyncStatus>(`/api/sync/status?days=${days}`)
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
