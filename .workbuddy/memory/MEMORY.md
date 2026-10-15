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
- **指标别名（硬约定）**：同一概念的上游多种写法必须在**入库时收敛到字典的规范名**，
  别名表是 `internal/domain/metric/alias.go` 的 `aliases` + `Canonical()`。
  字典里**一个概念只登记一条**——早期「同概念登记两条、共用中文名」的做法已废弃，
  它会让趋势页选择器出现两个同名 chip（其中一个永远无数据）。
  不收敛的后果是双向失效：数据躺在旧名下、字典卡片永远空图，界面上看不出哪里错了。
  语义不同的指标绝不合并（`apple_stand_time` 是分钟数、`apple_stand_hour` 是小时数）。
  改别名表必须同步改 `migrations/0004_metric_aliases.sql`（历史行搬迁），
  一致性由 `store/postgres/alias_migration_test.go` 锁定。
- **字典 ↔ 前端入口（硬约定）**：趋势页选择器是
  `defs.filter(d => group.metrics.includes(d.metric) && d.enabled)`，
  **没写进 `web/src/lib/metric-groups.ts` 的 METRIC_GROUPS 的指标在界面上没有入口**
  （数据在库里、接口也返回，但用户画不出图也搜不到）。字典新增指标必须同步改前端分组，
  并由 `internal/domain/metric/ui_contract_test.go` 双向校验（含 preferred_unit 是否在
  UNIT_DIGITS 里——查不到会按 0 位小数显示，0.2811 m/s 会显示成「0」）。
- **会动数据的迁移**：项目允许写数据迁移（`migrations/000N_*.sql`，启动时自动按序应用）。
  写这类迁移的硬要求：① 先 DELETE 冲突行再 UPDATE，否则撞上唯一/主键约束会让整份迁移报错、
  服务起不来；② 幂等（没有匹配行时全是空操作）；③ 搬完 samples 必须把受影响 (日期, 指标)
  排进 `rollup_queue`；④ 派生表（daily_metrics / daily_metrics_day / rollup_queue /
  sync_watermark / goals）一起搬；⑤ 引用 goals 要用 `to_regclass('goals') IS NOT NULL` 守卫
  ——internal/testdb 自举时跳过 0003 但仍会先跑 0004。
- **测试忌用「当下恰好未登记」的真实指标**：`environmental_audio_exposure` 被登记进字典后，
  三个断言它「未登记」的用例静默失效。要测未知指标行为就用构造出来的假名字
  （如 `zzz_probe_unregistered`）。新增/移除指标时先 grep 全仓测试里有没有拿它当反例。
- **心电（ECG）是事件流，不是指标**：与睡眠/运动一样**不产生 samples**、不进天级聚合、
  **不计入 `received`**（计进去会让 received 与落库条数对不上，掩盖真正丢数），
  条数单独走 `IngestStats.ECG` / 响应里的 `ecg` 字段。`IsEmpty()` 必须把 ecg 算进去
  （否则「只有心电图」的导出被判空 → 200/空、日志一片干净），`Count()` 不能算。
- **心电判定必须归一化（硬约定）**：`classification` 是**随手机语言本地化的显示串**
  （简中「窦性心律」/ 英文 `"Sinus Rhythm"`），**绝不是稳定枚举键**。按整串比较或直接入库，
  换一次手机语言历史数据就会与之后的分裂成两组。唯一事实来源是
  `internal/domain/ecg/classify.go` 的 `Classify()` → 9 个稳定码，库内**原文与码并存**
  （码用于统计筛选、原文用于核对与兜底展示；码认不出时界面回落原文并标注「上游原文」）。
  关键词判定**顺序敏感**：「低心率或高心率」只能给笼统 inconclusive；
  v2 的「高心率-侦测不到心房颤动」既含「心房颤动」又含「高心率」，必须靠否定词抢先分流。
- **波形与元信息分两张表**（`ecg_records` / `ecg_voltages`）：一次测量 15360 点约 120KB，
  与元信息差 5 个数量级；同表会让列表查询每次把波形一起拉出来（TOAST 只让大字段不进缓存，
  并不隔离读取开销）。时轴由 `start_time + i/采样率` 推出，逐点时间戳一律不存，
  **因此任一点缺电压就整段波形不入库、只留记录**。电压单位原样保留（实测 `mcV`，
  HAE 文档举例写 `V`）——不换算、不纠正。记录已存在时仍尝试写波形，让后到的波形能补挂上去。
- **迁移器按文件名记「已应用」**：给已提交过的迁移追加语句是无效的——已部署环境升级时
  不会重跑，新表/新列永远建不出来。要加就新开一个 `000N_*.sql`。
