-- 来源名归一：把「肉眼一样、字节不一样」的历史来源收敛掉。
--
-- 背景：HAE 写出的设备名用的是 U+00A0（不换行空格）。实测真实导出里
-- 249 个点位的来源全是「小明的Apple\u00a0Watch」。它与普通空格版
-- 「小明的Apple Watch」在界面上一模一样，却是两个不同的字符串：
--   - /sync 页的「数据来源」会多出「看起来重复」的一行，样本数被劈成两半；
--   - 去重唯一索引 (start_time, metric, source, value) 会因为 source 不同
--     而放行本该判为同一份数据的点。
-- HAE 还会把多来源拼成 "小明的Apple Watch|小明的iPhone"，且管两侧的空格
-- 不固定（实测 "A | B" 与 "A|B" 并存），同样会把一组来源分裂成两行。
--
-- 入库侧已由 sample.CleanSource 收敛（见 internal/domain/sample/source.go），
-- 但历史行仍带脏值，所以这里把已有数据也搬一遍。
--
-- ⚠️ 归一表达式必须与 sample.CleanSource 等价：两边不一致会让「历史数据」
-- 与「新入库数据」再次分裂。一致性由
-- internal/store/postgres/source_migration_test.go 在真库上逐用例比对锁定。
--
-- 冲突处理：归一后可能撞上去重唯一索引或唯一约束——撞上说明新旧两行是同一份
-- 数据，删旧留新，不让唯一约束把整份迁移打回去（与 0004 同一取舍）。
--
-- 天级聚合（daily_metrics / daily_metrics_day）**不在此处手工改写**：
-- Recompute 是「先按 (日期×指标) 整体 DELETE 再重建」，所以只要把受影响的
-- (日期×指标) 丢进重算队列，它们就会按归一后的来源重建，不需要合并逻辑。
--
-- 幂等：没有脏值时每条语句都是空操作，可以安全重放。

-- 与 sample.CleanSource 等价的 SQL 实现。先用零宽字符删除、再把所有
-- White_Space 类空白（NBSP / 表意空格 / 各种窄空格 / 制表换行）归一为普通空格、
-- 压缩连续空格、最后统一多来源分隔符。用完即删，不在库里留第二份事实来源。
CREATE OR REPLACE FUNCTION zhichang_clean_source(s text) RETURNS text
LANGUAGE sql AS $$
    SELECT COALESCE(
        array_to_string(
            array_remove(
                string_to_array(
                    regexp_replace(
                        btrim(
                            regexp_replace(
                                regexp_replace(
                                    regexp_replace(COALESCE(s, ''),
                                        U&'[\200B\200C\200D\2060\180E\FEFF]', '', 'g'),
                                    U&'[\0009-\000D\0085\00A0\1680\2000-\200A\2028\2029\202F\205F\3000]', ' ', 'g'),
                                ' +', ' ', 'g'),
                            ' '),
                        ' *\| *', ' | ', 'g'),
                    ' | '),
                ''),
            ' | '),
        '');
$$;

-- 受影响的 (日期 × 指标)：samples 归一后要按它们重算天级聚合。
CREATE TEMP TABLE zhichang_src_affected (
    local_date date NOT NULL,
    metric     text NOT NULL,
    PRIMARY KEY (local_date, metric)
) ON COMMIT DROP;

INSERT INTO zhichang_src_affected (local_date, metric)
SELECT DISTINCT s.local_date, s.metric
  FROM samples s
 WHERE s.source <> zhichang_clean_source(s.source)
ON CONFLICT DO NOTHING;

-- 原始样本（hypertable）：先删掉归一后会撞上唯一索引的重复行，再改名。
DELETE FROM samples s
 WHERE s.source <> zhichang_clean_source(s.source)
   AND EXISTS (SELECT 1 FROM samples t
                WHERE t.start_time = s.start_time
                  AND t.metric = s.metric
                  AND t.value = s.value
                  AND t.source = zhichang_clean_source(s.source));
UPDATE samples SET source = zhichang_clean_source(source)
 WHERE source <> zhichang_clean_source(source);

-- 运动（UNIQUE (name, source, start_time)）
DELETE FROM workouts w
 WHERE w.source <> zhichang_clean_source(w.source)
   AND EXISTS (SELECT 1 FROM workouts t
                WHERE t.name = w.name
                  AND t.start_time = w.start_time
                  AND t.source = zhichang_clean_source(w.source));
UPDATE workouts SET source = zhichang_clean_source(source)
 WHERE source <> zhichang_clean_source(source);

-- 睡眠（UNIQUE (local_date, source, sleep_start)；sleep_start 可为 NULL，
-- 而唯一约束里 NULL 互不相等，所以这类行不会撞车）
DELETE FROM sleep_sessions x
 WHERE x.source <> zhichang_clean_source(x.source)
   AND EXISTS (SELECT 1 FROM sleep_sessions t
                WHERE t.local_date = x.local_date
                  AND t.sleep_start = x.sleep_start
                  AND t.source = zhichang_clean_source(x.source));
UPDATE sleep_sessions SET source = zhichang_clean_source(source)
 WHERE source <> zhichang_clean_source(source);

-- 心电（UNIQUE (start_time, source)）
DELETE FROM ecg_records e
 WHERE e.source <> zhichang_clean_source(e.source)
   AND EXISTS (SELECT 1 FROM ecg_records t
                WHERE t.start_time = e.start_time
                  AND t.source = zhichang_clean_source(e.source));
UPDATE ecg_records SET source = zhichang_clean_source(source)
 WHERE source <> zhichang_clean_source(source);

-- 来源清单：归一后与已有干净行合并。device_class 不需要重算——
-- 它是按名字里的关键词粗判的（"watch" / "iphone" / …），而空白归一不改变
-- 任何关键词子串，所以脏行的类别与干净行必然相同。
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT source FROM sources WHERE source <> zhichang_clean_source(source)
    LOOP
        INSERT INTO sources (source, device_class, preferred, first_seen_at, last_seen_at)
        SELECT zhichang_clean_source(r.source), s.device_class, s.preferred,
               s.first_seen_at, s.last_seen_at
          FROM sources s WHERE s.source = r.source
        ON CONFLICT (source) DO UPDATE SET
            first_seen_at = LEAST(sources.first_seen_at, EXCLUDED.first_seen_at),
            last_seen_at  = GREATEST(sources.last_seen_at, EXCLUDED.last_seen_at),
            preferred     = sources.preferred OR EXCLUDED.preferred;
        DELETE FROM sources WHERE source = r.source;
    END LOOP;
    -- 归一后变成空串的来源（如整串都是 NBSP）没有任何意义，直接删掉
    DELETE FROM sources WHERE source = '';
END $$;

-- 重算受影响的日期：samples 的来源已变，天级聚合里的来源清单与
-- 跨来源口径必须跟着重建，否则界面拿到的还是「归一前」的天级值。
INSERT INTO rollup_queue (local_date, metric)
SELECT local_date, metric FROM zhichang_src_affected
ON CONFLICT DO NOTHING;

DROP TABLE IF EXISTS zhichang_src_affected;
DROP FUNCTION IF EXISTS zhichang_clean_source(text);
