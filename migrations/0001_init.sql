-- 知常 · 初始 schema
-- 约定：库内一切 timestamptz 存 UTC；local_date 由 payload 自带偏移推导，服务端时区不参与入库。

CREATE EXTENSION IF NOT EXISTS timescaledb;

-- 指标字典：聚合口径的唯一事实来源
CREATE TABLE IF NOT EXISTS metric_defs (
    metric           text PRIMARY KEY,
    display_name_zh  text NOT NULL,
    kind             text NOT NULL CHECK (kind IN ('cumulative', 'instant', 'interval', 'categorical')),
    day_agg          text NOT NULL CHECK (day_agg IN ('sum', 'mean', 'max', 'min', 'last', 'count')),
    cross_source_agg text NOT NULL CHECK (cross_source_agg IN ('max', 'mean', 'sum', 'latest')),
    unit             text,
    preferred_unit   text,
    value_field      text NOT NULL DEFAULT 'qty',
    chart            text NOT NULL DEFAULT 'line',
    enabled          boolean NOT NULL DEFAULT true,
    sort_order       integer NOT NULL DEFAULT 100,
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- 原始样本（hypertable）
CREATE TABLE IF NOT EXISTS samples (
    start_time    timestamptz      NOT NULL,
    end_time      timestamptz      NOT NULL,
    metric        text             NOT NULL,
    source        text             NOT NULL DEFAULT '',
    device        text,
    value         double precision NOT NULL,
    value_min     double precision,
    value_max     double precision,
    unit          text             NOT NULL DEFAULT '',
    local_date    date             NOT NULL,
    tz_offset_min integer          NOT NULL DEFAULT 0,
    meta          jsonb,
    ingest_run_id bigint,
    ingested_at   timestamptz      NOT NULL DEFAULT now()
);

SELECT create_hypertable('samples', by_range('start_time', INTERVAL '1 month'), if_not_exists => true, migrate_data => true);

-- 去重 / 幂等：TimescaleDB 要求唯一索引包含分区列 start_time
CREATE UNIQUE INDEX IF NOT EXISTS samples_dedup_uidx ON samples (start_time, metric, source, value);
CREATE INDEX IF NOT EXISTS samples_metric_date_idx ON samples (metric, local_date DESC);
CREATE INDEX IF NOT EXISTS samples_metric_time_idx ON samples (metric, start_time DESC);
CREATE INDEX IF NOT EXISTS samples_source_time_idx ON samples (source, start_time DESC) WHERE source <> '';

-- 天级聚合：每 (日期 × 指标 × 来源)
CREATE TABLE IF NOT EXISTS daily_metrics (
    local_date    date NOT NULL,
    metric        text NOT NULL,
    source        text NOT NULL,
    sample_count  integer NOT NULL DEFAULT 0,
    sum_value     double precision,
    mean_value    double precision,
    min_value     double precision,
    max_value     double precision,
    first_at      timestamptz,
    last_at       timestamptz,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (local_date, metric, source)
);

-- 天级聚合：每 (日期 × 指标)，跨来源收敛后的最终值
CREATE TABLE IF NOT EXISTS daily_metrics_day (
    local_date    date NOT NULL,
    metric        text NOT NULL,
    value         double precision NOT NULL,
    sample_count  integer NOT NULL DEFAULT 0,
    source_count  integer NOT NULL DEFAULT 0,
    sources       text[] NOT NULL DEFAULT '{}',
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (local_date, metric)
);
CREATE INDEX IF NOT EXISTS daily_metrics_day_metric_idx ON daily_metrics_day (metric, local_date DESC);

-- 重算队列（幂等：同一 (date, metric) 去重）
CREATE TABLE IF NOT EXISTS rollup_queue (
    local_date    date NOT NULL,
    metric        text NOT NULL,
    requested_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (local_date, metric)
);

-- 运动
CREATE TABLE IF NOT EXISTS workouts (
    id             bigserial PRIMARY KEY,
    start_time     timestamptz NOT NULL,
    end_time       timestamptz NOT NULL,
    name           text NOT NULL,
    duration_min   double precision,
    total_energy   double precision,
    energy_unit    text,
    total_distance double precision,
    distance_unit  text,
    source         text NOT NULL DEFAULT '',
    local_date     date NOT NULL,
    tz_offset_min  integer NOT NULL DEFAULT 0,
    meta           jsonb,
    ingest_run_id  bigint,
    ingested_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (name, source, start_time)
);
CREATE INDEX IF NOT EXISTS workouts_date_idx ON workouts (local_date DESC);

-- 睡眠
CREATE TABLE IF NOT EXISTS sleep_sessions (
    id            bigserial PRIMARY KEY,
    local_date    date NOT NULL,
    sleep_start   timestamptz,
    sleep_end     timestamptz,
    in_bed_start  timestamptz,
    in_bed_end    timestamptz,
    total_sleep_hr double precision,
    asleep_hr     double precision,
    core_hr       double precision,
    deep_hr       double precision,
    rem_hr        double precision,
    awake_hr      double precision,
    in_bed_hr     double precision,
    source        text NOT NULL DEFAULT '',
    tz_offset_min integer NOT NULL DEFAULT 0,
    ingest_run_id bigint,
    ingested_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (local_date, source, sleep_start)
);
CREATE INDEX IF NOT EXISTS sleep_date_idx ON sleep_sessions (local_date DESC);

-- 来源治理
CREATE TABLE IF NOT EXISTS sources (
    source        text PRIMARY KEY,
    device_class  text NOT NULL DEFAULT 'unknown',
    preferred     boolean NOT NULL DEFAULT false,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now()
);

-- 摄入审计
CREATE TABLE IF NOT EXISTS ingest_runs (
    id             bigserial PRIMARY KEY,
    transport      text NOT NULL,
    automation_id  text,
    automation_name text,
    session_id     text,
    period         text,
    aggregation    text,
    payload_sha256 text,
    payload_bytes  bigint NOT NULL DEFAULT 0,
    archived_path  text,
    received_at    timestamptz NOT NULL DEFAULT now(),
    sample_count   integer NOT NULL DEFAULT 0,
    inserted       integer NOT NULL DEFAULT 0,
    duplicates     integer NOT NULL DEFAULT 0,
    updated        integer NOT NULL DEFAULT 0,
    status         text NOT NULL DEFAULT 'ok',
    error          text,
    affected_from  date,
    affected_to    date
);
CREATE UNIQUE INDEX IF NOT EXISTS ingest_runs_session_uidx ON ingest_runs (session_id) WHERE session_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS ingest_runs_received_idx ON ingest_runs (received_at DESC);

-- 同步水位
CREATE TABLE IF NOT EXISTS sync_watermark (
    automation_id   text NOT NULL,
    metric          text NOT NULL,
    last_start_time timestamptz NOT NULL,
    last_local_date date NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (automation_id, metric)
);

-- 迁移记录
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);
