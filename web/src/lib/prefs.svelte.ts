// 本机偏好：存在浏览器 localStorage，换设备或清缓存要重设。
//
// 与「服务端设置」的分工要看清楚：睡眠目标这类是**数据口径**，跨设备要一致，
// 所以走 /api/settings + 令牌；这里的全是**口味**，只影响本机怎么显示。
// 主题另见 lib/theme.svelte.ts（同一套约定的第一个键）。
export type DurationFormat = 'cn' | 'decimal' | 'clock';

export const RANGE_OPTIONS = [7, 30, 90, 365] as const;

const RANGE_KEY = 'zhichang.default-range';
const DURATION_KEY = 'zhichang.duration-format';

/** 概览与趋势页各自的指标选择：键名归这里统管，重置才可能一次清干净。 */
export const FOCUS_STORE_KEY = 'zhichang.focus-metrics';
export const TREND_STORE_KEY = 'zhichang.trend-metrics';

/** 重置本机偏时要抹掉的键。不含 zhichang.settings-token——那是 sessionStorage，
 *  关标签页自己就没了，而且用户可能正授权着，不该被「恢复默认显示」顺手登出。 */
export const LOCAL_PREF_KEYS = [
	'zhichang.theme',
	RANGE_KEY,
	DURATION_KEY,
	FOCUS_STORE_KEY,
	TREND_STORE_KEY
];

let rangeDays = $state<number>(readRange());
let durationFmt = $state<DurationFormat>(readDuration());

function readRange(): number {
	try {
		const n = Number(localStorage.getItem(RANGE_KEY));
		if (RANGE_OPTIONS.includes(n as (typeof RANGE_OPTIONS)[number])) return n;
	} catch {
		/* 读不到按默认 */
	}
	return 30;
}

function readDuration(): DurationFormat {
	try {
		const v = localStorage.getItem(DURATION_KEY);
		if (v === 'cn' || v === 'decimal' || v === 'clock') return v;
	} catch {
		/* 读不到按默认 */
	}
	return 'cn';
}

export const prefs = {
	get rangeDays(): number {
		return rangeDays;
	},
	set rangeDays(days: number) {
		if (!RANGE_OPTIONS.includes(days as (typeof RANGE_OPTIONS)[number])) return;
		rangeDays = days;
		persist(RANGE_KEY, String(days));
	},
	get durationFormat(): DurationFormat {
		return durationFmt;
	},
	set durationFormat(f: DurationFormat) {
		durationFmt = f;
		persist(DURATION_KEY, f);
	}
};

function persist(key: string, value: string) {
	try {
		localStorage.setItem(key, value);
	} catch {
		/* 隐私模式存不下，不影响本次浏览 */
	}
}

/** 抹掉本机偏好，回到出厂显示。返回是否真的清掉了东西。 */
export function resetLocalPrefs(): void {
	try {
		for (const k of LOCAL_PREF_KEYS) localStorage.removeItem(k);
	} catch {
		/* 清不掉也没什么可报的 */
	}
	rangeDays = readRange();
	durationFmt = readDuration();
}

/** 分钟 → 用户选的时长写法。各页以前各有三套念法（「9 小时 12 分」/「9.2 小时」/「9.1 h」），
 *  同一晚的睡眠在两张卡上数字不一样，很容易被当成数据错了。 */
export function formatDuration(mins: number | null | undefined): string {
	if (mins === null || mins === undefined || !Number.isFinite(mins)) return '—';
	const m = Math.max(0, Math.round(mins));
	const h = Math.floor(m / 60);
	const r = m % 60;
	switch (durationFmt) {
		case 'decimal':
			return `${(m / 60).toFixed(1)} 小时`;
		case 'clock':
			return r === 0 ? `${h}h` : h === 0 ? `${m}m` : `${h}h ${r}m`;
		default:
			if (m < 60) return `${m} 分钟`;
			return r === 0 ? `${h} 小时` : `${h} 小时 ${r} 分`;
	}
}

/** 天级睡眠时长是「小时」这个浮点，进这里统一按分钟口径格式化 */
export function formatDurationHours(hr: number | null | undefined): string {
	if (hr === null || hr === undefined || !Number.isFinite(hr)) return '—';
	return formatDuration(hr * 60);
}

/** 带单位的指标值：时长单位按上面的偏好念，其余返回 null 让调用方走通用格式化。
 *  「7.8 hr / 7.5 hr」这种写法在中文界面里既难读又和别处的「7 小时 48 分」对不上。 */
export function formatDurationUnit(value: number | null | undefined, unit: string): string | null {
	if (value === null || value === undefined || !Number.isFinite(value)) return null;
	if (unit === 'hr' || unit === 'h') return formatDuration(value * 60);
	if (unit === 'min') return formatDuration(value);
	return null;
}
