// 频率档的回归测试。
//
// 关键是「窗口取 365 天、阈值按窗口比例算」这一条。曾经用 90 天窗口，
// 把「上次测量在 224 天前」的肺活量判成「从未接入」——一年测两次对它完全正常，
// 把它和真正没接过的指标归为一类，恰好是这个分区要解决的问题的反面。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  FREQ_WINDOW_DAYS,
  agoText,
  buildCoverage,
  freqBounds,
  freqOf
} from '../src/lib/metric-frequency.ts';

test('默认窗口是一年，不是 90 天', () => {
  assert.equal(FREQ_WINDOW_DAYS, 365);
});

test('阈值按窗口比例算，改窗口长度结论自动跟着变', () => {
  assert.deepEqual(freqBounds(365), { daily: 183, regular: 24 });
  assert.deepEqual(freqBounds(90), { daily: 45, regular: 6 });
});

test('365 天窗口下的四档分界', () => {
  assert.equal(freqOf(0), 'never');
  assert.equal(freqOf(-1), 'never');
  assert.equal(freqOf(Number.NaN), 'never');
  assert.equal(freqOf(1), 'occasional');
  assert.equal(freqOf(23), 'occasional');
  assert.equal(freqOf(24), 'regular');
  assert.equal(freqOf(182), 'regular');
  assert.equal(freqOf(183), 'daily');
  assert.equal(freqOf(365), 'daily');
});

test('一年只测两次的指标是偶发，不是未接入', () => {
  // 这是改成 365 天窗口的直接原因：90 天窗口下它会落到 never
  const cov = buildCoverage(
    [{ metric: 'forced_vital_capacity' }],
    [{ metric: 'forced_vital_capacity', days: 2, last_date: '2026-03-11', days_since: 224 }]
  );
  const c = cov.get('forced_vital_capacity');
  assert.equal(c?.freq, 'occasional');
  assert.equal(c?.daysSince, 224);
  assert.equal(c?.lastDate, '2026-03-11');
});

test('未接入靠字典与 coverage 做差集补齐', () => {
  const cov = buildCoverage(
    [{ metric: 'step_count' }, { metric: 'blood_glucose' }],
    [{ metric: 'step_count', days: 300, last_date: '2026-10-20', days_since: 1 }]
  );
  assert.equal(cov.get('step_count')?.freq, 'daily');
  const bg = cov.get('blood_glucose');
  assert.equal(bg?.freq, 'never');
  assert.equal(bg?.days, 0);
  assert.equal(bg?.lastDate, '');
  assert.equal(bg?.daysSince, null);
});

test('coverage 为空时全部落到未接入，选择器仍可用', () => {
  const cov = buildCoverage([{ metric: 'step_count' }], []);
  assert.equal(cov.get('step_count')?.freq, 'never');
});

test('days_since 为 -1（后端解析失败）按未接入处理', () => {
  const cov = buildCoverage(
    [{ metric: 'step_count' }],
    [{ metric: 'step_count', days: 5, last_date: '', days_since: -1 }]
  );
  assert.equal(cov.get('step_count')?.daysSince, null);
});

test('年龄文案', () => {
  assert.equal(agoText(0), '今天');
  assert.equal(agoText(1), '昨天');
  assert.equal(agoText(12), '12 天前');
  assert.equal(agoText(null), null);
});
