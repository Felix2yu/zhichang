-- 应用级偏好：需要跨设备共享、但语义上不是「健康指标」的设置。
--
-- 为什么不塞进 goals 表：SetGoal 用指标字典白名单校验，把一个偏好登记成
-- 「假指标」会让 /api/metrics、MCP 的 health_list_metrics 和接入白名单里
-- 多出一条永远不会有样本的指标——字典是聚合口径的唯一事实来源，不该混入口味。
CREATE TABLE IF NOT EXISTS app_settings (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
