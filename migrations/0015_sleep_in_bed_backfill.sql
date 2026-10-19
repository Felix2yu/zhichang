-- 知常 · 回填卧床时长
--
-- 上游（HAE 的睡眠日汇总）把 inBed 数值恒写成 0，而 inBedStart/inBedEnd 每条都有值。
-- 摄入侧已改成「0 或空缺时按起止时刻补算」，但 sleep_sessions 是 ON CONFLICT DO NOTHING：
-- 重放同一份导出不会更新已有行，历史数据会一直停在 0 上。
--
-- 0 不是无害的缺省值：睡眠评分用它算「清醒占卧床的比例」，分母为 0 时这一项直接归 0，
-- 界面显示的是「没数据」的评分却看不出为什么；把 0 修成真实时长后评分才成立。
--
-- 只补讲得通的行：起止缺一、终点不晚于起点、或跨度超过 24 小时（那是时刻本身不可信）
-- 都留原样，与摄入侧同一套判据。

UPDATE sleep_sessions
   SET in_bed_hr = ROUND((EXTRACT(EPOCH FROM (in_bed_end - in_bed_start)) / 3600.0)::numeric, 4)::double precision
 WHERE (in_bed_hr IS NULL OR in_bed_hr = 0)
   AND in_bed_start IS NOT NULL
   AND in_bed_end IS NOT NULL
   AND in_bed_end > in_bed_start
   AND in_bed_end - in_bed_start <= interval '24 hours';
