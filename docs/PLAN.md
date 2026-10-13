# 知常（Zhichang）— 自托管健康数据平台 · 实施方案 v1

> 复命曰常，知常曰明。
> 目标：把 iPhone / Apple Watch 的 Apple Health 数据，通过 Health Auto Export（HAE）自动同步进自托管时序库，提供美观的 Web 仪表盘与 MCP 查询能力。

**状态：方案已确认（全部按推荐项），代码已实现。** 实际落地版本：

| 层 | 版本 |
|---|---|
| Go | 1.27.2（go.mod `go 1.27`） |
| TimescaleDB / PostgreSQL | 2.30.2 / 18.6 |
| SvelteKit / Svelte / Vite | 3.0.1 / 5.57.2 / 8.3.4 |
| Tailwind CSS / ECharts | 4.3.3 / 6.1.0 |
| chi / pgx / MCP SDK | 5.4.0 / 5.11.0 / modelcontextprotocol-go-sdk v1.8.0 |

> 注：TypeScript 用 6.0.3 而非 7.x（Kit 3 读 tsconfig 的 API 与 TS 7 不兼容）；SvelteKit 3 起 `svelte.config.js` 废弃（配置改走 `vite.config.ts` 的 `sveltekit()` 插件）、`$lib` 改为 `#lib`（本仓库用 package.json 的 subpath imports）。

---

## 1. 整体架构

```
┌──────────── 数据源（iOS） ────────────┐
│  Apple Health (HealthKit)            │
│   iPhone + Apple Watch（重复样本源） │
└───────────────┬──────────────────────┘
                │ Health Auto Export App（自动化，受 iOS 后台调度）
                │ ① REST API（主通道，HTTPS POST + JSON）
                │ ② MQTT（可选旁路，QoS0）
                ▼
┌───────────────────────────────────────────────────────────────┐
│  知常 zhichangd（单个 Go 二进制，非 root 运行）               │
│                                                               │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌────────────────┐  │
│  │ ingest   │→│normalize │→│ upsert   │→│ rollup 增量重算│  │
│  │ REST/MQTT│ │ HAE v1/v2│ │ staging  │ │ (受影响日期)   │  │
│  └──────────┘ └──────────┘ └──────────┘ └────────────────┘  │
│        │             │                                        │
│        │ raw archive（可选落盘 + sha256，用于回溯重放）       │
│        ▼                                                      │
│  ┌────────────────────┐  ┌──────────────┐  ┌──────────────┐ │
│  │ /api/*  Dashboard  │  │ /mcp MCP     │  │ embed 前端   │ │
│  │  查询 API（只读）  │  │ stdio + HTTP │  │ SvelteKit    │ │
│  └────────────────────┘  └──────────────┘  └──────────────┘ │
└────────────────────────────┬──────────────────────────────────┘
                             ▼
        TimescaleDB (PostgreSQL 17 + timescaledb 2.28)
        samples(hypertable) / daily_metrics / workouts / sleep_sessions
```

关键取舍：**单一进程 + 单一二进制 + 前端 embed**，数据全在本地，无外部 SaaS 依赖。

---

## 2. 技术选型

| 层 | 选型 | 理由 |
|---|---|---|
| 后端 | Go 1.25 + `net/http` + chi 路由 + `pgx/v5` + `slog` | 单二进制、低内存（自托管小机器友好）；pgx 支持 COPY 高吞吐写入 |
| 时序库 | **TimescaleDB**（PG17 + timescaledb 2.28） | 完整 PostgreSQL（窗口函数、CTE、物化视图、jsonb、部分索引）；hypertable 自动分区 + 压缩；个人规模数据量（百万级样本）下查询毫秒级 |
| 时序库备选 | GreptimeDB | 写入吞吐与云原生更好，但 SQL 完备度、事务、upsert、jsonb 生态弱于 PG 系；**不推荐**做主库，仅在多设备/多用户场景下作可选后端（通过 repository 接口隔离） |
| 前端 | **SvelteKit 2（adapter-static）+ Tailwind v4 + ECharts** | 构建产物为纯静态，可直接 `embed.FS` 打进 Go 二进制；ECharts 对多指标/缩放/堆叠睡眠分期支持好 |
| 图表 | ECharts（趋势/堆叠/缩放），uPlot 作为超大数据量备选 | — |
| MCP | 官方 `modelcontextprotocol/go-sdk`（已 v1.x，支持 2026-07-28 修订、struct tag 生成 schema） | 官方维护 + typed handler；stdio 与 Streamable HTTP 双通道 |
| MQTT | `eclipse/paho.mqtt.golang`（可选编译进主进程，默认关闭） | 仅作旁路 |
| 迁移 | goose / golang-migrate，SQL 文件 + 启动自动 migrate（可关） | 与代码解耦，方便自建库升级 |

