// 睡眠评分——按 Apple 公开的口径自算。
//
// 背景：watchOS 26 起 Apple Watch 会给出 0~100 的「睡眠评分」，但 HealthKit
// **至今没有开放任何相关 API**（Apple 开发者关系部 2025-09 的官方回复：
// 「As of today, there is no API that allows the access to sleep scores」），
// 评分在设备端计算，因此任何第三方 App / 导出工具（含 Health Auto Export）
// 都取不到这个数，本应用的导入数据里也没有它。想要就得自己算。
//
// Apple 公开的权重（三项相加 = 100）：
//   睡眠时长 50 分 + 就寝规律性 30 分（比对最近 13 晚的入睡时间）+ 中断 20 分
//   档位：0-40 非常低 / 41-60 低 / 61-80 一般 / 81-95 高 / 96+ 非常高
//
// 重要预期：这里是「同口径、同权重、同方向」的复刻，**不会与 iPhone 上显示的
// 数字逐分吻合**。Apple 的算法未公开（官方称用 500 万个夜晚的睡眠数据训练），
// 且是「与你自己的历史比」的个性化评分，我们无法反推出它的曲线。
//
// 两项个性化基准都取自你自己的历史（与 Apple 的思路一致）：
//   - 时长基准：该晚之前最多 30 晚实际睡眠时长的中位数
//   - 就寝基准：该晚之前最多 13 晚入睡时刻的中位数
//
// 本模块是纯函数，不碰 DOM / 网络，便于单独核对数值。

/** 一晚的睡眠输入（同一天多来源已收敛）。 */
export type SleepNight = {
  local_date: string;
  total_sleep_hr?: number | null;
  asleep_hr?: number | null;
  in_bed_hr?: number | null;
  awake_hr?: number | null;
  /** UTC RFC3339 */
  sleep_start?: string | null;
  /** 本地时区偏移（分钟）：把 sleep_start 还原成本地入睡时刻 */
  tz_offset_min?: number | null;
  /** 夜间清醒段数；仅当导出含睡眠分段数据时才有值 */
  awake_count?: number | null;
};

/** 三项权重（合计 100），与 Apple 公开口径一致。 */
export const COMPONENT_MAX = { duration: 50, bedtime: 30, interruption: 20 } as const;

/** 时长基准取该晚之前最近 30 晚；至少要有 5 晚才认为基准可用。 */
const DURATION_BASELINE_NIGHTS = 30;
const DURATION_MIN_NIGHTS = 5;
/** 就寝基准取最近 13 晚（Apple 口径）。 */
const BEDTIME_BASELINE_NIGHTS = 13;
const BEDTIME_MIN_NIGHTS = 5;

/** 时长：实际/基准 低于 0.6 时该单项 0 分（比值 1 及以上满分）。 */
const DURATION_RATIO_MIN = 0.6;
/** 就寝：偏差 ≤30 分钟不扣分，≥120 分钟扣光。 */
const BEDTIME_FREE_MIN = 30;
const BEDTIME_MAX_MIN = 120;
/** 中断：清醒时长占在床时长 ≤2% 不扣分，≥18% 扣光。
 *  换成「睡眠效率」即 98% 以上满分、82% 以下零分——常见的 85~95% 落在中间段，
 *  不会因为「正常入睡耗时」就被扣掉大半。 */
const AWAKE_RATIO_FREE = 0.02;
const AWAKE_RATIO_MAX = 0.18;
/** 中断：清醒 ≤1 次不扣「次数」分，≥5 次扣光。 */
const AWAKE_COUNT_FREE = 1;
const AWAKE_COUNT_MAX = 5;

/** 中断项的数据来源，用于在界面上说明依据。 */
export type InterruptionSource = 'segments' | 'awake-hr' | 'in-bed-diff';

export type SleepScore = {
  local_date: string;
  /** 总分 0~100；历史不足或当晚无睡眠时长时为 null */
  score: number | null;
  duration: number | null;
  bedtime: number | null;
  interruption: number | null;

  // —— 各项依据，供界面展示「为什么是这个分」——
  sleepHr: number | null;
  baselineHr: number | null;
  bedtimeMin: number | null;
  bedtimeBaselineMin: number | null;
  /** 就寝基准是用户设定的预期入睡时间（true）还是最近 13 晚中位数（false）。
   *  界面上「基准 22:30」和「目标 22:30」是两回事，标错等于骗人。 */
  bedtimeBaselineIsTarget: boolean;
  bedtimeDeviationMin: number | null;
  awakeHr: number | null;
  awakeCount: number | null;
  interruptionSource: InterruptionSource | null;
};

export type SleepBand = { label: string; color: string };

