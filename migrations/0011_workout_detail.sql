-- 知常 · 运动记录补全字段与逐点心率
--
-- 此前 normalizeWorkout 只留名称/起止/时长/总能量/距离，导出里的其余部分全部丢弃：
-- 一年实测 110 次运动带逐点心率、109 组配速分段、94 组活动分段、95 个 effortScore、
-- 最大/平均心率、步数、速度、爬楼、温湿度海拔、室内外与地点。
-- 同时 workouts.meta 列自建表起就是恒 NULL（INSERT 的列清单里没有它）。
--
-- 分成「列 / meta / 序列表」三层放，按的是「会不会被过滤或聚合」：
--   - 列：界面上要排序、求和、按区间统计的量（主动能量、平均/最大心率、effortScore）；
--   - meta：只用于核对与展示的上下文（地点、室内外、温湿度、爬楼、速度…），不参与聚合；
--   - 序列表：逐点心率与恢复心率是数组，规模与单行记录差几个数量级，
--     放在 workouts 上会让列表查询每次把曲线一起拉出来（与心电波形分表同一理由）。

-- 主动能量：Apple 运动记录里的「消耗」是主动能量，总能量含基础代谢。
-- 两者都要留：只留总能量会让运动页的消耗比健康 App 高约三成（实测 +29.4%）。
ALTER TABLE workouts ADD COLUMN IF NOT EXISTS active_energy      double precision;
ALTER TABLE workouts ADD COLUMN IF NOT EXISTS active_energy_unit text;
ALTER TABLE workouts ADD COLUMN IF NOT EXISTS avg_hr             double precision;
ALTER TABLE workouts ADD COLUMN IF NOT EXISTS max_hr             double precision;
ALTER TABLE workouts ADD COLUMN IF NOT EXISTS effort_score       double precision;

CREATE TABLE IF NOT EXISTS workout_series (
    workout_id   bigint PRIMARY KEY REFERENCES workouts(id) ON DELETE CASCADE,
    -- 逐点心率：绝对时刻 = 对应运动的 start_time + hr_secs[i]。
    -- 不存逐点绝对时间戳，与心电波形同一取舍（时轴只留一个事实来源）。
    -- 曲线列可空：上游对同一次运动可能只给恢复心率或只给分段，
    -- 写 NOT NULL 会让「有分段没曲线」这种真实形态整条投递失败。
    hr_secs      integer[],
    hr_bpm       float8[],
    -- 恢复心率（运动结束后的心率回落序列），同样按相对秒数存
    rec_secs     integer[],
    rec_bpm      float8[],
    -- 配速分段与活动分段的原文：结构随上游版本变，原样留着用于核对，不解析成列
    splits       jsonb,
    ingested_at  timestamptz  NOT NULL DEFAULT now(),
    -- 成对的秒数/数值长度必须一致：时轴按索引推，长度不等说明写入本身错了。
    -- 两者同时为空是合法状态（该形态下本就不该有这一行，但约束只管写进来的数据）。
    CONSTRAINT workout_series_hr_len CHECK (
        (hr_secs IS NULL AND hr_bpm IS NULL)
        OR coalesce(array_length(hr_secs,1),0) = array_length(hr_bpm,1)),
    CONSTRAINT workout_series_rec_len CHECK (
        (rec_secs IS NULL AND rec_bpm IS NULL)
        OR coalesce(array_length(rec_secs,1),0) = array_length(rec_bpm,1)),
    -- 三样都没有的行没有任何信息量，比不写更糟（看起来像「采到了空曲线」）
    CONSTRAINT workout_series_not_empty CHECK (
        hr_bpm IS NOT NULL OR rec_bpm IS NOT NULL OR splits IS NOT NULL)
);