---

## 3. 数据接入：MQTT vs REST API 对比与结论

官方能力（help.healthyapps.dev 核实）：

| 维度 | REST API 自动化 | MQTT 自动化 |
|---|---|---|
| 实时性 | 由 App 自动化调度（同步频率 + iOS 后台刷新）决定；设备锁定/低电量/后台刷新关闭时不运行 | **完全相同**——同一套调度器，MQTT 只是换了出口。长连接不带来任何额外实时性，因为数据产出在 HealthKit 侧 |
| 触发时机上限 | iOS 后台任务约 30s 窗口，建议充电 + iPhone 镜像时更频繁 | 同左（同受后台任务约束） |
| 部署复杂度 | 只需暴露一个 HTTPS 端点（复用现有 Caddy/Nginx/Tailscale），零新组件 | 需额外部署 broker（Mosquitto/EMQX）、开端口、配鉴权与 TLS、公网暴露面变大 |
| 可靠性 | HTTP 状态码 + App 活动日志可见成败；支持**批量请求**拆分大负载；失败可重试；payload 可归档重放 | **QoS 0（最多一次）、非 retained**，broker 未连接/掉线即静默丢消息；无回执、无重放；单条消息过大易被 broker 拒收 |
| 元数据 | 自动附带 `automation-id / automation-name / automation-period / automation-aggregation / session-id` header → 可做幂等与来源治理 | 只有 topic，无等价元数据 |
| 认证 | 自定义 header：`X-API-Key` 或 `Authorization: Bearer` | 用户名/密码（TLS 支持需自行验证 broker 配置） |
| 格式 | JSON / CSV，**JSON 支持批量请求** | 仅 JSON，无批量 |
| 负载上限 | 文档提示可能达数百 MB（需服务端能扛） | broker 消息大小限制，需拆分 |

**选型结论：以 REST API 为主通道，MQTT 作为可选旁路（默认关闭）。**

- 实时性两者等价，MQTT 的“推送”优势在这个场景里不存在（数据不是从设备实时流出，而是 App 被系统调度后才查询 HealthKit 并外发）。
- MQTT 的 QoS0 + 无 retained + 无重放 = 丢数据不可知，对“健康数据完整性”这个核心诉求是硬伤。
- REST 的 `automation-id` / `session-id` header 让服务端能做**幂等去重与同步水位治理**，这是 MQTT 给不了的。

> 补充说明：HAE 自带一个 iOS 端 MCP Server（`http://{LAN_IP}:9000/mcp`，需 App 保持前台）。它是“按需实时查询 HealthKit”的旁路，不稳定也不适合做入库主链路，本方案**不依赖**它；但在实现上预留一个可选的 `hae-live` 数据源适配器，用于“今天还没同步时手动临查”。

### HAE 侧推荐配置（写进部署文档）
- 类型：REST API，格式 JSON，导出版本 **v2**
- 日期范围：**自上次同步**（增量最小）；补数据时用手动导出 JSON → `zhichangctl import`
- 汇总数据：**开启** + 时间分组按**小时**（步数/能量按小时分组，跨来源重复在聚合层解决；秒/分钟分组会显著拖慢 App 并放大负载）
- 批量请求：**开启**（大负载拆分）
- 频率：15～60 分钟（结合充电/后台刷新现实约束）
- 指标：只勾选真正要用的（空指标也会拖慢 App）
- Header：`X-Zhichang-Token: <token>`（或 `Authorization: Bearer`）

---

## 4. 目录结构

