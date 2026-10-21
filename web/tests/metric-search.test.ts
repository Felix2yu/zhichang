// 指标搜索的回归测试：中文子串 + 拼音首字母 + id 子串。
//
// 首字母表是手写的，新增指标时最容易漏的正是这一步，而漏了只能靠人眼发现。
// 所以最后一条用分组表（全量 51 项）做覆盖度断言：字典里每个指标都必须有首字母。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { matchMetric, pinyinOf } from '../src/lib/metric-search.ts';
import { METRIC_GROUPS } from '../src/lib/metric-groups.ts';

const def = (metric: string, display_name_zh: string) => ({ metric, display_name_zh });

test('空查询命中一切', () => {
  assert.equal(matchMetric(def('step_count', '步数'), ''), true);
  assert.equal(matchMetric(def('step_count', '步数'), '   '), true);
});

test('中文子串命中', () => {
  assert.equal(matchMetric(def('body_mass', '体重'), '体重'), true);
  assert.equal(matchMetric(def('body_mass', '体重'), '重'), true);
  assert.equal(matchMetric(def('step_count', '步数'), '体重'), false);
});

test('指标 id 子串命中', () => {
  assert.equal(matchMetric(def('resting_heart_rate', '静息心率'), 'resting'), true);
  assert.equal(matchMetric(def('resting_heart_rate', '静息心率'), 'heart'), true);
  assert.equal(matchMetric(def('resting_heart_rate', '静息心率'), 'zzz'), false);
});

test('拼音首字母命中：tz → 体重，xt → 血糖', () => {
  assert.equal(matchMetric(def('body_mass', '体重'), 'tz'), true);
  assert.equal(matchMetric(def('blood_glucose', '血糖'), 'xt'), true);
  assert.equal(matchMetric(def('body_mass', '体重'), 'sg'), false);
});

test('首字母是前缀匹配：输到哪一位都能命中', () => {
  assert.equal(matchMetric(def('six_minute_walking_test_distance', '六分钟步行距离'), 'l'), true);
  assert.equal(matchMetric(def('six_minute_walking_test_distance', '六分钟步行距离'), 'lfz'), true);
  assert.equal(
    matchMetric(def('six_minute_walking_test_distance', '六分钟步行距离'), 'lfzz'),
    false
  );
});

test('中文输入不走首字母，避免误命中', () => {
  // 「体」是中文，首字母表是用 ASCII 前缀匹配的，不该掺和进来
  assert.equal(matchMetric(def('body_mass', '体重'), '体'), true);
  assert.equal(matchMetric(def('blood_glucose', '血糖'), '体'), false);
});

test('大小写不敏感', () => {
  assert.equal(matchMetric(def('vo2_max', '最大摄氧量'), 'VO2'), true);
  assert.equal(matchMetric(def('body_mass', '体重'), 'TZ'), true);
});

test('字典里每个指标都有拼音首字母', () => {
  const all = [...new Set(METRIC_GROUPS.flatMap((g) => g.metrics))];
  assert.equal(all.length, 51, '分组表应覆盖全部 51 项指标');
  const missing = all.filter((m) => pinyinOf(m) === '');
  assert.deepEqual(missing, [], `缺首字母的指标：${missing.join(', ')}`);
});
