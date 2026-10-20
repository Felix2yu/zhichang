// 心电分析页的共享口径：指标档位、颜色、质量与节律文案、指标释义。
//
// 与服务端 internal/domain/ecg 的档位函数（HRVLevel/IndexLevel/RiskLevel）
// 和打分函数一一对应——口径属于指标语义的一部分，前后端各写一份必然漂移，
// 所以这里只放「展示侧」的映射，打分本身全在服务端。
import { dataColor } from '#lib/theme.svelte';

export type EcgMetricKey = 'hrv' | 'stress' | 'fatigue' | 'risk';

/** 每个可解释指标的元信息。文案会原样展示给用户，写成人话。 */
export const ECG_METRICS: Record<
  EcgMetricKey,
  {
    title: string;
    /** 越大越好（hrv）还是越小越好（其余）——决定仪表条渐变方向与增减配色 */
    higherIsBetter: boolean;
    /** 含义 */
    meaning: string;
    /** 怎么算 */
    formula: string;
    /** 可信度 */
    trust: string;
    /** 怎么看 */
    hint: string;
  }
> = {
  hrv: {
    title: '心率变异性指数',
    higherIsBetter: true,
    meaning:
      '相邻两次心跳之间的时间差异有多大。差异大说明自主神经对心脏的调节更灵活，通常代表恢复能力更好。',
    formula:
      '从本次测量的心电波形里逐个找出心跳（R 峰），计算相邻心跳间隔的均方根（RMSSD），再按 10ms 记 0 分、90ms 记 100 分折算成 0-100。',
    trust:
      '中等。基于 Apple 心电测量自带波形的标准时域算法，每次测量约 30 秒；短时测量的数值波动比 24 小时动态心电图大，适合看趋势，不适合抠单次绝对值。',
    hint: '和过去的自己比：持续走低往往对应疲劳、压力或恢复不足；运动人群天然偏高。'
  },
  stress: {
    title: '精神压力指数',
    higherIsBetter: false,
    meaning: '当前自主神经的张力状态。交感神经偏兴奋（紧张、应激）时心跳间隔更趋一致，指数升高。',
    formula:
      '用 Baevsky 压力指数：对心跳间隔做直方图统计，按「众数占比 ÷ 间隔分布范围」计算，再折算成 0-100。',
    trust:
      '中等偏低。这是学术界常用的自主神经评估方法，但 30 秒样本的直方图较粗，且个体差异大；只看相对变化，不要跨人比较。',
    hint: '测量前刚运动、喝咖啡或情绪激动时会偏高；安静休息几分钟后再测更可比。'
  },
  fatigue: {
    title: '疲劳指数',
    higherIsBetter: false,
    meaning:
      '恢复是否跟上的综合倾向。心率变异性低、压力指数高、静息心率偏高，都指向「身体还没恢复过来」。',
    formula:
      '经验复合值 = 45% × (100 − 心率变异性) + 30% × 压力指数 + 25% × 静息心率负荷（55 次/分记 0、100 次/分记 100）。',
    trust:
      '参考级。没有临床金标准的复合指标，权重是经验值；它的价值在于把一个综合感受变成可追踪的单值。',
    hint: '连续几天走高，值得留意睡眠与休息安排；偶尔一次升高不必紧张。'
  },
  risk: {
    title: '心脏疾病风险关注度',
    higherIsBetter: false,
    meaning:
      '这些测量里有没有值得留意的信号（房颤提示、心率过快、测不准），以及心率变异性是否偏低。它是筛查关注度，不是患病概率。',
    formula:
      '透明规则：出现房颤提示从 55 分起评并按占比加权；高心率、测不准、心率变异性偏低按比例加分。触发项会原样列在卡片下方。',
    trust:
      '参考级。Apple 心电每次只测约 30 秒，间歇性房颤可能整段都没被测到；关注度高请就医，关注度低也不能排除问题。',
    hint: '把触发项当「该做什么」的清单：有房颤提示就尽早就诊；只有「记录欠佳」则重测即可。'
  }
};

/** 档位 → 颜色。色值与 app.css 的 token 一致；暗底下由 dataColor 提亮度。 */
const BRAND = '#0f6e56';
const WARN = '#8a6d1f';
const DANGER = '#a32d2d';

/** 取某个档位的展示色（已按主题提亮）。 */
export function levelColor(key: EcgMetricKey, level: string): string {
  const meta = ECG_METRICS[key];
  let hex: string;
  if (meta.higherIsBetter) {
    // 越高越好：低分是坏消息
    hex = level === '偏低' ? DANGER : level === '一般' ? WARN : BRAND;
  } else {
    // 越低越好：低分是好消息
    hex = level === '低' ? BRAND : level === '中' || level === '需关注' ? WARN : DANGER;
  }
  return dataColor(hex);
}

/** 判定码 → 展示色（分布图、印象卡时间点、记录表判定列共用）。 */
const CODE_COLORS: Record<string, string> = {
  sinus_rhythm: BRAND,
  atrial_fibrillation: DANGER,
  high_heart_rate: WARN,
  inconclusive: '#6b7280',
  inconclusive_low_heart_rate: '#6b7280',
  inconclusive_high_heart_rate: '#6b7280',
  inconclusive_poor_recording: '#9aa0a6',
  unrecognized: '#9aa0a6',
  unknown: '#b6bac1'
};

export function codeColor(code: string): string {
  return dataColor(CODE_COLORS[code] ?? CODE_COLORS.unknown);
}

/** 信号质量文案（good/fair/poor/none） */
export const QUALITY_LABELS: Record<string, string> = {
  good: '良好',
  fair: '一般',
  poor: '不佳',
  none: '无波形'
};

/** 节律文案（regular/mild/irregular） */
export const RHYTHM_LABELS: Record<string, string> = {
  regular: '整齐',
  mild: '略有波动',
  irregular: '不齐'
};