```
zhichang/
├── cmd/
│   └── zhichangd/          # 唯一二进制：serve（默认）+ migrate / seed-metrics / doctor / mcp
│                           # 子命令实现在 internal/cli（合并为一个二进制，便于装配式镜像）
├── internal/
│   ├── config/             # env + 配置文件（时区、默认单位、去重模式、保留策略）
│   ├── domain/
│   │   ├── metric/         # 指标字典 registry（kind/聚合口径/单位/中文名），DB 驱动 + 内置默认
│   │   ├── sample/         # 归一化样本模型、local_date 推导、去重键计算
│   │   └── tz/             # 时区解析：payload 自带偏移优先，回落 home_timezone
│   ├── ingest/
│   │   ├── hae/            # HAE payload 解析（v1/v2，metrics/workouts/sleep/ecg 等）
│   │   ├── httpping/       # POST /api/ingest/hae 接收器（流式解析 + 归档 + 幂等）
│   │   ├── mqtt/           # 可选 MQTT subscriber（build tag 或配置开关）
│   │   └── pipeline/       # 归一化 → staging → upsert → rollup 触发（共用编排）
│   ├── store/postgres/     # pgx 连接池、repository、COPY staging、批量 upsert、rollup SQL
│   ├── query/              # 查询构造器：时间窗、粒度、时区、指标→聚合口径映射
│   ├── api/                # Dashboard 只读 API（handler + DTO）
│   ├── mcp/                # MCP server：tools/resources（stdio + streamable http 复用）
│   ├── jobs/               # 定时：rollup 修补、压缩策略、保留策略、数据缺口检查
│   └── web/                # embed.FS（前端构建产物），SPA fallback
├── migrations/             # 0001_init.sql ... （含 hypertable/索引/rollup/策略）
├── web/                    # SvelteKit 前端源码
│   ├── src/routes/         # / 概览、/trends、/sleep、/workouts、/sync、/settings
│   └── src/lib/            # api.ts、components(MetricCard/TrendChart/RangePicker...)、stores
├── deploy/
│   ├── Dockerfile          # 多阶段：node 构建前端 → go build → alpine（非 root + PUID/PGID）
│   ├── docker-compose.yml  # zhichang + timescaledb + (可选 caddy)
│   └── caddy/Caddyfile
├── testdata/               # 样例 HAE 导出 JSON（用于解析与聚合的 fixtures）
└── docs/                   # PLAN.md、数据模型、HAE 配置指南、MCP 用法
```

---

## 5. 数据模型

### 5.1 指标字典（聚合口径的唯一事实来源）

```sql
CREATE TABLE metric_defs (
  metric            text PRIMARY KEY,          -- HAE snake_case 名，如 step_count
  display_name_zh   text NOT NULL,             -- 步数
  kind              text NOT NULL CHECK (kind IN ('cumulative','instant','interval','categorical')),
  day_agg           text NOT NULL CHECK (day_agg IN ('sum','mean','max','min','last')),
  cross_source_agg  text NOT NULL CHECK (cross_source_agg IN ('max','mean','sum','latest')),
  unit              text,                      -- HAE 原单位
  preferred_unit    text,                      -- 展示单位（与 unit 不同时做换算）
  value_field       text NOT NULL DEFAULT 'qty', -- qty | Avg | totalSleep | asleep ...
  chart             text DEFAULT 'line',       -- line | bar | stacked | scatter
  enabled           boolean NOT NULL DEFAULT true,
  sort_order        int NOT NULL DEFAULT 100
);
```

默认口径（内置种子数据）：

| 类别 | 指标 | kind | 日内聚合 | 跨来源 |
|---|---|---|---|---|
| 累计量 | `step_count` `active_energy` `basal_energy_burned` `flights_climbed` `walking_running_distance` `dietary_water` `mindful_minutes` `exercise_time` | cumulative | **sum** | **max**（取各来源总量的最大值，消除 iPhone/Watch 双计） |
| 瞬时量 | `heart_rate` `resting_heart_rate` `heart_rate_variability` `body_mass`/`weight_&_body_mass` `body_mass_index` `body_fat_percentage` `oxygen_saturation` `respiratory_rate` `body_temperature` `blood_glucose` | instant | **mean** | **mean**（按各来源样本数加权） |
| 区间量 | `sleep_analysis`（asleep/core/deep/rem/inBed） | interval | sum（分期时长）／last（起止时刻） | max |
| 特殊 | `blood_pressure`（systolic/diastolic） | instant | mean | mean |
| 事件 | `workouts`、`ecg`、`heart_rate_notifications`、`state_of_mind`、`symptoms`、`medications`、`cycle_tracking` | 入专属表 | — | — |

