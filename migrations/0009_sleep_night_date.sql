-- 知常 · 睡眠日归属改按「本夜」（18:00 分界）
--
-- 背景：sleep_sessions.local_date 原先按「入睡时刻的日历日」归属。于是
-- 「27 号 02:23 入睡」（26 号那晚的延续）与「27 号 23:39 入睡」（27 号那晚）
-- 落在同一个 local_date 上——一年导出里实测有 36 天如此。两晚挤进一天之后，
-- 天级睡眠值要么把两晚求和成一晚（虚高一倍），要么取最长把一晚直接丢掉。
--
-- 规则：入睡（缺则上床）时刻的本地钟点早于 18:00 → 归前一天。
-- 这与睡眠时间表矩阵的横轴（相对 18:00 的本夜分钟轴）是同一把尺子。
--
-- 只动带 sleep_start 的行：没有入睡时刻时，local_date 出自 HAE 的日桶
-- （当地午夜），那是「哪天」而不是「几点睡」，按钟点判定会把整晚挪错一天。
--
-- 幂等：新归属是 (sleep_start, tz_offset_min) 的纯函数，重跑第二次没有行会变。
-- 唯一约束 (local_date, source, sleep_start) 也不会被撞：同一 (source, sleep_start)
-- 的两行在迁移前就违反该约束，因此不存在。
--
-- ⚠️ 这里的判定必须与 internal/domain/tz.NightDate 完全一致，否则新入库的行
-- 与历史行会按两套口径归属。一致性由
-- internal/store/postgres/sleep_night_migration_test.go 在真库上逐行比对锁定。

-- 本地钟点：sleep_start 是 timestamptz，先按 UTC 取出去掉时区的墙上时间，
-- 再加上记录自带的偏移。直接对 timestamptz 转 date 会按会话 TimeZone 换算，
-- 同一行数据在 UTC 库与 +08 库上会迁出两个不同的结果，所以必须显式钉住。
CREATE TEMP TABLE zhichang_night_moved ON COMMIT DROP AS
SELECT id,
       local_date AS old_date,
       (wall::date - CASE WHEN EXTRACT(HOUR FROM wall) < 18 THEN 1 ELSE 0 END) AS new_date
  FROM (
    SELECT id, local_date,
           (sleep_start AT TIME ZONE 'UTC') + make_interval(mins => tz_offset_min) AS wall
      FROM sleep_sessions
     WHERE sleep_start IS NOT NULL
  ) x;

UPDATE sleep_sessions s
   SET local_date = m.new_date
  FROM zhichang_night_moved m
 WHERE s.id = m.id AND s.local_date <> m.new_date;

-- 新旧两天都要重算：旧日期少了一晚，新日期多了一晚。
-- 服务启动后会消费这条队列，不必重启前手工触发。
INSERT INTO rollup_queue (local_date, metric)
SELECT DISTINCT d, 'sleep_analysis'
  FROM (SELECT old_date AS d FROM zhichang_night_moved
        UNION
        SELECT new_date      FROM zhichang_night_moved) x
 WHERE d IS NOT NULL
ON CONFLICT DO NOTHING;

DROP TABLE IF EXISTS zhichang_night_moved;
