-- 别名收敛：把 HAE 的旧导出名合并到字典的规范名上。
--
-- 背景：同一概念在导出里有两种写法——血氧（导出 blood_oxygen_saturation /
-- 字典 oxygen_saturation）、锻炼时长（apple_exercise_time / exercise_time）、
-- 站立时长（apple_stand_time / stand_time）、步行跑步距离
-- （distance_walking_running / walking_running_distance）、体重
-- （weight_&_body_mass / body_mass）。
--
-- 入库侧已按 metric.Canonical 收敛，但历史行仍躺在旧名下：卡片按字典的规范名取数，
-- 所以那些历史数据既显示不出来也删不掉，看上去就是「血氧一直没数据」。
-- 这里把历史行改名，让老数据回到卡片的口径上。
--
-- 冲突处理：改名可能撞上去重唯一索引 (start_time, metric, source, value)、
-- 天级主键 (local_date, metric, source) 或 (local_date, metric)——
-- 撞上说明新旧两行是同一份数据，删旧留新，不让唯一约束把整份迁移打回去。
--
-- 幂等：没有旧名行时每条语句都是空操作，可以安全重放。
--
-- ⚠️ 别名对照表是这份迁移的全量事实来源，必须与 internal/domain/metric/alias.go
-- 的 aliases 完全一致，否则新出现的旧名会继续把数据搁浅。
-- 一致性由 internal/store/postgres/alias_migration_test.go 锁定。

DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT * FROM (VALUES
            ('blood_oxygen_saturation',          'oxygen_saturation'),
            ('apple_exercise_time',              'exercise_time'),
            ('apple_stand_time',                 'stand_time'),
            ('apple_stand_hour',                 'stand_hour'),
            ('distance_walking_running',         'walking_running_distance'),
            ('weight_&_body_mass',               'body_mass'),
            ('weight_body_mass',                 'body_mass'),
            ('apple_sleeping_wrist_temperature', 'sleeping_wrist_temperature')
        ) AS t(old_name, new_name)
    LOOP
        -- 原始样本（hypertable）：改名后聚合才有数据可算
        DELETE FROM samples s
         WHERE s.metric = r.old_name
           AND EXISTS (SELECT 1 FROM samples t
                        WHERE t.metric = r.new_name
                          AND t.start_time = s.start_time
                          AND t.source = s.source
                          AND t.value = s.value);
        UPDATE samples SET metric = r.new_name WHERE metric = r.old_name;

        -- 天级中间量：重算会重建它，但旧名的残留行没有归属，留着就是垃圾
        DELETE FROM daily_metrics d
         WHERE d.metric = r.old_name
           AND EXISTS (SELECT 1 FROM daily_metrics t
                        WHERE t.metric = r.new_name
                          AND t.local_date = d.local_date
                          AND t.source = d.source);
        UPDATE daily_metrics SET metric = r.new_name WHERE metric = r.old_name;

        DELETE FROM daily_metrics_day d
         WHERE d.metric = r.old_name
           AND EXISTS (SELECT 1 FROM daily_metrics_day t
                        WHERE t.metric = r.new_name AND t.local_date = d.local_date);
        UPDATE daily_metrics_day SET metric = r.new_name WHERE metric = r.old_name;

        -- 重算队列
        DELETE FROM rollup_queue q
         WHERE q.metric = r.old_name
           AND EXISTS (SELECT 1 FROM rollup_queue t
                        WHERE t.metric = r.new_name AND t.local_date = q.local_date);
        UPDATE rollup_queue SET metric = r.new_name WHERE metric = r.old_name;

        -- 同步水位：入库侧写的是规范名，旧名的水位留着会让 /sync 页出现幽灵条目
        DELETE FROM sync_watermark w
         WHERE w.metric = r.old_name
           AND EXISTS (SELECT 1 FROM sync_watermark t
                        WHERE t.metric = r.new_name AND t.automation_id = w.automation_id);
        UPDATE sync_watermark SET metric = r.new_name WHERE metric = r.old_name;

        -- 目标值：值随指标走，不能因为改名把用户设过的目标丢掉
        IF to_regclass('goals') IS NOT NULL THEN
            DELETE FROM goals g
             WHERE g.metric = r.old_name
               AND EXISTS (SELECT 1 FROM goals t WHERE t.metric = r.new_name);
            UPDATE goals SET metric = r.new_name WHERE metric = r.old_name;
        END IF;

        -- 字典里的旧条目必须删掉：留着会让趋势页出现两个同名 chip，其中一个永远没数据。
        -- 这里不清 enabled 而是整行删除——条目本身就是「同一概念的另一种写法」，
        -- 没有独立存在的意义。
        DELETE FROM metric_defs WHERE metric = r.old_name;
    END LOOP;
END $$;

-- 重算受影响日期的天级聚合：samples 已经改名，daily_metrics / daily_metrics_day
-- 必须跟着重建，否则界面拿到的是「改名前的天级值」或直接没有值。
INSERT INTO rollup_queue (local_date, metric)
SELECT DISTINCT s.local_date, s.metric
  FROM samples s
 WHERE s.metric IN (
    'oxygen_saturation',
    'exercise_time',
    'stand_time',
    'stand_hour',
    'walking_running_distance',
    'body_mass',
    'sleeping_wrist_temperature'
 )
ON CONFLICT DO NOTHING;