### 5.2 原始样本表（hypertable）

```sql
CREATE TABLE samples (
  start_time    timestamptz      NOT NULL,   -- 分区列；库内统一 UTC
  end_time      timestamptz      NOT NULL,   -- 累计量的统计区间（无 end 时 = start）
  metric        text             NOT NULL,
  source        text             NOT NULL DEFAULT '',  -- HAE 的 source（App/设备名）
  device        text,                                   -- 若 payload 提供
  value         double precision NOT NULL,   -- 主数值
  value_min     double precision,            -- 心率 Min 等
  value_max     double precision,            -- 心率 Max 等
  unit          text             NOT NULL,
  local_date    date             NOT NULL,   -- ★ 由 payload 自带偏移推导的本地日期
  tz_offset_min smallint         NOT NULL DEFAULT 0,  -- ★ payload 自带偏移（分钟）
  meta          jsonb,                        -- mealTime / reason / value 文本等
  ingest_run_id bigint,
  ingested_at   timestamptz      NOT NULL DEFAULT now()
);

SELECT create_hypertable('samples', by_range('start_time', INTERVAL '1 month'));
-- timescaledb < 2.13 写法：SELECT create_hypertable('samples','start_time', chunk_time_interval => INTERVAL '1 month');

-- 去重 / 幂等（唯一索引必须包含分区列 start_time）
CREATE UNIQUE INDEX samples_dedup_uidx  ON samples (start_time, metric, source, value);   -- 模式 A：strict
-- CREATE UNIQUE INDEX samples_latest_uidx ON samples (metric, source, start_time);        -- 模式 B：latest（二选一）

CREATE INDEX samples_metric_date_idx  ON samples (metric, local_date DESC);
CREATE INDEX samples_metric_time_idx  ON samples (metric, start_time DESC);
CREATE INDEX samples_source_time_idx  ON samples (source, start_time DESC) WHERE source <> '';

-- 压缩（segmentby 决定压缩率；压缩后不可 upsert，见 §6.4）
ALTER TABLE samples SET (
  timescaledb.enable_columnstore = true,          -- 2.18+ / 旧版用 timescaledb.compress
  timescaledb.segmentby = 'metric, source',
  timescaledb.orderby   = 'start_time DESC'
);
SELECT add_columnstore_policy('samples', after => INTERVAL '30 days');  -- 旧版 add_compression_policy
```

### 5.3 天级聚合（自建增量 rollup，按 local_date）

```sql
CREATE TABLE daily_metrics (            -- 每 (日期 × 指标 × 来源) 一行
  local_date date NOT NULL,
  metric     text NOT NULL,
  source     text NOT NULL,
  sample_count int NOT NULL,
  sum_value  double precision,
  mean_value double precision,
  min_value  double precision,
  max_value  double precision,
  first_at   timestamptz,
  last_at    timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (local_date, metric, source)
);

CREATE TABLE daily_metrics_day (        -- 每 (日期 × 指标) 一行：跨来源收敛后的最终值
  local_date date NOT NULL,
  metric     text NOT NULL,
  value      double precision NOT NULL, -- 累计型 = max(逐来源 sum)；瞬时型 = 样本数加权 mean
  sample_count int NOT NULL,
  source_count int NOT NULL,
  sources    text[] NOT NULL DEFAULT '{}',
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (local_date, metric)
);
CREATE INDEX daily_metrics_day_metric_idx ON daily_metrics_day (metric, local_date DESC);
```

**为什么不用 TimescaleDB continuous aggregate 做天聚合**：官方限制“cagg 内不支持依赖本地时区的函数”，只能用**显式固定时区**的 `time_bucket`。而我们的口径是“沿用每条记录自带的本地日期”（跨时区旅行/夏令时都要正确），必须按 `local_date` 分桶。且单人数据量（几年 × 每日数百~数千样本 ≈ 百万行）下，按日增量重算的成本可以忽略。因此：**自建 rollup 表 + 增量重算**为默认；如未来数据量上量或想要 cagg，可再加一个固定时区的 cagg 作补充。

### 5.4 事件类表

