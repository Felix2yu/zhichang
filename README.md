# 知常（Zhichang）

> 复命曰常，知常曰明。

自托管的 Apple Health 数据平台：Health Auto Export 自动同步入库（TimescaleDB），提供 Web 仪表盘与 MCP 自然语言查询。

- **后端**：Go 1.27 · chi · pgx v5 · TimescaleDB 2.30（PostgreSQL 18）
- **前端**：SvelteKit 3 · Svelte 5 · Tailwind v4 · ECharts 6（构建产物内嵌进 Go 二进制）
- **接入**：Health Auto Export REST API（主通道）· MQTT（可选旁路，默认关闭）
- **AI**：内置 MCP 服务器（stdio + Streamable HTTP），9 个只读工具

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
| 分桶 | 一律按 `local_date`（不是服务端时区），跨时区旅行/夏令时都正确 |

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
| `ZHICHANG_MCP_ENABLED` / `_TOKEN` / `_PATH` | `true` / 空 / `/mcp` | MCP over HTTP |
| `ZHICHANG_MQTT_ENABLED` / `_BROKER` / `_TOPIC` | `false` | MQTT 旁路 |
| `ZHICHANG_MIGRATE_ON_BOOT` | `true` | serve 启动时自动执行迁移 |
| `ZHICHANG_LOG_LEVEL` | `info` | 日志级别 |
| `PUID` / `PGID` | `1000` | 容器内运行身份（Docker） |

## MCP

内置 9 个只读工具：`health_list_metrics`、`health_daily_summary`、`health_query`、`health_compare`、`health_latest`、`health_sleep`、`health_workouts`、`health_data_coverage`、`health_correlation`。

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
- 一期指标范围：健康指标 + 睡眠 + 运动；ECG、心率通知、心理状态、周期、症状、药物留待后续。
