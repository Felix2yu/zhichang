-- 目标：按指标存一个期望值，供页面画目标线与统计达标。
-- 只存数值，单位与中文名一律从 metric_defs 取（指标字典是口径的唯一事实来源）。
CREATE TABLE IF NOT EXISTS goals (
    metric     text PRIMARY KEY,
    target     double precision NOT NULL CHECK (target > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);
