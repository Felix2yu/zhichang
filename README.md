# 知常（Zhichang）

> 复命曰常，知常曰明。

自托管的 Apple Health 数据平台：Health Auto Export 自动同步入库（TimescaleDB），提供 Web 仪表盘与 MCP 自然语言查询。

- **后端**：Go 1.27 · chi · pgx v5 · TimescaleDB 2.30（PostgreSQL 18）
- **前端**：SvelteKit 3 · Svelte 5 · Tailwind v4 · ECharts 6（构建产物内嵌进 Go 二进制）
- **接入**：Health Auto Export REST API（主通道）· MQTT（可选旁路，默认关闭）
- **AI**：内置 MCP 服务器（stdio + Streamable HTTP），11 个只读工具

## 快速开始

### Docker Compose（推荐）

```bash
cp deploy/.env.example deploy/.env   # 设置 ZHICHANG_INGEST_TOKEN
cd deploy && docker compose up -d
docker compose logs -f zhichang
```

服务地址 `http://<host>:8787`，数据目录 `deploy/data`（payload 归档）与 TimescaleDB 卷。

镜像由 CI 发到 `ghcr.io/felix2yu/zhichang`（`latest` / `vX.Y.Z`，amd64 + arm64）。
从源码构建请先 `make web build` 产出 `bin/zhichangd`，再 `make docker`——镜像是装配式的，
只把预编译的二进制拷进去（前端在 `go build` 时已内嵌）。

### 本地开发

```bash
# 1) 起一个 TimescaleDB
docker run -d --name zhichang-tsdb -p 5433:5432 \
  -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=zhichang \
  timescale/timescaledb:2.30.2-pg18

# 2) 迁移 + 指标字典
export ZHICHANG_DB_DSN="postgres://postgres:postgres@127.0.0.1:5433/zhichang?sslmode=disable"
make migrate seed doctor

# 3) 起服务（前端另开一个终端）
make run                                   # :8787
cd web && pnpm dev                          # :5173，已代理 /api 到 8787
# 后端跑在别的端口时：ZHICHANG_BACKEND=http://127.0.0.1:<port> pnpm dev
```

生产构建用 `make`（先构建前端再编译 Go，前端产物经 `embed` 打进二进制）。

## 测试

```bash
make test    # 单元 + 集成测试
make cover   # 按全仓语句口径汇总覆盖率
```

集成测试打的是真库：`internal/testdb` 在测试进程里自举一个 PostgreSQL 18
（embedded-postgres，首次运行下载发行包约 30MB，之后离线可跑），**不需要 docker**。

口径差异要说清：基座建 schema 时会跳过 `0001` 里 `CREATE EXTENSION timescaledb` 和
`create_hypertable` 这两条，所以测试库里 `samples` 是普通表而不是 hypertable——
chunk 裁剪、压缩策略那类分区行为不在覆盖范围内（它们本来也不由这些查询决定）。
查询侧不依赖任何 TimescaleDB 专有函数（全仓没有 `time_bucket`），所以 SQL 语义与生产一致。
要把这一层也压上真库，设 `ZHICHANG_TEST_DSN` 指向一个可用的 TimescaleDB，此时完全不走自举路径。

## 数据接入

详见 [docs/hae-setup.md](docs/hae-setup.md)。要点：

- 自动化类型选 **REST API**，格式 **JSON**，导出版本 **v2**
- 日期范围选 **自上次同步**；汇总数据开启、时间分组按**小时**
- 大负载请开启 **批量请求**
- Header 加 `X-Zhichang-Token: <ZHICHANG_INGEST_TOKEN>`
- 端点：`https://<你的域名>/api/ingest/hae`

MQTT 仅作旁路（`ZHICHANG_MQTT_ENABLED=true`）：HAE 的 MQTT 是 QoS 0、不保留、无重放，丢消息不可知，**不建议作为唯一通道**。

## 关键处理规则

