/**
 * 睡眠分期的展示元数据。
 *
 * 阶段键由后端归一化而来（见 internal/store/postgres/query.go 的 normalizeSleepStage），
 * 顺序即时间线自上而下的顺序，与 Apple 健康一致：清醒 → REM → 核心 → 深睡。
 */

export type StageKey = 'awake' | 'rem' | 'core' | 'deep' | 'asleep' | 'inbed' | 'other';

export type StageMeta = {
  key: StageKey;
  label: string;
  /** 窄屏（移动端）用的短标签 */
  short: string;
  color: string;
  /** 是否参与时间线渲染：在床（inbed）不是分期阶段，只用于「在床时长」统计 */
  drawn: boolean;
};

export const STAGE_META: StageMeta[] = [
  { key: 'awake', label: '清醒', short: '清醒', color: '#e8705a', drawn: true },
  { key: 'rem', label: '快速眼动', short: 'REM', color: '#7cc3e8', drawn: true },
  { key: 'core', label: '核心睡眠', short: '核心', color: '#3f7fc4', drawn: true },
  { key: 'deep', label: '深度睡眠', short: '深睡', color: '#274a7d', drawn: true },
  // 未分期：旧设备或手动记录只给「Asleep」而没有细分，单独一层避免被当成核心睡眠
  { key: 'asleep', label: '睡眠（未分期）', short: '未分期', color: '#9db8d8', drawn: true },
  { key: 'inbed', label: '在床', short: '在床', color: '#cfd6e0', drawn: false },
  { key: 'other', label: '其他', short: '其他', color: '#c2c8d2', drawn: false }
];

export function stageMeta(key: string): StageMeta {
  return (
    STAGE_META.find((s) => s.key === key) ?? {
      key: 'other',
      label: key || '其他',
      short: key || '其他',
      color: '#c2c8d2',
      drawn: false
    }
  );
}

export function stageLabel(key: string): string {
  return stageMeta(key).label;
}