```sql
CREATE TABLE workouts (
  id            bigserial PRIMARY KEY,
  start_time    timestamptz NOT NULL,
  end_time      timestamptz NOT NULL,
  name          text NOT NULL,      -- 跑步 / 骑行 ...
  duration_min  double precision,
  total_energy  double precision, energy_unit text,
  total_distance double precision, distance_unit text,
  source        text, local_date date NOT NULL, tz_offset_min smallint NOT NULL DEFAULT 0,
  meta          jsonb,                        -- 心率区间、路线摘要等
  ingest_run_id bigint,
  UNIQUE (name, source, start_time)           -- 幂等
);
CREATE INDEX workouts_date_idx ON workouts (local_date DESC);

CREATE TABLE sleep_sessions (
  id bigserial PRIMARY KEY,
  local_date date NOT NULL,
  sleep_start timestamptz, sleep_end timestamptz,
  in_bed_start timestamptz, in_bed_end timestamptz,
  total_sleep_hr double precision, asleep_hr double precision,
  core_hr double precision, deep_hr double precision, rem_hr double precision,
  awake_hr double precision, in_bed_hr double precision,
  source text, tz_offset_min smallint NOT NULL DEFAULT 0,
  ingest_run_id bigint,
  UNIQUE (local_date, source, sleep_start)
);

CREATE TABLE sources (                  -- 来源治理：iPhone / Watch 归类、是否参与 max
  source text PRIMARY KEY,
  device_class text NOT NULL DEFAULT 'unknown',  -- phone | watch | app | unknown
  preferred boolean NOT NULL DEFAULT false,      -- 参与跨来源收敛时的优先级提示
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now()
);
```

### 5.5 摄入审计（幂等与水位治理）

```sql
CREATE TABLE ingest_runs (
  id            bigserial PRIMARY KEY,
  transport     text NOT NULL,          -- rest | mqtt | cli
  automation_id text, automation_name text,
  session_id    text,                   -- ★ HAE 每次请求的唯一 ID，用于幂等
  period        text, aggregation text, -- header 透传
  payload_sha256 text,
  payload_bytes bigint,
  archived_path text,
  received_at   timestamptz NOT NULL DEFAULT now(),
  sample_count  int NOT NULL DEFAULT 0,
  inserted      int NOT NULL DEFAULT 0,
  duplicates    int NOT NULL DEFAULT 0,
  updated       int NOT NULL DEFAULT 0,
  status        text NOT NULL DEFAULT 'ok',   -- ok | duplicate | error
  error         text,
  affected_dates daterange                     -- 触发 rollup 重算的范围
);
CREATE UNIQUE INDEX ingest_runs_session_uidx ON ingest_runs (session_id) WHERE session_id IS NOT NULL;
CREATE INDEX ingest_runs_received_idx ON ingest_runs (received_at DESC);

CREATE TABLE sync_watermark (           -- 每个 automation × metric 的水位，用于缺口检测
  automation_id text NOT NULL,
  metric        text NOT NULL,
  last_start_time timestamptz NOT NULL,
  last_local_date date NOT NULL,
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (automation_id, metric)
);
```

---

## 6. 同步实现要点

### 6.1 接收（REST）

1. `POST /api/ingest/hae`（支持 `?v=2`），鉴权 `X-Zhichang-Token` 或 `Authorization: Bearer`。
2. 读取 HAE 自动附带的 header：`automation-id`、`automation-name`、`automation-period`、`automation-aggregation`、`session-id`。
3. **幂等**：相同 `session-id` 已成功处理过 → 直接返回上次结果，不重复写。
4. **流式解析**：`json.Decoder` 按 token 流处理 `data.metrics[]` / `data.workouts[]`，不把整包读进内存；同时可选 spool 到 `raw_archive/`（文件 + sha256），便于事后重放与排错。
5. 大负载：`io.LimitReader` 上限可配（默认 512MB），支持 `Content-Encoding: gzip`；分批 flush（每 N 条或每 8MB 提交一次 staging）。
6. 响应：`{"run_id":..,"received":..,"inserted":..,"duplicates":..,"updated":..,"metrics":[...]}`，便于 App 活动日志核对。

### 6.2 归一化（时区的核心）