| 主题 | 口径 |
|---|---|
| 去重 | 唯一索引 `(start_time, metric, source, value)` + `ON CONFLICT DO NOTHING`；同一 `session-id` 重复提交直接回放上次结果 |
| 跨设备重复 | iPhone 与 Apple Watch 的 `source` 不同，入库阶段不消除；**天级聚合时收敛**——累计量取各来源总量的 `max`，瞬时量取样本数加权 `mean` |
| 时区 | 记录自带偏移优先，`local_date` 由自带偏移推导（服务端时区不参与入库）；库内 `timestamptz` 全 UTC；无偏移时回落 `ZHICHANG_HOME_TZ` |
| 日内聚合 | 步数/能量/距离等累计量 `sum()`；心率/体重/HRV 等瞬时量 `mean()`；身高、VO2max 取当日 `last` |
| 单位 | **入库时按字典 `metric_defs.unit` 换算**（能量 kJ→kcal、距离 m/mi→km、体重 lb→kg），值与单位一起改；不可换算的标签差异（`count/min` 与 `bpm`、`ml/(kg·min)` 与 `mL/min·kg`）不动。展示单位一律取字典的 `preferred_unit`，因此库里的值必须已经是字典口径——否则会出现「1091 kJ 标成 1091 kcal」这类虚高 4.18 倍且界面上看不出错的数值 |
| 运动记录 | 时长一律存分钟（`duration_min`），距离统一 km、能耗统一 kcal。HAE 老版本 `duration` 给分钟、新版本给秒且无单位标记，按「隐含时长更接近 `end-start`」判定；距离字段新版叫 `distance`、老版叫 `totalDistance`，取有值的那个 |
| 上游字段容错 | 解析层对 `source`（字符串或 `{name, identifier}` 对象）与数量字段（对象/裸数值/数组）一律容错，认不出按缺失处理——**绝不允许单个字段的形态变化导致整份导出 400**。HAE 换过 `workouts[].source` 的形态，按字符串解析会让 34 个指标一条都入不了库 |
| 指标别名 | 同一概念的上游多种写法在**入库时收敛到字典的规范名**：`blood_oxygen_saturation`→`oxygen_saturation`、`apple_exercise_time`→`exercise_time`、`apple_stand_time`→`stand_time`、`apple_stand_hour`→`stand_hour`、`distance_walking_running`→`walking_running_distance`、`weight_&_body_mass`/`weight_body_mass`→`body_mass`、`apple_sleeping_wrist_temperature`→`sleeping_wrist_temperature`。别名表是 `internal/domain/metric/alias.go` 的 `aliases`（唯一事实来源）<br>不收敛的后果是**双向失效**：数据躺在旧名下、字典里的卡片永远空图，界面上既看不到数值也看不出哪里错了。语义不同的指标绝不合并（`apple_stand_time` 是站立分钟数、`apple_stand_hour` 是站立小时数）<br>历史行由 `migrations/0004_metric_aliases.sql` 一次性搬到规范名下（撞唯一约束时删旧留新），并删除字典里的旧条目——留着会让趋势页出现两个同名 chip，其中一个永远无数据。别名表与迁移表的一致性由 `alias_migration_test.go` 锁定 |
| 来源名 | 上游写的来源名用的是 **NBSP**（U+00A0）而不是普通空格——实测真实导出里 249 个点位全是「子翼的Apple\u00a0Watch」；多来源还会被拼成一个串（`子翼的Apple Watch\|子翼的iPhone`），且管两侧的空格不固定。解析出口由 `sample.CleanSource` 统一归一：非 U+0020 的空白（NBSP / 表意空格 / 各种窄空格 / 制表换行）收成普通空格、零宽字符（U+200B/200C/200D/2060/FEFF）直接删掉、连续空白压缩、`\|` 两侧统一成一个空格<br>**只做空白等价替换**：不折叠大小写、不删标点、不猜语义、不拆分多来源——设备名是人写的，任何「聪明」的改写都可能把两台真不同的设备合并成一台<br>不归一的后果不是「看着别扭」：`sources` 表会多出一行肉眼一模一样的来源把样本数劈成两半，去重唯一索引 `(start_time, metric, source, value)` 也会因为 `source` 不同而把同一份数据入两遍<br>历史行由 `migrations/0007_source_normalize.sql` 一次性搬迁（撞唯一约束时删旧留新），受影响的日期排进重算队列重建天级聚合；两份归一的等价性由 `source_migration_test.go` 在真库上逐用例比对锁定 |
| 分桶 | 一律按 `local_date`（不是服务端时区），跨时区旅行/夏令时都正确 |
| 目标（goals） | 按指标存一个期望值：`goals` 表只存数值，单位与中文名一律从 `metric_defs` 取；`PUT/DELETE /api/goals` 必须带 `ZHICHANG_SETTINGS_TOKEN`，未配置时返回 503 保持只读。睡眠页达标口径 = 范围内 `total_sleep_hr ≥ 目标` 的晚数，没有总睡眠时长的夜晚不进分母 |
| 设置页 | 两类落点，不能混：**口味存本机**（`localStorage` 的 `zhichang.theme` / `default-range` / `duration-format` / `focus-metrics` / `trend-metrics`，换设备或清缓存要重设），**目标存服务端**（时长目标在 `goals`，预期入睡时间在 `app_settings`）。预期入睡时间用 `app_settings` 而不是往 `goals` 里塞一条假指标：`SetGoal` 按指标字典白名单校验，登记一条「入睡时间」会让 `/api/metrics`、MCP 的 `health_list_metrics` 和接入白名单多出一条永远没有样本的指标 |
| 明暗模式 | 结构色全部走 `app.css` 的 `@theme` 变量，暗黑只是 `html[data-theme='dark']` 里同名变量换值，业务代码一律引用变量、不写字面色值（写死一处就等于漏掉一个暗色场景）。ECharts 画的是 canvas，读不到 CSS 变量，所以在建 option 时经 `palette()` 取一次——这个读取同时把主题注册进 `$effect` 依赖，切换即重绘。分期色、指标主色这类**身份色**不另配一套暗色表，而是按明度统一提亮：色相与相互次序都不动，深睡在暗色下仍然是蓝。首屏由 `app.html` 的内联脚本定色，避免暗黑用户进来闪一帧浅色 |
| 入睡目标与评分 | 「就寝规律性」这一项的基准默认是最近 13 晚入睡中位数（Apple 口径）；设了预期入睡时间就改用目标值，头一晚也能打分。两者在界面上分开措辞（「目标 22:30」/「习惯 22:30」），因为「我定的」和「我一贯的」是两个不同的问题 |
| 睡眠时间表矩阵 | 横轴是「相对 18:00 的本夜分钟轴」，与分期时间线同一把尺子（凌晨的时刻不会把行切断）；留白 = 那晚没有分段记录，由前端按日期序列补，后端只回有数据的夜晚；`/api/sleep/stages/range` 服务端截到 120 天、前端最多画 90 晚 |
| 入睡后清醒（WASO） | 取**入睡时间窗内**清醒分段的时长合计，不含「躺下到睡着」之间的那段（那是入睡潜伏期）；与整晚清醒合计 `awake_hr` 相对，没有分段数据时不回退估算。睡眠中点由前端算：入睡与起床在「相对 18:00 的环形时刻轴」上的正中 |
| 睡眠窗生理指标 | 夜间心率/HRV/血氧/呼吸频率按 `sleep_sessions` 的**入睡时间窗** `[sleep_start, sleep_end)` 关联样本，不按 `local_date`（样本按各自起始日归属，跨零点的后半夜会整段漏掉；取 `{睡眠日, 次日}` 两天只为走索引/裁 chunk）；**一晚只认一个来源**——会话来源优先，否则窗口内样本数最多，不跨来源平均（手表 5 分钟一条、手机低频，平均会被采样密度带偏）；无 `sleep_start` 时退到上床/离床，同晚有午睡与夜觉时取夜觉；「夜间最低」取窗口内 `value_min` 的最小，与 iPhone 睡眠详情页同口径；「最低出现时刻」用打出该最低值的样本自带偏移还原，缺偏移则不显示 |
| 心电（ECG） | 事件数据，与睡眠/运动一样**不产生 samples**、不进天级聚合，也**不计入 `received`**（计进去会让 received 与落库条数对不上，掩盖真正丢数）；接收条数单独走响应里的 `ecg` 字段<br>判定 `classification` 是**本地化文本**（简中「窦性心律」/ 英文 `Sinus Rhythm`），入库时收敛成稳定码（`sinus_rhythm`、`atrial_fibrillation`、`inconclusive_low_heart_rate` …），原文并存：码用于统计筛选、原文用于核对与兜底展示。**绝不按整串比较判定**——换一次手机语言，历史数据就会与之后的分裂成两组<br>波形与元信息分两张表（`ecg_records` / `ecg_voltages`）：一次测量 15360 个点（约 120KB），同表会让列表查询每次把波形一起拉出来。时轴由 `start_time + i/采样率` 推出，逐点时间戳一律不存（上游那个字段的形态在版本间变过）；**因此任一采样点缺电压就整段波形不入库**，只留记录——按索引推时轴的前提是点数完整<br>电压单位（实测 `mcV`）原样保留、不换算，与其它单位口径一致 |

