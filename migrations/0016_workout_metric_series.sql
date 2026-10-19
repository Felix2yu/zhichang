-- 知常 · 运动内部的逐分钟数组
--
-- 2026 版导出把「这一次运动里」的量按分钟摊开成数组随运动一起送出，实测一年：
-- 主动能量 5202 点、基础能量 4924 点、步数 5174 点、步行+跑步距离 4151 点、骑行距离 159 点。
-- 应用此前一个字也没收：结构体按聚合对象解析，而 Quantity 的容错逻辑对数组返回
-- 「不认识」——字段看着在解析，数据整段丢弃，且界面上看不出任何矛盾。
--
-- 为什么不写进 samples：那些量在 samples 里已有**日内聚合**（一天所有运动加起来），
-- 逐分钟点再进同一张表会让天级求和双计；而它们只在「这一次运动内」有意义，
-- 归属是 workout_id 而不是 (local_date, metric)。
--
-- 时轴与轨迹、逐点心率同一套约定：不存逐点绝对时间，绝对时刻 = workouts.start_time + at_secs[i]。
-- 这里存的是显式秒数（不像心电波形按索引推），所以缺时间的点只丢那一个点，不会让后面错位。
--
-- metric 用指标字典里的规范名（step_count / active_energy / basal_energy_burned /
-- walking_running_distance / cycling_distance），单位换算到字典口径后与值一起写：
-- 只换值不换单位，展示层按字典单位渲染就会把 kJ 标成 kcal（虚高 4.18 倍）。
-- 不加指向 metric_defs 的外键：新增一个逐分钟量不该要求先跑一次迁移。

CREATE TABLE IF NOT EXISTS workout_metric_series (
    workout_id  bigint NOT NULL REFERENCES workouts(id) ON DELETE CASCADE,
    metric      text   NOT NULL,
    unit        text,
    at_secs     integer[] NOT NULL,
    vals        double precision[] NOT NULL,
    ingested_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workout_id, metric),
    -- 秒数与数值必须等长：两列按索引配对，长度不等说明写入本身错了。
    CONSTRAINT workout_metric_series_len CHECK (coalesce(array_length(at_secs,1),0) = array_length(vals,1)),
    -- 空数组比不写更糟（看起来像「采到了但全是空的」）
    CONSTRAINT workout_metric_series_not_empty CHECK (array_length(vals,1) > 0)
);