- 时间解析布局：`2006-01-02 15:04:05 -0700`（HAE 的 `date` 自带偏移）。
- **local_date 推导**：`t.In(fixedOffsetFromPayload).Format("2006-01-02")`，即**用记录自带的偏移**求本地日期；服务端 `TZ` 环境变量不参与。
- 回落规则（依次）：① 记录自带偏移 → ② 同批次/同 metric 已知偏移 → ③ 配置 `home_timezone`（如 `Asia/Shanghai`）→ ④ UTC。睡眠聚合记录的 `date` 只有 `2006-01-02`（无偏移），走 ②③。
- 库内一切 `timestamptz` 存 UTC；查询连接 `SET LOCAL timezone` 仅影响**展示格式化**，不影响 `local_date` 分桶。
- 数值字段按 `metric_defs.value_field` 提取：`qty` / 心率的 `Avg`（同时把 `Min`/`Max` 存 `value_min`/`value_max`）/ 睡眠的 `totalSleep` 等。

### 6.3 去重与写入

- **模式 A（strict，默认对齐你的口径）**：唯一索引 `(start_time, metric, source, value)` + `ON CONFLICT DO NOTHING`。同一来源、同一开始时间、同一数值的重复推送被丢弃；计数进 `duplicates`。
- **模式 B（latest）**：唯一索引 `(metric, source, start_time)` + `ON CONFLICT DO UPDATE SET value=EXCLUDED.value ... WHERE IS DISTINCT FROM`。额外好处：累计量按小时分组时，同一小时的步数在当天内会增长，后到的更大值能覆盖旧值。**代价**：同一来源同一时刻两个不同瞬时值只保留最后一条。
- 建议：**累计量用 B、瞬时量用 A** —— 但这需要表级二选一。折中做法：统一用 B，并在 upsert 里加 `WHERE EXCLUDED.value IS DISTINCT FROM samples.value`，对瞬时量而言同值覆盖 = 去重，不同值的极小概率冲突可接受（HAE 已按小时/更粗粒度聚合）。**此项列为决策点 D3 由你定。**
- 高吞吐写入：`pgx` `CopyFrom` 写入 `UNLOGGED` staging 表（每 run 一张临时表，事务结束即 DROP），再 `INSERT INTO samples SELECT ... FROM staging ON CONFLICT ...`（COPY 本身不支持 ON CONFLICT，必须走 staging 合并）。
- 事务粒度：单次请求一个事务；staging 分批（5000 行/批）避免超长事务与锁放大。
- **跨设备重复不在入库阶段消除**（iPhone 与 Watch 的 `source` 不同，`(source,start,value)` 去不掉），而是**在跨来源聚合时取 max/加权 mean 消除**，这正符合你定的口径。`sources` 表记录各来源的 `device_class`，用于仪表盘“数据来源”页展示与可选的人工指定 preferred。

### 6.4 rollup 增量重算

- 写入完成后收集受影响的 `(metric, local_date)` 集合（含 `end_time` 跨天的情况），入 `rollup_recompute` 队列（内存队列 + DB 兜底去重），异步重算 `daily_metrics` 与 `daily_metrics_day`（`DELETE` + `INSERT ... SELECT`，按天为单位）。
- 重算 SQL 严格按 `metric_defs.day_agg` 动态拼装（白名单 switch，不拼用户字符串）：
  - 累计型：`sum_value = sum(value)`；`daily_metrics_day.value = max(per_source_sum)`
  - 瞬时型：`mean_value = avg(value)`；`daily_metrics_day.value = sum(sum_value)/sum(sample_count)`（样本数加权，等价于全样本 mean）
  - interval/last：按定义取 `max` / 末次值
- 幂等：重算只覆盖受影响日期，重复执行结果一致；`zhichangctl recompute --from --to` 可全量/区间重建。
- **与压缩的冲突**：压缩后的 chunk 不允许插入/更新。策略是把压缩阈值设为 30 天（正常同步只碰最近几天），`zhichangctl backfill/recompute` 在写老数据前先 `decompress_chunk` 对应区间，完成后再 `compress_chunk`。

### 6.5 缺口检测与补数

- 定时任务对比 `sync_watermark` 与 `daily_metrics_day`，标记“缺失日/不完整日”，在 `/sync` 页高亮。
- 补数：HAE 手动导出（过去 7 天 / 自定义）→ 走同一 REST 端点；或 `zhichangctl import --file export.json`（离线 JSON 导入，内部复用同一 pipeline）。

