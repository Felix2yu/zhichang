-- 知常 · 心电图
--
-- 一行一次测量：起止时刻、上游判定（原文 + 归一码）、平均心率、采样率、点数、来源。
-- 波形不在本表——见 0006_ecg_voltages.sql（两者规模差 5 个数量级，读列表不该碰波形）。

CREATE TABLE IF NOT EXISTS ecg_records (
    id                  bigserial PRIMARY KEY,
    start_time          timestamptz NOT NULL,
    end_time            timestamptz NOT NULL,
    -- classification 是上游原文（随手机语言本地化，简中「窦性心律」/ 英文 "Sinus Rhythm"），
    -- classification_code 是归一后的稳定码。两个都留：
    -- 码用于统计与筛选（跨语言可比），原文用于核对，并在码认不出时兜底展示。
    classification      text NOT NULL DEFAULT '',
    classification_code text NOT NULL DEFAULT 'unknown',
    average_heart_rate  double precision,
    sampling_frequency  integer,
    -- 上游声明的波形点数（numberOfVoltageMeasurements）。本表只落元数据、不存波形，
    -- 它用于列表页显示，以及核对导出是否被截断。
    voltage_count       integer NOT NULL DEFAULT 0,
    source              text NOT NULL DEFAULT '',
    local_date          date NOT NULL,
    tz_offset_min       integer NOT NULL DEFAULT 0,
    ingest_run_id       bigint,
    ingested_at         timestamptz NOT NULL DEFAULT now(),
    -- 同来源同起始秒即同一次测量。这是幂等的锚点：重复提交同一份导出时按它去重，
    -- 值不参与（否则重算一次判定就会多出一行「同一时刻的另一条记录」）。
    UNIQUE (start_time, source)
);
CREATE INDEX IF NOT EXISTS ecg_records_date_idx ON ecg_records (local_date DESC);
CREATE INDEX IF NOT EXISTS ecg_records_start_idx ON ecg_records (start_time DESC);
