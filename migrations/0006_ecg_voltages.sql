-- 知常 · 心电图波形
--
-- 与 ecg_records 分表的理由：一次测量带 15360 个电压点（double precision 约 120KB），
-- 与「一行一次测量」的元信息差了 5 个数量级。放在同一张表里，列表页的 SELECT
-- 每次都会把波形一起拉出来——TOAST 只是让大字段不进缓存，并不是隔离，
-- 读取开销照样付。分开之后读列表永不碰波形。
--
-- 用 double precision[] 而不是 bytea：pgx 对 float64 数组是原生支持，
-- 不必自己定一套二进制编解码；且能在 psql 里直接 voltages[1:10] 看着核对。
-- 代价是每点 8 字节（float4 只要 4），9 条记录合计约 1MB——不值得为它写编解码。
--
-- 独立成一份迁移而不是并进 0005：迁移器按文件名记录「已应用」，
-- 若把这张表追加进已发布过的 0005，已部署的环境升级时不会重跑它，表永远建不出来。

CREATE TABLE IF NOT EXISTS ecg_voltages (
    ecg_id      bigint PRIMARY KEY REFERENCES ecg_records(id) ON DELETE CASCADE,
    -- 上游写的单位，原样保留。HAE 实测给的是 "mcV"，而它的文档举例写 "V"——
    -- 不换算、不纠正：标签差异不登记数值等价换算，单位口径存疑时宁可原样保留。
    unit        text NOT NULL DEFAULT '',
    -- 按时间升序的电压值。采样间隔由 ecg_records.sampling_frequency 决定，
    -- 时轴为 start_time + i/fs，因此不必逐点存时间戳（上游的逐点 date
    -- 既可能是 unix 秒浮点、也可能是日期字符串，存下来只会把两种形态一起固化）。
    voltages    double precision[] NOT NULL,
    ingested_at timestamptz NOT NULL DEFAULT now()
);