### 6.6 MQTT 旁路（可选）

- 订阅 `zhichang/hae/+`，QoS 0，解析复用同一归一化器；因无 `session-id`，幂等退化为 `(automation 主题 + payload sha256)` 去重。
- 配置默认关闭；文档中明确其“可能丢消息、不保证完整”的局限。

---

## 7. 查询与 API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | 存活 + 版本 + DB 状态 |
| GET | `/api/metrics` | 指标字典（中文名、单位、口径、数据覆盖区间） |
| GET | `/api/summary/today` | 今日概览（步数/活动能量/静息心率/睡眠/体重/最新同步时间 + 环比） |
| GET | `/api/daily?from&to&metrics=a,b` | 天级序列（读 `daily_metrics_day`） |
| GET | `/api/series?metric&from&to&granularity=day\|week\|month&sources=` | 趋势序列 |
| GET | `/api/samples?metric&from&to&limit&offset` | 明细（排障用，上限 1000） |
| GET | `/api/sleep?from&to` | 睡眠分期 |
| GET | `/api/workouts?from&to` | 运动记录 |
| GET | `/api/sources` | 来源清单、device_class、末次出现 |
| GET | `/api/sync/status` | 最近同步、水位、缺失日 |
| POST | `/api/ingest/hae` | 数据接入（需 token） |

查询统一走 `query` 层：先按 `metric_defs` 决定聚合函数，再拼 SQL（全部参数化绑定，指标名走字典白名单校验）。

---

## 8. Web 仪表盘

- **`/` 今日概览**：核心指标卡片（步数、活动能量、静息心率、睡眠时长、体重、HRV）+ 较昨日/7 日均值环比；睡眠分期条；今日心率曲线；数据新鲜度提示。
- **`/trends` 趋势**：多指标叠加/切换，区间 7/30/90/365/自定义，粒度 日/周/月，支持缩放、游动平均（7 日）、同环比阴影。
- **`/sleep`**：睡眠时长与分期堆叠柱、入睡/起床时刻散点。
- **`/workouts`**：运动列表 + 按类型/时长/能耗统计。
- **`/sync`**：自动化清单、最近同步时间、水位、缺失日高亮、摄入 run 日志（来源/条数/重复数）。
- **`/settings`**：指标开关与展示单位、home_timezone、跨来源策略查看。
- 视觉：浅色为主（可跟随系统）、紧凑单行卡片、中文字体栈 + `font-smoothing: auto`、表格化数字对齐；下拉/区间选择器在视口允许时一次展开不滚动。

---

## 9. MCP 服务器

传输：`zhichangd mcp`（stdio，供 Claude Desktop/本地客户端）+ `zhichangd serve` 内嵌 `/mcp`（Streamable HTTP，需 token）。二者复用同一套 tool 实现。

| Tool | 能力 |
|---|---|
| `health_list_metrics` | 指标目录：中文名、单位、口径、覆盖区间 |
| `health_daily_summary` | 指定日期全貌（步数/能量/心率/睡眠/体重） |
| `health_query` | 按指标 + 区间 + 粒度查询时序（自动套用 sum/mean） |
| `health_compare` | 两个区间对比（变化量、变化率、显著性提示） |
| `health_trend` | 趋势与移动平均、极值/最好最差日 |
| `health_latest` | 各指标最新值与时间 |
| `health_sleep` | 睡眠分期与作息规律 |
| `health_workouts` | 运动记录与统计 |
| `health_correlation` | 两指标相关性（按天对齐，Pearson + 散点摘要） |
| `health_data_coverage` | 数据覆盖/缺失日/新鲜度（回答“数据可靠吗”） |
| `health_insight`（可选） | 规则型洞察：偏离 30 日基线、连续达标/下滑等 |

- Resources：`metric://catalog`（指标字典 JSON）、`health://coverage`（覆盖日历）。
- 约束：**只读**；默认不提供任意 SQL tool（需要时可作为 `dev` 开关单独开启并强制只读事务）；所有入参走字典白名单与参数绑定；结果限制行数（默认 1000）避免刷屏。

---

## 10. 部署与运维

