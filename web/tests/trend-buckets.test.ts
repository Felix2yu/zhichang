// 分桶逻辑的回归测试。
//
// 这里最容易悄悄坏掉的是「累计量取日均」的口径——它和瞬时量的算式完全一样
// （都是 sum ÷ 有效天数），差别只在标注。一旦有人把标注改回「均值」，
// 周桶就会被读成 7 倍的日值，而类型检查和构建都不会报错。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  bucketKey,
  bucketLabel,
  bucketSeries,
  grainFor,
  type Grain
} from '../src/lib/trend-buckets.ts';

test('粒度阈值与睡眠页一致：45 / 180 是分界', () => {
  assert.equal(grainFor(1), 'day');
  assert.equal(grainFor(45), 'day');
  assert.equal(grainFor(46), 'week');
  assert.equal(grainFor(180), 'week');
  assert.equal(grainFor(181), 'month');
  assert.equal(grainFor(365), 'month');
});

test('周桶键取周一，不是周日', () => {
  // 2026-01-01 是周四，所在周的周一是 2025-12-29
  assert.equal(bucketKey('2026-01-01', 'week'), '2025-12-29');
  // 周日（2026-01-04）归属它前面那个周一，不能自己开一周
  assert.equal(bucketKey('2026-01-04', 'week'), '2025-12-29');
  assert.equal(bucketKey('2026-01-05', 'week'), '2026-01-05');
});

test('月桶键是 YYYY-MM；日粒度就是日期本身', () => {
  assert.equal(bucketKey('2026-01-15', 'month'), '2026-01');
  assert.equal(bucketKey('2026-01-15', 'day'), '2026-01-15');
  // 解析不了的日期原样返回，不让 NaN 污染分组键
  assert.equal(bucketKey('not-a-date', 'month'), 'not-a-date');
});

test('日粒度原样透传，每个点自己一桶', () => {
  const out = bucketSeries(
    [
      { date: '2026-01-02', value: 3 },
      { date: '2026-01-01', value: 1 }
    ],
    'day'
  );
  // 输入无需排序，输出必须升序，否则时间轴上会画出回折
  assert.deepEqual(out, [
    { key: '2026-01-01', value: 1, n: 1 },
    { key: '2026-01-02', value: 3, n: 1 }
  ]);
});

test('周桶取桶内均值，并带出有效天数', () => {
  const out = bucketSeries(
    [
      { date: '2026-01-01', value: 10 },
      { date: '2026-01-02', value: 20 },
      { date: '2026-01-03', value: 30 }
    ],
    'week'
  );
  assert.equal(out.length, 1);
  assert.equal(out[0].n, 3);
  assert.equal(out[0].value, 20);
});

test('缺数据的天少一个样本，而不是当成 0 拉低均值', () => {
  // 01-01 周四与 01-03 周六同属 2025-12-29 那一周；这一周有 7 天但只有 2 天有数据，
  // 就该除以 2，不是除以 7
  const out = bucketSeries(
    [
      { date: '2026-01-01', value: 10 },
      { date: '2026-01-03', value: 30 }
    ],
    'week'
  );
  assert.equal(out.length, 1);
  assert.equal(out[0].key, '2025-12-29');
  assert.equal(out[0].n, 2);
  assert.equal(out[0].value, 20);
});

test('整桶无数据不出桶：不插 0 柱', () => {
  const out = bucketSeries([{ date: '2026-01-01', value: 5 }], 'month');
  assert.deepEqual(out, [{ key: '2026-01', value: 5, n: 1 }]);
});

test('无效点被丢弃；全无效返回空', () => {
  assert.deepEqual(
    bucketSeries(
      [
        { date: '2026-01-01', value: 1 },
        { date: '2026-01-02', value: Number.NaN },
        { date: '', value: 9 }
      ],
      'day'
    ),
    [{ key: '2026-01-01', value: 1, n: 1 }]
  );
  assert.deepEqual(bucketSeries([], 'week'), []);
  assert.deepEqual(bucketSeries([{ date: 'x', value: Number.NaN }], 'week'), []);
});

test('桶标签：月显示年-月，周/日显示月-日', () => {
  assert.equal(bucketLabel('2026-01', 'month'), '2026-01');
  assert.equal(bucketLabel('2026-01-15', 'week'), '01-15');
  assert.equal(bucketLabel('2026-01-15', 'day'), '01-15');
});

test('三种粒度都能跑通，不抛异常', () => {
  const pts = [
    { date: '2026-01-01', value: 1 },
    { date: '2026-06-30', value: 2 },
    { date: '2026-12-31', value: 3 }
  ];
  for (const g of ['day', 'week', 'month'] as Grain[]) {
    assert.ok(bucketSeries(pts, g).length > 0, `粒度 ${g} 应有输出`);
  }
});
