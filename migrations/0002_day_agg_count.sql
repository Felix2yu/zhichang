-- 类别型指标（洗手、刷牙等）按天计数，扩展 day_agg 允许的值。
ALTER TABLE metric_defs DROP CONSTRAINT IF EXISTS metric_defs_day_agg_check;
ALTER TABLE metric_defs ADD CONSTRAINT metric_defs_day_agg_check
    CHECK (day_agg IN ('sum', 'mean', 'max', 'min', 'last', 'count'));
