-- 知常 · 刷牙口径从「次数」改成「时长」
--
-- 上游（实测是电动牙刷 App）给的是每次刷牙的秒数（36~230，单位 s），
-- 而字典把它登记成 count + 日内计数。后果不是「措辞不准」：
-- 天级值永远是「1 次」，真实时长在 samples 里躺着却没人显示，
-- 而且库里的 unit 是 's'、字典的 unit 是 'count'，界面上看不出任何矛盾。
--
-- 登记成累计量 + 日内求和之后，一天的多次刷牙会相加（单位仍是秒）。
--
-- 新增指标不需要迁移：启动时 SeedMetrics 会补齐缺失行（ON CONFLICT DO NOTHING）。
-- 但**改已存在的行**必须靠这条迁移——SeedMetrics 对已存在的行不动，
-- 用户手工调过的口径也不该被启动覆盖。

UPDATE metric_defs
   SET display_name_zh = '刷牙时长',
       kind = 'cumulative',
       day_agg = 'sum',
       unit = 's',
       preferred_unit = 's',
       updated_at = now()
 WHERE metric = 'toothbrushing'
   AND (kind <> 'cumulative' OR day_agg <> 'sum' OR unit IS DISTINCT FROM 's');

-- 已入库的刷牙样本要按新口径重算，否则界面上继续显示迁移前的「1 次」
INSERT INTO rollup_queue (local_date, metric)
SELECT DISTINCT local_date, 'toothbrushing' FROM samples WHERE metric = 'toothbrushing'
ON CONFLICT DO NOTHING;