/** 档位（分档与 Apple 一致）。 */
export function bandOf(score: number): SleepBand {
  if (score >= 96) return { label: '非常高', color: '#0f6e56' };
  if (score >= 81) return { label: '高', color: '#2f8f6f' };
  if (score >= 61) return { label: '一般', color: '#b07a2a' };
  if (score >= 41) return { label: '低', color: '#b0563a' };
  return { label: '非常低', color: '#a32d2d' };
}

function clamp01(v: number): number {
  return Math.min(1, Math.max(0, v));
}

function median(xs: number[]): number | null {
  if (xs.length === 0) return null;
  const s = [...xs].sort((a, b) => a - b);
  const m = s.length >> 1;
  return s.length % 2 === 1 ? s[m] : (s[m - 1] + s[m]) / 2;
}

/**
 * 同一晚可能被 iPhone / Apple Watch 各记一条（sleep_sessions 按
 * (local_date, source, sleep_start) 唯一），也可能有「小睡 + 主睡眠」多条。
 * 一律按项目既有惯例收敛：取当晚总睡眠最长的那条。
 */
export function collapseNights<T extends SleepNight>(rows: T[]): T[] {
  const byDate = new Map<string, T>();
  for (const r of rows) {
    const cur = byDate.get(r.local_date);
    if (!cur || (r.total_sleep_hr ?? -1) > (cur.total_sleep_hr ?? -1)) byDate.set(r.local_date, r);
  }
  return [...byDate.values()].sort((a, b) => a.local_date.localeCompare(b.local_date));
}

/**
 * 入睡时刻 → 相对「当日 18:00」的分钟数（0..1439）。
 * 以 18:00 为分界是为了让 23:50 与 00:10 只差 20 分钟（330 与 370），
 * 而不是差 1430 分钟；同时把凌晨入睡自然地排到深夜之后。
 */
export function bedtimeMinutes(sleepStart: string, tzOffsetMin = 0): number | null {
  const ms = Date.parse(sleepStart);
  if (!Number.isFinite(ms)) return null;
  const d = new Date(ms + tzOffsetMin * 60_000);
  const minsOfDay = d.getUTCHours() * 60 + d.getUTCMinutes();
  return (((minsOfDay - 1080) % 1440) + 1440) % 1440;
}

/** 相对 18:00 的分钟数 → 钟点 'HH:MM'。 */
export function minutesToClock(rel: number): string {
  const mins = (((rel + 1080) % 1440) + 1440) % 1440;
  return `${String(Math.floor(mins / 60)).padStart(2, '0')}:${String(mins % 60).padStart(2, '0')}`;
}

/** 钟点 'HH:MM' → 相对 18:00 的分钟数（上面那个的逆向，同一根轴）。
 *  解析不了回 null，让调用方决定「没设目标」而不是「目标是 0 点」。 */
export function clockToBedtime(clock: string): number | null {
  const m = /^(\d{2}):(\d{2})$/.exec(clock.trim());
  if (!m) return null;
  const mins = Number(m[1]) * 60 + Number(m[2]);
  if (mins >= 1440) return null;
  return (((mins - 1080) % 1440) + 1440) % 1440;
}

/** 环形展开：把一组时刻平移到基准 ref 附近，消除跨零点造成的中位数失真。 */
function unwrap(xs: number[], ref: number): number[] {
  return xs.map((v) => v + 1440 * Math.round((ref - v) / 1440));
}

/**
 * 逐晚计算评分。传入的 nights 需已按日期升序（先用 collapseNights 收敛来源）。
 * 返回数组与入参一一对应；历史不足的夜晚 score 为 null（而不是给个猜的值）。
 */
export type SleepScoreOptions = {
  /** 用户设定的预期入睡时间，相对 18:00 的分钟数（用 bedtimeMinutes 同一坐标轴）。
   *  给了就把它当就寝基准——用户问的是「我有没有守住自己定的点」，
   *  而不是「我和过去的自己比有没有跑偏」；不给则维持最近 13 晚中位数的老口径。 */
  bedtimeTargetMin?: number | null;
};