天级聚合落在 `daily_metrics`（每来源）与 `daily_metrics_day`（收敛值）两张表，由摄入写入时按受影响的 `(日期, 指标)` 入队异步重算；服务重启后会继续消费未完成的队列。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `ZHICHANG_DB_DSN` | `postgres://postgres:postgres@127.0.0.1:5432/zhichang?sslmode=disable` | 数据库连接 |
| `ZHICHANG_INGEST_TOKEN` | 必填 | 数据接入令牌（HAE 的 header） |
| `ZHICHANG_HTTP_ADDR` | `:8787` | 监听地址 |
| `ZHICHANG_HOME_TZ` | `Asia/Shanghai` | 无偏移记录回落的时区 |
| `ZHICHANG_DEDUP_MODE` | `strict` | `strict`（按值去重）/ `latest`（同来源同起点覆盖更新） |
| `ZHICHANG_RAW_ARCHIVE_DIR` | 空（不归档） | 原始 payload 归档目录，便于回溯 |
| `ZHICHANG_MAX_BODY_BYTES` | `536870912` | 单次请求体上限 |
| `ZHICHANG_SETTINGS_TOKEN` | 空（目标与设置只读） | 写接口的 Bearer 令牌，覆盖 `PUT/DELETE /api/goals` 与 `PUT/DELETE /api/settings`；留空即不开放修改 |
| `ZHICHANG_MCP_ENABLED` / `_TOKEN` / `_PATH` | `true` / 空 / `/mcp` | MCP over HTTP |
| `ZHICHANG_MQTT_ENABLED` / `_BROKER` / `_TOPIC` | `false` | MQTT 旁路 |
| `ZHICHANG_MIGRATE_ON_BOOT` | `true` | serve 启动时自动执行迁移 |
| `ZHICHANG_LOG_LEVEL` | `info` | 日志级别 |
| `PUID` / `PGID` | `1000` | 容器内运行身份（Docker） |

