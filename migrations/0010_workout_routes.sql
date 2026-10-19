-- 知常 · 运动轨迹（GPX 导入）
--
-- 与 workouts 分表的理由和心电波形一样：一条轨迹实测两万多个点（约 80KB），
-- 而运动列表每次都要读几十行运动记录。放在同一张表里，列表查询会把折线一起拉出来
-- ——TOAST 只是让大字段不进缓存，不是隔离，读取开销照样付。
--
-- 逐点不存绝对时间戳：绝对时刻 = start_time + at_sec[i]。上游的逐点时间形态在
-- HAE 各版本间变过，存下来只会把两种形态一起固化，并让时轴有两个可能互相矛盾的来源。
-- 代价是任一缺点时间就得整条丢弃（导入侧强制），因为按索引推时轴的前提是点数完整。
--
-- 建表时只由本机命令行写入（zhichangd import-routes），接入端点不收轨迹。
--
-- 这条口径后来变了，原因不是判断错了，而是前提不成立：同一份 HAE 导出在 JSON 里
-- 本来就带着 workouts[].route（实测一年 109 条运动、30.7 万个点），逐点经纬度随 POST
-- 已经进了服务器。端点不解析它只是把数据丢掉，并不减少一分暴露，而界面会显示成
-- 「这条运动没有轨迹」，看不出是上游没给还是应用没收。现在摄入侧也写这张表，
-- 覆盖方向单向：自动写入只覆盖自动写入，手工导入的 GPX 永不被后台同步顶掉。

CREATE TABLE IF NOT EXISTS workout_routes (
    workout_id   bigint PRIMARY KEY REFERENCES workouts(id) ON DELETE CASCADE,
    -- 来源文件与内容指纹：同一份 GPX 重放导入时按 sha256 判定「没变过」直接跳过，
    -- 让命令可以反复跑而不产生第二次写入。
    file_name    text NOT NULL,
    sha256       text NOT NULL,
    point_count  integer NOT NULL,
    start_time   timestamptz NOT NULL,
    end_time     timestamptz NOT NULL,
    -- 摘要列：地图定位与「有没有轨迹」的判定都从这里取，不必碰数组。
    lat_min      double precision,
    lat_max      double precision,
    lon_min      double precision,
    lon_max      double precision,
    elev_min     double precision,
    elev_max     double precision,
    -- 折线本体。用数组而不是逐点行：307,201 个点建成行表是 30 万行纯坐标，
    -- 一次读还要排序；数组在 pgx 里是原生支持，也能在 psql 里直接切片核对。
    lats         double precision[] NOT NULL,
    lons         double precision[] NOT NULL,
    eles         double precision[] NOT NULL,
    at_secs      integer[] NOT NULL,
    ingested_at  timestamptz NOT NULL DEFAULT now()
);