- **解析层容错（硬约定）**：单个字段的形态变化**不允许**导致整份导出解析失败。
  `SourceName` 兼容 `"名字"` 与 `{name, identifier}`；`Float`/`Quantity` 容忍数字/字符串/对象，
  认不出按缺失处理。历史教训：HAE 把 `workouts[].source` 从字符串改成对象后，按 string 解析抛
  UnmarshalTypeError → 端点 400，34 个指标 + 7 运动 + 14 晚睡眠一条都入不了库。
- **来源名归一（硬约定）**：HAE 写的设备名用 **NBSP（U+00A0）而不是普通空格**
  （实测真实导出 249 个点位全是 `子翼的Apple\u00a0Watch`），多来源还会拼成一个串
  （`A|B`、`A | B`、`A |B` 三种写法并存）。唯一落点是 `SourceName.UnmarshalJSON` 里调
  `sample.CleanSource`——指标点/运动/睡眠/心电四类来源全走这个类型，一处清洗即全覆盖。
  口径：非 U+0020 空白收成普通空格、零宽字符（200B/200C/200D/2060/180E/FEFF）删掉、
  压缩连续空白、`|` 两侧统一成一个空格。**只做空白等价替换**——不折叠大小写、不删标点、
  不猜语义、不拆分多来源（设备名是人写的，聪明的改写真会把两台设备合并成一台）。
  不归一的后果不是「看着别扭」：sources 表会多一行肉眼一样的来源把样本数劈成两半，
  去重唯一索引也会因 source 不同把同一份数据入两遍。历史行由 `0007_source_normalize.sql`
  搬迁（撞唯一约束删旧留新）；**天级聚合不用手工改写**——Recompute 是先按 (日期×指标)
  整体 DELETE 再重建，只要把受影响日期丢进 `rollup_queue` 即可。`device_class` 不用重算
  （按子串判定，空白归一不改任何关键词）。SQL 与 Go 两份归一的等价性由
  `source_migration_test.go` 从迁移文件抠出函数 DDL 在真库上逐用例比对（函数用完即 DROP）。
- **仪表盘 focus 列表恒定占位**：概览页「关注的指标」即便当期无数据也要渲染成一张
  `— / 本期无数据` 的卡，不能从网格里 filter 掉——消失会让后面几张卡整体前移，看着像
  布局错位，也分不清「没测」还是「我们没接这个指标」。只有字典里都没登记的才不占位。
- 运动口径：`duration_min` 一律存分钟——HAE 老版 `duration` 给分钟、新版给**秒**且无单位标记，
  按「隐含时长更接近 `end-start`」判定（`workoutDurationMin`）；距离新版叫 `distance`、老版叫
  `totalDistance`，取有值者；能量统一 kcal、距离统一 km。

## 本地开发
```bash
docker run -d --name zhichang-tsdb -p 5433:5432 -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=zhichang timescale/timescaledb:2.30.2-pg18
export ZHICHANG_DB_DSN="postgres://postgres:postgres@127.0.0.1:5433/zhichang?sslmode=disable"
ZHICHANG_INGEST_TOKEN=test-token ZHICHANG_HTTP_ADDR=:8899 go run ./cmd/zhichangd
cd web && pnpm dev   # :5173，已代理 /api
go test ./...        # 集成测试靠 internal/testdb 自举 PG18，无需外部库
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

## 概览页常态口径（2026-10-15 落地）
- 新增只读接口 `/api/baseline`、`/api/insights`、`/api/coverage`（实现分别在
  store/postgres 的 baseline.go 与 insights.go），概览页重写为六区带。
- **常态 = 对比日之前 30 天窗口的中位数，不含对比日本身**；今日无数据回落到
  最后有数据日并置 `stale`，窗口跟回落日走；样本 <4 不给基准（宁缺毋假）。
- 偏离提示两条线：Tukey 1.5×IQR 离群（IQR=0 的平坦序列必须跳过，否则恒定值
  序列任何微小偏离都会误报）与连续 3 天同向偏离；幅度门槛 5%；只报 high/low 不判好坏。
- 涨跌色按 `web/src/lib/metric-direction.ts` 的方向表（up-good/down-good/neutral），
  不再沿用股票红涨绿跌；键的笔误由 `TestMetricDirectionKeysAreKnown` 锁定。
  该表与 metric-groups.ts 同为纯展示层配置，不进 API/库契约。
- focus/趋势图指标选择持久化在 localStorage（`zhichang.focus-metrics` /
  `zhichang.trend-metrics`），读档必须按字典过筛，全空回落默认，不能信存档。
- BaselineItem 的 value/baseline 为 null 是口径的一部分（可选标量），
  JSON 契约测试只锁数组字段（recent 必须 []）——别把标量 null 当违规。