export function computeSleepScores(nights: SleepNight[], opts?: SleepScoreOptions): SleepScore[] {
  const bedtimes = nights.map((n) =>
    n.sleep_start ? bedtimeMinutes(n.sleep_start, n.tz_offset_min ?? 0) : null
  );
  const allSleepHr = nights
    .map((n) => n.total_sleep_hr)
    .filter((v): v is number => v != null && v > 0);

  return nights.map((n, i) => {
    // —— 1. 睡眠时长（50 分）：与自己的 30 晚中位数比 ——
    const sleepHr = n.total_sleep_hr != null && n.total_sleep_hr > 0 ? n.total_sleep_hr : null;
    const priorSleep: number[] = [];
    for (let j = Math.max(0, i - DURATION_BASELINE_NIGHTS); j < i; j++) {
      const v = nights[j].total_sleep_hr;
      if (v != null && v > 0) priorSleep.push(v);
    }
    // 前置窗口不足（新用户前几天）时退回全量中位数，仍不足则判定为历史不足
    const pool = priorSleep.length >= DURATION_MIN_NIGHTS ? priorSleep : allSleepHr;
    const baselineHr = pool.length >= DURATION_MIN_NIGHTS ? median(pool) : null;
    const duration =
      sleepHr != null && baselineHr != null && baselineHr > 0
        ? COMPONENT_MAX.duration *
          clamp01((sleepHr / baselineHr - DURATION_RATIO_MIN) / (1 - DURATION_RATIO_MIN))
        : null;

    // —— 2. 就寝规律性（30 分）：与最近 13 晚的入睡中位数比 ——
    const todayBed = bedtimes[i];
    const priorBed: number[] = [];
    for (let j = Math.max(0, i - BEDTIME_BASELINE_NIGHTS); j < i; j++) {
      const b = bedtimes[j];
      if (b != null) priorBed.push(b);
    }
    let bedtime: number | null = null;
    let bedtimeBaselineMin: number | null = null;
    let bedtimeDeviationMin: number | null = null;
    let baseIsTarget = false;
    const targetMin = opts?.bedtimeTargetMin;
    // 设了预期入睡时间就不必等满 13 晚历史：头一晚也能对着自己定的点打分
    if (todayBed != null && (targetMin != null || priorBed.length >= BEDTIME_MIN_NIGHTS)) {
      let base: number | null;
      if (targetMin != null && Number.isFinite(targetMin)) {
        base = ((targetMin % 1440) + 1440) % 1440;
        baseIsTarget = true;
      } else {
        base = median(unwrap(priorBed, priorBed[priorBed.length - 1]));
      }
      if (base != null) {
        const dev = Math.abs(unwrap([todayBed], base)[0] - base);
        bedtimeDeviationMin = Math.round(dev);
        bedtimeBaselineMin = ((base % 1440) + 1440) % 1440;
        bedtime =
          COMPONENT_MAX.bedtime *
          clamp01(1 - (dev - BEDTIME_FREE_MIN) / (BEDTIME_MAX_MIN - BEDTIME_FREE_MIN));
      }
    }

    // —— 3. 中断（20 分）：有分段数据就拆成「次数 + 时长」，否则只用清醒占比 ——
    let awakeHr = n.awake_hr ?? null;
    let source: InterruptionSource | null = awakeHr != null ? 'awake-hr' : null;
    const asleep = n.asleep_hr ?? sleepHr;
    if (awakeHr == null && n.in_bed_hr != null && asleep != null) {
      awakeHr = Math.max(0, n.in_bed_hr - asleep);
      source = 'in-bed-diff';
    }
    const inBed = n.in_bed_hr ?? (asleep != null ? asleep + (awakeHr ?? 0) : null);
    const ratio = awakeHr != null && inBed != null && inBed > 0 ? clamp01(awakeHr / inBed) : 0;
    const count = n.awake_count ?? null;

    let interruption: number;
    if (count != null) {
      const half = COMPONENT_MAX.interruption / 2;
      interruption =
        half * clamp01(1 - (count - AWAKE_COUNT_FREE) / (AWAKE_COUNT_MAX - AWAKE_COUNT_FREE)) +
        half * clamp01(1 - (ratio - AWAKE_RATIO_FREE) / (AWAKE_RATIO_MAX - AWAKE_RATIO_FREE));
      source = 'segments';
    } else {
      interruption =
        COMPONENT_MAX.interruption *
        clamp01(1 - (ratio - AWAKE_RATIO_FREE) / (AWAKE_RATIO_MAX - AWAKE_RATIO_FREE));
    }

    // 时长与就寝两项缺一不可，否则不给分（避免用残缺信息编造一个总分）
    const score =
      duration != null && bedtime != null
        ? Math.max(0, Math.min(100, Math.round(duration + bedtime + interruption)))
        : null;

    return {
      local_date: n.local_date,
      score,
      duration,
      bedtime,
      interruption,
      sleepHr,
      baselineHr,
      bedtimeMin: todayBed,
      bedtimeBaselineMin,
      bedtimeBaselineIsTarget: baseIsTarget,
      bedtimeDeviationMin,
      awakeHr,
      awakeCount: count,
      interruptionSource: source
    };
  });
}
