// 指标的渲染形态覆盖表。
//
// 放在独立模块而不是塞进 metric-groups.ts：后者被
// internal/domain/metric/ui_contract_test.go 用正则解析，改动结构会让契约测试变红。
// 这里的映射纯属前端画图口味，与后端字典契约无关，应该分开。
//
// 区分的是两种「稀疏」：
//   - 状态型：测一次，这个值一直成立到下次测量（体重、身高、VO2max）。
//     两点之间画实线阶梯是对的——横线长度就是「这个状态保持了多久」。
//   - 事件型：每次是一次独立事件，中间没有「值」可言（血糖、血压、肺活量）。
//     画阶梯就是在编数据，应当画点 + 虚线轻连，不暗示中间存在取值。
//
// 未列出的指标按默认规则走（状态型），因为大多数「偶尔测一次」的身体指标
// 本质是状态量。只有确认是离散事件的才需要登记到下面。

/**
 * 空态的三种成因。
 *
 * 三种的下一步完全不同：从来没接过数据要去接数据源，今天还没同步只需等，
 * 区间内没有但历史有数据则应该把最近几次调出来看。混成一句「无记录」，
 * 用户就无从判断该做什么。
 */
export type EmptyKind = 'never' | 'pending' | 'stale';

/** 事件型指标：只画点、虚线连接，不用阶梯。 */
const EVENT_METRICS = new Set([
  'blood_glucose',
  'blood_pressure_systolic',
  'blood_pressure_diastolic',
  'peak_expiratory_flow_rate',
  'forced_vital_capacity',
  'six_minute_walking_test_distance',
  'sexual_activity',
  'handwashing',
  'state_of_mind'
]);

/** 是否为事件型指标（决定稀疏档用点列还是阶梯）。 */
export function isEventMetric(metric: string): boolean {
  return EVENT_METRICS.has(metric);
}

/**
 * 稀疏档的连接方式：状态型用阶梯实线，事件型用虚线。
 * 返回 ECharts lineStyle 需要的 type 与 step。
 */
export function stepStyle(metric: string): { step: 'end' | false; lineType: 'solid' | 'dashed' } {
  return isEventMetric(metric)
    ? { step: false, lineType: 'dashed' }
    : { step: 'end', lineType: 'solid' };
}