## MCP

内置 11 个只读工具：`health_list_metrics`、`health_daily_summary`、`health_query`、`health_compare`、`health_latest`、`health_sleep`、`health_sleep_detail`、`health_workouts`、`health_ecg`、`health_data_coverage`、`health_correlation`。

**stdio（Claude Desktop 等）**

```json
{
  "mcpServers": {
    "zhichang": {
      "command": "/path/to/zhichangd",
      "args": ["mcp"],
      "env": {
        "ZHICHANG_DB_DSN": "postgres://postgres:postgres@127.0.0.1:5433/zhichang?sslmode=disable",
        "ZHICHANG_HOME_TZ": "Asia/Shanghai"
      }
    }
  }
}
```

**Streamable HTTP（Claude Code / Cursor / VS Code / Codex）**

```bash
claude mcp add --transport http zhichang https://<你的域名>/mcp \
  --header "Authorization: Bearer <ZHICHANG_MCP_TOKEN>"
```

## 运维

只有一个二进制 `zhichangd`，`serve` 是默认子命令：

```bash
zhichangd migrate         # 执行迁移
zhichangd doctor          # 检查库、扩展、数据量、最近同步
zhichangd seed-metrics    # 补齐内置指标字典（已存在的不覆盖）
zhichangd version         # 打印版本（CI 注入，本地为 dev）
```

手工补数：HAE 手动导出 JSON → `POST /api/ingest/hae`。

## PWA

站点可安装为 PWA：图标七件套、`manifest.webmanifest`（`any` 与 `maskable` 分离）、
Service Worker 缓存名按构建版本变化并在 `activate` 阶段清旧。

- `/api/**` 一律走网络、失败不回落壳页，未知 API 路径返回 404 JSON 而不是首页 HTML
- SW 由 `web/scripts/gen-sw.mjs` 从 `web/src/sw-template.js` 注入构建版本后生成
  （SvelteKit 3 不支持 `transformIndexHtml`，`vite-plugin-pwa` 注入不了注册脚本）

## 已知限制

- iOS 侧：Health Auto Export 仅在设备解锁且后台刷新可用时运行，锁屏/低电量会延迟同步（Apple 限制，无法绕过）。
- `strict` 去重模式下，同一来源同一起点但数值被源端修正的累计量不会覆盖（需切 `latest` 模式并重建索引）。
- 压缩策略默认 30 天后开启，压缩后的 chunk 不能写入；回填历史数据前需先解压对应区间。
- 一期指标范围：健康指标 + 睡眠 + 运动 + 心电图；心率通知、心理状态、周期、症状、药物留待后续。
- 明暗模式在应用内是全站生效的；但 PWA 的 `manifest.webmanifest` 只有一个 `background_color`，装到主屏后系统启动屏仍是浅色，要两态得发布两份 manifest。
