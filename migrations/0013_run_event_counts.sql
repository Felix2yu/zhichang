-- 知常 · 摄入审计补齐事件类计数
--
-- ingest_runs 只记 sample_count/inserted，而运动、睡眠、心电都不产生样本
-- （它们各自落自己的表）。于是一次只含心电的同步在概览上显示
-- 「收到 0 条，入库 0」——看着像同步坏了，实际是 31 条心电刚进来。
-- 事件类的落库条数本来就是 IngestStats 的一部分，只是没被写进审计行。

ALTER TABLE ingest_runs ADD COLUMN IF NOT EXISTS workout_count integer NOT NULL DEFAULT 0;
ALTER TABLE ingest_runs ADD COLUMN IF NOT EXISTS sleep_count   integer NOT NULL DEFAULT 0;
ALTER TABLE ingest_runs ADD COLUMN IF NOT EXISTS ecg_count     integer NOT NULL DEFAULT 0;
