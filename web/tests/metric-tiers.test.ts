// 档位与节奏的回归测试。
//
// 两条规则是本项目特有的、外面查不到的，也是最容易在后续「优化」中被改坏的：
//   1. 只看点数会错，点数够但节奏慢（中位间隔 > 9 天）必须降为阶梯档；
//   2. 节奏念中位间隔的真实天数，不套「每周/每月」这种粗糙分档。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  TIER_CHART_HEIGHT,
  cadenceText,
  gapDays,
  gapsOf,
  medianGap,
  tierOf,
  tierOfPoints
} from '../src/lib/metric-tiers.ts';

const DAILY = [
  '2026-03-01',
  '2026-03-02',
  '2026-03-03',
  '2026-03-04',
  '2026-03-05',
  '2026-03-06',
  '2026-03-07',
  '2026-03-08',
  '2026-03-09'
];

// 一年里测三次：1-03、3-28、8-12
const SPARSE = ['2026-01-03', '2026-03-28', '2026-08-12'];

test('按点数定档的基本分界', () => {
  assert.equal(tierOf(0), 'empty');
  assert.equal(tierOf(-1), 'empty');
  assert.equal(tierOf(Number.NaN), 'empty');
  assert.equal(tierOf(1), 'single');
  assert.equal(tierOf(2), 'step');
  assert.equal(tierOf(5), 'step');
  assert.equal(tierOf(6), 'points');
  assert.equal(tierOf(23), 'points');
  assert.equal(tierOf(24), 'continuous');
});

test('间隔计算按 UTC 整天，不受时区影响', () => {
  assert.equal(gapDays('2026-01-03', '2026-03-28'), 84);
  assert.equal(gapDays('2026-03-28', '2026-08-12'), 137);
  assert.equal(gapDays('2026-01-01', '2026-01-01'), 0);
  assert.equal(gapDays('bad', '2026-01-01'), null);
});

test('gapsOf 去重排序后再算间隔', () => {
  assert.deepEqual(gapsOf(['2026-01-05', '2026-01-01', '2026-01-03']), [2, 2]);
  assert.deepEqual(gapsOf(['2026-01-01']), []);
});

test('中位间隔：奇数取中间，偶数取中间两项均值', () => {
  assert.equal(medianGap(DAILY), 1);
  // 84 与 137 的中位 = (84+137)/2 = 110.5 → 111
  assert.equal(medianGap(SPARSE), 111);
  assert.equal(medianGap(['2026-01-01']), null);
});

test('节奏念真实天数，不说「每月」', () => {
  assert.equal(cadenceText(DAILY), '每天');
  assert.equal(cadenceText(SPARSE), '每 111 天一次');
  assert.equal(cadenceText(['2026-01-01']), null);
});

test('节奏慢的一律降档：9 个点但跨一整年，不能画折线', () => {
  // 点数 9 落在「点线图」区间，但中位间隔 111 天远超 9 天阈值
  assert.equal(tierOf(9), 'points');
  assert.equal(tierOfPoints(9, SPARSE), 'step');
});

test('节奏快的不降档：同样 9 个点、逐日记录，画折线是对的', () => {
  assert.equal(tierOfPoints(9, DAILY), 'points');
});

test('降档只作用于点线档：单次与空态不受节奏影响', () => {
  assert.equal(tierOfPoints(1, ['2026-01-01']), 'single');
  assert.equal(tierOfPoints(0, []), 'empty');
  // 连续档 24 点以上本就是趋势，不降
  assert.equal(tierOfPoints(300, DAILY), 'continuous');
});

test('不画图的两档图区高度为 0，避免留白', () => {
  assert.equal(TIER_CHART_HEIGHT.single, 0);
  assert.equal(TIER_CHART_HEIGHT.empty, 0);
  // 稀疏档把空间让给日期与间隔标注
  assert.ok(TIER_CHART_HEIGHT.step < TIER_CHART_HEIGHT.continuous);
});