- `Dockerfile` 多阶段：`node` 构建前端 → `go build` → `alpine`；非 root + `PUID/PGID` 对齐宿主机目录属主；前端经 `embed.FS` 内嵌，**单二进制可独立运行**。
- `docker-compose.yml`：`timescale/timescaledb:2.28-pg17`（数据卷 + WAL）+ `zhichang`；可选 Caddy 反代（HTTPS / Tailscale）。
- 配置全部走环境变量：`ZHICHANG_DB_DSN`、`ZHICHANG_INGEST_TOKEN`、`ZHICHANG_HOME_TZ`、`ZHICHANG_DEDUP_MODE`、`ZHICHANG_RAW_ARCHIVE_DIR`、`ZHICHANG_MCP_ENABLED`、`ZHICHANG_MCP_TOKEN`、`ZHICHANG_MQTT_ENABLED`…
- 启动即执行迁移（可 `--migrate=false` 关闭）；`zhichangctl doctor` 自检 DB 扩展、hypertable、压缩策略、rollup 一致性。

---

## 11. 里程碑

| 阶段 | 内容 | 验收 |
|---|---|---|
| M1 | 骨架：配置、迁移、pgx 仓储、hypertable、指标字典种子 | `doctor` 通过；空库可启动 |
| M2 | 接入：HAE v2 解析（metrics + workouts + sleep）、REST 接收、幂等、staging upsert | 用样例 JSON 导入，重复导入两次 `duplicates>0` 且总量不变 |
| M3 | 聚合：rollup 增量重算、跨来源 max/加权 mean、时区 local_date 验证 | 跨时区样例数据按本地日期分桶正确 |
| M4 | 查询 API + 前端仪表盘（概览/趋势/睡眠/运动/同步） | 本地浏览器实测 |
| M5 | MCP（stdio + HTTP）+ 文档 | MCP Inspector / Claude 客户端可查 |
| M6 | 部署：Docker、compose、Caddy、README/HAE 配置指南、备份脚本 | 真机 HAE 同步一次成功 |

---

## 12. 待确认决策点（请逐项答复，或说“按推荐”）

- **D1 前端框架**：SvelteKit(adapter-static，产物 embed 进 Go 单二进制)【推荐】 / React+Vite（同样 embed，但你更熟 React） / 其他
- **D2 时序库**：TimescaleDB【推荐】 / GreptimeDB（需接受 SQL 与 upsert 能力受限）
- **D3 去重模式**：A strict `(source,start,value)` DO NOTHING（严格按你的口径）【推荐，先严格再观察】 / B latest `(source,start)` DO UPDATE（可修正累计量增长，但同刻不同瞬时值只留一条） / 累计量用 B + 瞬时量用 A（需拆两张表）
- **D4 天聚合**：自建 rollup 表按 `local_date` 增量重算【推荐】 / TimescaleDB cagg（固定时区，跨时区不准）
- **D5 MQTT**：实现可选旁路（默认关闭）【推荐】 / 完全不实现 / 与 REST 并列默认开启
- **D6 MCP 传输**：stdio + Streamable HTTP 双通道【推荐】 / 仅 stdio / 仅 HTTP
- **D7 一期数据范围**：健康指标 + 睡眠 + 运动【推荐】 / 再加 ECG、心率通知、心理状态、周期、症状、药物（工作量增加）
- **D8 认证与多用户**：单用户 + 单一 token【推荐】 / 多用户（每用户独立 token 与数据隔离，表加 user_id）
- **D9 保留与压缩**：原始样本压缩阈值 30 天、原始永久保留（rollup 长期保留）【推荐】 / 原始只保留 N 天后降采样
- **D10 时区回落**：payload 自带偏移优先 → 回落 `home_timezone`（如 Asia/Shanghai）【推荐】

---

## 13. 参考（仅借鉴思路，不照搬）

- `chimph/health-server`、`HealthyApps/health-auto-export-server`：REST 接收 + 落库的端点形态
- `health-auto-export-mcp-server`、`Lybron/health-auto-export`：MCP tool 设计与 JSON 解析细节
- `irvinlim/apple-health-ingester`：Apple Health XML 导入的指标归一化思路
- 官方文档：HAE REST API / MQTT / 导出格式（本文事实均来自 help.healthyapps.dev 核实）
