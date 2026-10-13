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
