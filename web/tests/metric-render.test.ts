// 状态型 / 事件型的渲染形态。
//
// 区分的意义：状态型（体重、身高）测一次后这个值一直成立到下次测量，
// 画阶梯是对的；事件型（血糖、血压）每次是独立事件，中间没有「值」可言，
// 画阶梯就是在编数据。分错了用户会读出一个不存在的持续状态。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isEventMetric, stepStyle } from '../src/lib/metric-render.ts';

test('事件型指标登记在册', () => {
  for (const m of [
    'blood_glucose',
    'blood_pressure_systolic',
    'blood_pressure_diastolic',
    'peak_expiratory_flow_rate',
    'forced_vital_capacity',
    'six_minute_walking_test_distance'
  ]) {
    assert.equal(isEventMetric(m), true, `${m} 应为事件型`);
  }
});

test('状态型走默认：体重、身高、VO2max', () => {
  for (const m of ['body_mass', 'height', 'vo2_max', 'body_fat_percentage']) {
    assert.equal(isEventMetric(m), false, `${m} 应为状态型`);
  }
});

test('未登记的指标按状态型处理', () => {
  // 字典新增指标时不应退化成虚线：大多数身体指标本质是状态量
  assert.equal(isEventMetric('some_future_metric'), false);
});

test('状态型 → 阶梯实线；事件型 → 虚线不阶梯', () => {
  assert.deepEqual(stepStyle('body_mass'), { step: 'end', lineType: 'solid' });
  assert.deepEqual(stepStyle('blood_glucose'), { step: false, lineType: 'dashed' });
});
