# 知常（zhichang）项目长期笔记

## 定位与技术栈
- 自托管 Apple Health 数据平台：Health Auto Export → Go 服务 → TimescaleDB → Web 仪表盘 + MCP。
- Go 1.27.2（chi 5.4 + pgx v5.11 + modelcontextprotocol/go-sdk v1.8）· TimescaleDB 2.30.2 / PG18 ·
  SvelteKit 3.0.1 + Svelte 5.57 + Vite 8.3 + Tailwind 4.3 + ECharts 6.1 · TypeScript 6.0.3（**不能用 7.x**）。
- 前端构建产物（web/build）经 embed 打进 Go 二进制，单二进制即可提供站点。

## 数据口径（核心约定，勿改）
- 去重：唯一索引 `(start_time, metric, source, value)` + `ON CONFLICT DO NOTHING`；同 `session-id` 重提交回放上次结果。
- 跨设备重复（iPhone/Watch）不在入库阶段消除，在天级聚合收敛：累计量取各来源总量 `max`，瞬时量按样本数加权 `mean`。
- 时区：`local_date` 由 payload 自带偏移推导，服务端时区不参与入库；库内 timestamptz 全 UTC；无偏移回落 `ZHICHANG_HOME_TZ`。
- 天聚合按 `local_date` 分桶，不用 TimescaleDB cagg（cagg 不支持依赖本地时区的函数）。
- 写入走 `pgx CopyFrom` → UNLOGGED staging → `INSERT ... ON CONFLICT` 合并（COPY 不支持 ON CONFLICT）。
- **单位归一（硬约定）**：入库时按字典 `metric_defs.unit` 换算（能源 kJ→kcal、距离 m/mi→km、体重 lb→kg），
  value 与其 unit **一起**改（含 Min/Max）；展示单位一律取 `preferred_unit`，所以库里必须已是字典口径。
  只登记数值等价换算；**标签差异**（count/min 与 bpm、ml/(kg·min) 与 mL/min·kg）绝不换算。
  未知单位宁可原样保留也不猜系数。实现在 internal/domain/metric/unit.go 的 `ConvertUnit`。
- **解析层容错（硬约定）**：单个字段的形态变化**不允许**导致整份导出解析失败。
  `SourceName` 兼容 `"名字"` 与 `{name, identifier}`；`Float`/`Quantity` 容忍数字/字符串/对象，
  认不出按缺失处理。历史教训：HAE 把 `workouts[].source` 从字符串改成对象后，按 string 解析抛
  UnmarshalTypeError → 端点 400，34 个指标 + 7 运动 + 14 晚睡眠一条都入不了库。
- 运动口径：`duration_min` 一律存分钟——HAE 老版 `duration` 给分钟、新版给**秒**且无单位标记，
  按「隐含时长更接近 `end-start`」判定（`workoutDurationMin`）；距离新版叫 `distance`、老版叫
  `totalDistance`，取有值者；能量统一 kcal、距离统一 km。

## 本地开发
```bash
docker run -d --name zhichang-tsdb -p 5433:5432 -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=zhichang timescale/timescaledb:2.30.2-pg18
export ZHICHANG_DB_DSN="postgres://postgres:postgres@127.0.0.1:5433/zhichang?sslmode=disable"
ZHICHANG_INGEST_TOKEN=test-token ZHICHANG_HTTP_ADDR=:8899 go run ./cmd/zhichangd
cd web && pnpm dev   # :5173，已代理 /api
ZHICHANG_TEST_DSN=<同上> go test ./...   # 启用集成测试
```
- 本机 Go 在 /opt/homebrew/bin/go（系统 PATH 无 Go）；Node v26.11.1、pnpm 12.11.2。
- curl 探测本机端口必须加 `--noproxy '*'`。

## 踩过的坑
- SvelteKit 3：`svelte.config.js` 废弃，配置改到 `vite.config.ts` 的 `sveltekit({ adapter, ... })`；`$lib` 移除，用 `#lib`（package.json 的 `imports` subpath）。
- TypeScript 7.0.2 会让 Kit 3 的 tsconfig 解析崩溃（`load_tsconfig` undefined），降到 6.0.3。
- `node:26-alpine` 无 corepack，Docker 里要 `npm i -g pnpm@12.11.2`。
- 容器内 `proxy.golang.org` 不可达，Dockerfile 需 `ENV GOPROXY=https://goproxy.cn,direct`。
- pgx：`pgxpool.Conn` 没有 `PgConn()`，要 `c.Conn().PgConn()`；date 列扫描用 `pgtype.Date`（`*string` 不行）。
- MCP stdio 冒烟测试要 `(printf ...; sleep 4) | binary`，管道立即 EOF 会拿不到响应。
- **JSON 契约（硬约定）**：store 层任何返回 slice 的方法必须 `make([]T, 0, N)` 初始化，
  绝不写 `var out []T`——nil slice 会被 encoding/json 编成 `null`，前端拿到后
  `.map()` / 展开 / `.at()` 直接抛 TypeError，SvelteKit 错误边界接管后整页显示 500，
  而容器日志一片干净，极易误判为服务端问题。空结果集必须编成 `[]`。
  前端侧 `web/src/lib/api.ts` 的 `arr<T>()` 做第二层兜底，新增 api.* 方法时记得套上。
  回归由 internal/store/postgres/json_contract_test.go 锁定。
- 本机没有 docker CLI（`docker: command not found`，沙箱内外均是），起不了容器化的临时库；
  但 **internal/testdb 会用 embedded-postgres 在测试进程内自举一个真 PG18**，
  所以 `go test ./...` 本身就能跑通集成测试（api/mcp/pipeline/store/httpping 全都在真库上跑），
  无需 ZHICHANG_TEST_DSN。差异：测试库里 samples 是普通表、不是 hypertable（建表时跳过
  CREATE EXTENSION timescaledb 与 create_hypertable）；要覆盖分区行为才需要指向真 TimescaleDB。
  注意首次运行会下载 PG 二进制到用户缓存目录。
