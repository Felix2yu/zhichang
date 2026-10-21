# UI 控件与交互模式收敛评估

> **实施进度（2026-10-22）**：P0 已全部落地，P1 已完成 5/6 页，均在同一个工作区未提交。
> 组件落在 `web/src/lib/components/ui/`（基础控件 + 状态反馈 + 布局容器）与
> `web/src/lib/components/DataTable.svelte`（业务组件层外壳）。
>
> | 项 | 状态 |
> | --- | --- |
> | HelpToggle / ErrorBanner / PageHeader / Card / CardHeader / InfoNote | ✅ 已抽取并全部落位 |
> | Chip | ✅ 概览、趋势、MetricPicker（含 dashed 未接入态） |
> | Button | ✅ settings 六钮（含 busy / 两段式危险确认）、sleep 夜晚步进 |
> | StatTile | ✅ sleep 4 张、workouts 3 张 |
> | DataTable 外壳 | ✅ workouts / sync×2 / sleep / ecg（expand 片段保留 reveal） |
> | EmptyState / LoadingState | ✅ trends；其余页的加载占位仍内联（见下） |
> | 三处裸 `<tr onclick>` 键盘不可达 | ✅ DataTable 统一 tabindex + Enter/Space + 行内按钮豁免 |
> | 概览 `<select>` 换 MetricPicker | ⬜ 未做（功能改动，需单独过一眼视觉） |
> | sleep 图卡的 6 处加载占位换 LoadingState | ⬜ 未做（逐处带业务文案，留待顺手） |
> | P2 口径整治（进度条、format.ts、透明度、行选中色） | ⬜ 未动，按原计划需先做一次视觉确认 |
>
> 验证方式：`pnpm check` + `pnpm lint` + 39 个单测 + `pnpm build` 全绿；
> 另用 Playwright 对 7 个页面做了 mock 数据的浏览器冒烟（含运动页键盘展开、
> 心电页自动选中/收起/主色底），全部通过。

范围：`web/src/routes` 下 7 个页面、`web/src/lib/components` 下 25 个组件、`web/src/app.css`。
方法：逐文件通读 + 类名/文案频次统计。文中所有行号为 2026-10-22 工作区状态（HEAD `bca55fd`）。

## 0. 结论摘要

| 档位 | 数量 | 代表项 |
| --- | --- | --- |
| A 可直接统一 | 7 | Card/CardHeader、ErrorBanner、PageHeader、Chip、StatTile、InfoNote、HelpToggle |
| B 参数配置后统一 | 7 | DataTable 外壳、EmptyState、LoadingState、Meter/Gauge、fmt、Segmented 扩展、行选中口径 |
| C 保持独立实现 | 10 | Modal/Toast/Pagination、MetricCard vs MetricTrendCard、TrendChart vs MetricTrendCard、Ecg*/Sleep* 系列、settings 表单行、概览 vs 睡眠的结构条 |

两条最需要说在前面的判断：

1. **不要引入 Modal / Toast / Pagination。** 全项目零处需求。详情展示刻意用了三种非弹窗方案（表格行内展开、底部追加面板、URL 化的聚焦视图），因为它们要让链接可分享 —— `trends/+page.svelte:255-264` 的 `?v=focus&f=<metric>`、`sleep/+page.svelte:334-353` 的 `?date=`。引入弹窗会破坏「URL 是这一屏的真相」这条顶层约定。
2. **`MetricCard` 与 `MetricTrendCard` 不该合并**，理由见 §3.3；它们不是同一控件的两个变体，而是「一句话结论」与「区间内完整形态」两种语义。

## 1. 控件与模式盘点

### 1.1 页面标题条 —— 7/7 页

`<h1 class="text-[15px] font-medium">` 出现在全部 7 个页面（`+page.svelte:347`、`trends:527`、`sleep:540`、`workouts:75`、`ecg:124`、`sync:30`、`settings:231`），字号口径完全一致，说明这条视觉约定已经自发成立。

差异只在左右布局与右侧内容：

| 页面 | 外层 | 右侧内容 |
| --- | --- | --- |
| 概览 `:346` | `flex items-baseline justify-between`（无 `flex-wrap`） | 今日日期 / 加载态文本 |
| 趋势 `:526` | `flex flex-wrap items-baseline justify-between gap-2` | `Segmented`（视图）+ `RangePicker` |
| 睡眠 `:539`、运动 `:74`、心电 `:123` | 同上 | `RangePicker` |
| 数据源 `:30` | 仅 `h1`，无同行容器 | 无 |
| 设置 `:231` | `mb-1` 的 `h1` + 独立副说明行 `:232` | 无 |

### 1.2 区间选择器 RangePicker —— 4 页引用，2 套刷新策略

引用点：`sleep:43/541`、`workouts:5/76`、`ecg:5/125`、`trends:27/540`。
组件本身（`RangePicker.svelte`）已经是共享实现，且内部复用了 `Segmented`（`:86`）。

真正的分裂在调用侧：

- 睡眠 `:541`、运动 `:76`、心电 `:125`：`onchange={(r) => ((range = r), load())}` —— 立即重载。
- 趋势 `:444-447`：`onRangeChange` → `scheduleLoad()` → 200ms 防抖（`trends:419-427`），因为一次可点多个指标，逐次全量重拉会打满接口。
- 概览：完全不使用 RangePicker，只读 `prefs.rangeDays`（`+page.svelte:52` 的 `TREND_DAYS`）。
- 数据源页：无区间概念。

这是**数据加载策略差异**，不是控件差异。RangePicker 不应吸收它，最多加一个可选的 `debounceMs`。

### 1.3 分段选择器 Segmented —— 6 处使用，1 个组件，3 类取值

`components/Segmented.svelte` 是泛型组件（`generics="T extends string | number"`），已被复用在：

- 设置页 4 处：外观 `:245`（`'light'|'dark'|'system'`）、默认区间 `:361`（`7|30|90|365`）、时长格式 `:378`（`DurationFormat`）、导出天数 `:407`（数字）。
- 趋势页 1 处：视图切换 `:529`（`'grid'|'list'`）。
- `RangePicker:86` 内部：预设 + `CUSTOM_KEY = -1` 哨兵（`RangePicker.svelte:37`）。
- `EcgTrendChart.svelte:5` 内部：指标切换。

组件本身无需改造，只有一处体感不一致：趋势工具条上 Segmented 与 RangePicker 并排时偏紧，缺 `size` 参数。

### 1.4 错误横幅 —— 6 页 7 处 + 组件 2 处

完全同构的 6 处（逐字符相同）：

```
class="card px-4 py-3 text-[13px] text-[var(--color-danger)]"
```

`sync:33`、`sleep:545`、`workouts:80`、`ecg:129`、`trends:545`、`settings:237`。

偏离的 5 处：

| 位置 | 差异 |
| --- | --- |
| `+page.svelte:354` | `card px-4 py-2.5 text-[12px]`，且渲染的是 `errors[]` 数组而非单个字符串 |
| `sleep:901` | 分期错误 **无 card 容器**，纯文本 `text-[12px]` |
| `ecg:287` | 波形错误 **无 card**，在展开行内 `py-2` |
| `settings:352` | 操作反馈 msg **无 card**，11px |
| `components/WorkoutDetail.svelte:162` | `border-t` + `px-4 py-3 text-[13px]`，位于面板内 |

判断：6 处已一致，剩下 5 处并非设计分歧，而是「这段错误要不要自带卡片背景」没有约定。可以一次性抽出 `ErrorBanner`，由 `bare` 参数控制是否带 card。

### 1.5 加载态 —— 5 种实现共存

| 形态 | 位置 | 说明 |
| --- | --- | --- |
| 纯文本「加载中…」 | 15 处 | 概览 `:349/492/513/587`、睡眠 `:676/753/812/853/906/984`、设置 `:452`、数据源 `:59`、趋势 `:562`、运动 `:174` |
| `animate-pulse` 骨架 | 4 处 | `MetricCard:63`（h-22）、`MetricTrendCard:410`（h-16）、`:516`（h-110）、`sleep:552`（h-32） |
| 占位区块（撑图表高度） | 7 处 | 睡眠 `:673/750/808/850/903/981`、趋势 `:559`，形式 `flex h-[Npx] items-center justify-center`，N ∈ {160, 200, 220, 240, 260} |
| `invisible` 遮罩 | 1 处 | `MetricTrendCard:512`，图容器隐藏后叠骨架 |
| 业务专属文案 | 5 处 | 概览 `:349`（加载时显示"加载中…"、否则显示日期）、`FreshnessBar:55`「正在读取同步状态…」、`WorkoutDetail:170`「读取轨迹与心率…」、`ecg:133`「正在分析心电数据…」、`ecg:284`「波形加载中…」 |

关键取舍：后 5 处的文案**不能**统一成「加载中…」。「波形加载中…」和「正在分析心电数据…」回答的是"系统在做什么"，把它们抹平会丢信息。可抽 `LoadingState`，把文案作为必填 `text` 参数。

### 1.6 空状态 —— 14 处，3 种视觉容器 / 4 档信息密度

含「暂无 / 尚未 / 没有记录 / 无记录」的文案分布在 6 页 + 5 组件（计数：sleep 5、trends 3、MetricTrendCard 4、sync 2、ecg 2、workouts 1、MetricPicker 1、TrendListView 1、SleepScheduleChart 1、EcgIndexCard 1）。

三种容器：

1. **表格内 colspan 行**：`sync:114`（colspan=5）、`sync:154`（8）、`workouts:173`（8）、`ecg:316`（5），统一 `px-4 py-6 text-center text-[var(--color-ink-500)]`。
2. **图区占位**：高度口径分裂 —— `trends:560` h-160、`sleep:756/815/909` py-10、`sleep:679/682` h-220。
3. **解释型面板**：`MetricTrendCard:473-506` 的三分支（`never` / `pending` / `stale`），是全项目最成熟的空态，每条都给了下一步动作或"无需处理"的结论。

第 3 类的价值正在于它的**文案是数据语义**，不能压成通用组件内部的分支。正确做法：`EmptyState` 只提供容器与 `title/desc/action`，三分支判断留在 `trends/+page.svelte:127-128` 的 `emptyKind` 计算处。

### 1.7 表格 —— 5 个 `<table>`，表头口径统一、行语义不统一

`sync`（2 张：数据源 `:85`、同步记录 `:127`）、`sleep:1025`、`workouts:122`、`ecg:222`，外加 `EcgComparisonCard:78` 的紧凑变体。

表头完全一致的部分：

```
thead class="bg-[var(--color-surface-2)] text-[12px] text-[var(--color-ink-500)]"
th    class="px-4 py-2 text-left|right font-normal"
tr    class="border-t border-[var(--color-line)]"
```

差异：

| 维度 | sync | workouts | sleep | ecg | EcgComparisonCard |
| --- | --- | --- | --- | --- | --- |
| 行交互 | 无 | 点击选中 + `title` | 点击切夜晚 + `title` | 点击展开/收起 + 行内详情 | 无 |
| hover | 无 | `hover:bg-surface-2` | `hover:bg-surface-2` | `hover:bg-surface-2` | 无 |
| 选中色 | — | `surface-3` (`:142`) | `surface-3` (`:1045`) | `brand-soft/60` (`:237`) | — |
| 单元格 | `px-4 py-2` | 同 | 同 | 同 | `py-1.5`，12px |
| 空态 | colspan 行 | colspan 行 | 无（`#each` 无 `{:else}`） | colspan 行（筛选态） | 整块 `py-8` |
| 横向溢出 | 无 | 无 | `overflow-x-auto` + `min-w-[700px]` (`:1024`) | 无 | 无 |

结论：抽 `DataTable` **只做外壳**（thead/tbody/空态/行点击与选中色），行内容用 slot 交给页面。不要试图吸收 ecg 的展开行 —— 那个 `<tr>` 紧跟一个 `colspan` 详情行、并挂了 `use:reveal` 滚动 action（`ecg:93-101, 259-313`），是页面专属能力。

### 1.8 多选胶囊 chip —— 6 处，4 种尺寸 / 3 种未选中态

```
概览:385    rounded-full border px-2.5 py-1     text-[12px]   未选中 text-ink-700
趋势:609    rounded-full border px-2.5 py-1     text-[12px]   未选中 text-ink-700   ← 与上行逐字符相同
MetricPicker:140/153/162  px-2 py-0.5  text-[11px]  未选中 text-ink-500
MetricPicker:215          px-2 py-0.5  text-[12px]  未选中 text-ink-700 / 未接入 border-dashed + text-ink-400
```

选中态 6 处全部一致：`border-transparent bg-[var(--color-brand-soft)] text-[var(--color-brand)]`。视觉语言已经自发统一，纯粹是缺一个 `<Chip>` 基础件。

### 1.9 按钮 —— 无 Button 组件，5 种手写口径

| 口径 | 代表位置 |
| --- | --- |
| 次级描边 + brand 文字 | `settings:288/310/417` — `rounded-md border border-[var(--color-line)] px-2.5 py-1 text-[11px] text-[var(--color-brand)] disabled:opacity-40` |
| 次级描边 + ink-700 文字 | `sleep:876/882` — `rounded border border-[var(--color-line)] px-2 py-0.5 text-[11px] text-[var(--color-ink-700)]` |
| 幽灵文字 | `settings:294/316` — `rounded-md px-2 py-1 text-[11px] text-[var(--color-ink-500)] hover:bg-[var(--color-surface-3)]` |
| 链接式 | 概览 `:364/431/444/500`、多处 `text-[12px] text-[var(--color-brand)] hover:underline` |
| 两段式危险确认 | `settings:427-434`（`resetArmed`：「恢复默认」→「确认恢复默认？」） |

`disabled:opacity-40` 出现在 5 处，但**没有任何一处有 `aria-busy`**，也没有统一的「忙碌时把按钮文案换成『保存中…』」约定（settings 里是手写的 `busy === '睡眠时长目标' ? '保存中…' : '保存'`）。

### 1.10 单选控件 —— 3 处需求，3 种实现

- `<select>`：唯一一处，概览 `:541-549`（趋势卡片指标切换）。
- `Segmented`：设置页全部偏好项、趋势页视图。`RangePicker:37` 甚至用 `CUSTOM_KEY = -1` 哨兵把「自定义」塞进 Segmented。
- 手写 chip 组：睡眠 `:917-932`（时间线叠加指标，`phyActive` 用 `class:` 指令逐个切边框/背景），以及 `EcgTrendChart`（Segmented）。

判断：这三种里 `<select>` 是遗留写法 —— 概览那两个下拉选的是 51 个指标，而项目已有更好的 `MetricPicker`。这是"该换实现"而不是"该抽组件"。

### 1.11 弹窗 / 抽屉 / Toast / 分页 —— 全部 0 处

`grep '<dialog|fixed inset|role="dialog"|Toast|toast'` 在全部 `.svelte` 中无结果。

「详情」的三种替代实现：

1. **表格行内展开**：`ecg:259-313`。`<tr>` 后紧跟 `colspan` 详情行，配 `use:reveal`（`ecg:93-101`）在波形异步加载完后把展开块滚进视野。
2. **页面底部追加面板**：`WorkoutDetail.svelte`，由 `workouts:182-184` 渲染，自带「收起」按钮（`:145-148`）。
3. **URL 化的聚焦视图**：`trends:255-264, 572-619`，`view === 'focus'` + `?v=focus&f=<metric>`，可分享、可刷新还原。

「分页」的三种替代（都是限量，不是翻页）：

- `trends:40` `GRID_MAX = 12`，超出部分在 `:624-635` 给出「切到列表看全部」。
- `sleep:122` `SCHEDULE_MAX_NIGHTS = 90`，`:860` 提示「另有 N 晚未画」。
- 概览 `:167-168` 硬编码近 7 日 / 前 7 日对比。

### 1.12 口径脚注 —— 19 处

`text-[10px]/[11px] leading-relaxed text-[var(--color-ink-500)]/80`：

- 设置页 6 处完全同构：`:260/324/347/395/442/468`。
- 睡眠页 5 处：`:569(10px)/738(10px)/800(10px)/839(10px)/857(10px)`，另 `:911` 用纯 `ink-500`。
- 心电页 1 处 `:326`，`EcgIndexCard:92/103` 用 `ink-700`（说明面板正文）。

### 1.13 「?」说明开关 —— 2 处逐字符重复

`EcgIndexCard.svelte:32-39` 与 `EcgVerdictCard.svelte:45-52`，连 `aria-label="查看指标说明"` 都相同，展开后也都是 `{#if open}` 的一段 11px 正文。这是本项目最容易摘的果子。

### 1.14 进度/量规类 —— 4 处，2 个物种

| 位置 | 形态 | 轨道底色 | 高度 |
| --- | --- | --- | --- |
| `GoalProgress:57-62` | 条 + width% | `bg-line` | h-1.5 |
| `sleep:587-594`（评分三项） | 条 + width% | `bg-line/60` | h-1.5 |
| `sleep:786-796`（结构比） | 条 + 绝对定位参考线 | `bg-line/60` | h-1.5 |
| `概览:461-465`（昨晚分期堆叠） | 堆叠 segmentation | `bg-line` | **h-2** |
| `EcgIndexCard:66-79` | 渐变轨道 + 圆点游标 | gradient | h-1.5 |

前四条是 `ProgressBar`（比例条），第五条是 `Gauge`（量表）。两者不宜合成。

### 1.15 数值格式化 —— 4 份重复实现

| 实现 | 位置 | 逻辑 |
| --- | --- | --- |
| `fmt(name, v)` | `TrendChart:55-60` | ≥1000 且 digits=0 时千分位，否则 toFixed，拼单位 |
| `fmt(v)` | `MetricTrendCard:109-113` | 同上，不拼单位 |
| `fmt(v, digits)` | `TrendListView:92-96` | 同上 |
| `formatValue(v, unit, digits)` | `api.ts`（被 `MetricCard:66`、`GoalProgress:32`、`概览:601` 用） | null → `—` |

四份逻辑等价，且都对 `Number.isFinite` 做了防御。抽到 `#lib/format.ts` 是零风险项。

### 1.16 统计小卡 —— 7 处，结构逐字一致

```
<div class="card px-4 py-3">
  <div class="text-[12px] text-[var(--color-ink-500)]">{label}</div>
  <div class="num mt-1 text-[20px] font-medium">{value}</div>
</div>
```

睡眠 `:616-663`（4 张：平均评分 / 平均总睡眠 / 平均入睡潜伏期 / 记录天数）、运动 `:84-102`（3 张：次数 / 总时长 / 总消耗）。
唯一差异在第三行注脚：睡眠用 `text-[10px] text-[var(--color-ink-500)]/70`，有断档时切 `text-[var(--color-warn)]`（`:650-652`）；运动用 `mt-0.5 text-[11px]`。

## 2. 三档分类结论

### A. 可直接统一（7 项）

判断依据：**视觉串逐字符相同 + 交互语义单一 + 无业务逻辑**。

| # | 建议组件 | 重复处的当前位置 | 依据 |
| --- | --- | --- | --- |
| A1 | `ErrorBanner` | `sync:33`、`sleep:545`、`workouts:80`、`ecg:129`、`trends:545`、`settings:237` | 6 处 `card px-4 py-3 text-[13px] text-danger` 逐字符相同；`bare` 参数吸收另外 5 处无边框变体 |
| A2 | `PageHeader` | 7 页全部 `<h1 class="text-[15px] font-medium">` | 布局容器 + 右侧 slot，不动数据流 |
| A3 | `Chip` | `+page.svelte:385`、`trends:609`、`MetricPicker:140/153/162/215` | 选中态 6 处完全一致；未选中态差异收敛为 `variant` |
| A4 | `StatTile` | `sleep:616-663`（4）、`workouts:84-102`（3） | 结构逐字一致，仅第三行注脚可变 |
| A5 | `Card` / `CardHeader` | `card p-4` 16 处（sleep 9 / ecg 3 / sync 2 / settings 1 / workouts 1）；`text-[13px] font-medium` 标题 22 处 | 最高频重复；`CardHeader` 的 `action` 槽吸收 5 处右上角链接（概览 `:431/444/500`、睡眠 `:1003`、设置多处） |
| A6 | `InfoNote` | `settings:260/324/347/395/442/468`、`sleep:569/738/800/839/857`、`ecg:326` | 纯文本 + 密度参数，无逻辑 |
| A7 | `HelpToggle` | `EcgIndexCard:32-39`、`EcgVerdictCard:45-52` | 逐字符重复，连 aria-label 都一样 |

### B. 参数配置后统一（7 项）

判断依据：**交互语义相同，但数据结构 / 视觉密度 / 加载策略存在真实差异**，需要 `variant` / `density` / `slot` 承载。

| # | 组件 | 必须暴露的参数 | 为什么不能一次性做强统一 |
| --- | --- | --- | --- |
| B1 | `DataTable` | `columns`（含 align/width）、`rowKey`、`selectedKey?`、`onrowclick?`、`density: 'normal'｜'compact'`、`empty?`、`expandable?` | ecg 表格带 `use:reveal` 的行内展开，sync 表格纯只读，EcgComparisonCard 是 `py-1.5 / 12px` 紧凑变体。只统一外壳（thead 口径、border-t、`hover:bg-surface-2`、空态 colspan 行），行内容永远走 slot |
| B2 | `EmptyState` | `variant: 'row'｜'block'｜'panel'`、`colspan?`、`title`、`desc?`、`action?`、`href?` | 三容器的信息密度不同步：表格内只能给一句；图区要给高度撑住布局；`MetricTrendCard:473-506` 的 never/pending/stale 三分支必须留在调用方，压进组件会把 `trends:127-128` 的数据语义搬进 UI 层 |
| B3 | `LoadingState` | `text`（必填）、`height?`、`skeleton?` | 「波形加载中…」「正在分析心电数据…」是有效信息，文案必须可由调用方指定；4 个骨架位置的高度各不相同 |
| B4 | `ProgressBar` / `Gauge` | 前者：`value/max/height/track/tick?`；后者：`value/level/color/higherIsBetter` | 分两个物种，不要合成。`EcgIndexCard:66-79` 的渐变轨道 + 圆点游标是量表不是进度条；两者行为语义不同（进度有终点、量表无终点） |
| B5 | `num()` in `#lib/format.ts` | `digits`、千分位阈值 | 替换 `TrendChart:55`、`MetricTrendCard:109`、`TrendListView:92`；`api.ts` 的 `formatValue` 因被类型层共用，暂不动，改为内部调用 `num()` |
| B6 | `Segmented` 扩展 | `size: 'sm'｜'md'`、`disabled?: Set<T>` | 现有 API 已经很好（泛型 + `onchange`），只补趋势工具条的紧凑尺寸；不要动 `value/onchange` 形状 |
| B7 | 行选中色口径 | 统一到 `--color-brand-soft` 或 `--color-surface-3` 二者之一 | 现状：sleep `:1045`、workouts `:142` 用 `surface-3`；ecg `:237` 用 `brand-soft/60`。ecg 的语义是"这一行对应正在看的波形"，比普通选中更强，可用 `emphasis` 参数保留；但若追求一致性，统一到 `brand-soft` 更符合已有视觉语言 —— 这需要一次视觉确认，故列 B 档而非 A 档 |

### C. 应保持独立实现（10 项）

| # | 项 | 保持独立的理由 |
| --- | --- | --- |
| C1 | Modal / Drawer / Toast | 零需求。现有三种替代方案（行内展开 / 底部面板 / URL 聚焦视图）服务于「链接可分享」这条顶层约定（`trends:255-264`、`sleep:334-353`）。引入弹窗 = 引入不可分享状态 |
| C2 | Pagination | 零需求。三处"限量"是三条互不相同的业务规则（`GRID_MAX=12` 图表实例上限 / `SCHEDULE_MAX_NIGHTS=90` 可读性上限 / 概览近 7 日语义），且不提供翻页入口。没有翻页需求的通用分页组件是空转成本 |
| C3 | `MetricCard` vs `MetricTrendCard` | 语义不同：`MetricCard.svelte` 是「今天 vs 我的常态」的一句话结论（`baseline` / `deviationPct` / `recent` / `hint`，12 个 props）；`MetricTrendCard.svelte` 是「区间内这个指标的完整形态」（tier 分档 + ECharts time 轴 + 密度条 + 回溯开关 + 三分支空态，20 个 props）。合并后 props 会涨到 25+，且两处在 nuances 上互斥（sparkline vs ECharts、常态单调 vs 区间分档）。**可只抽两者共有的「最新值 + ▲▼delta」为一个 3 行小件**，卡本体不合并 |
| C4 | `TrendChart` vs `MetricTrendCard` 的图表部分 | 这不是重复而是**口径分歧**。`TrendChart.svelte:93` 用 `type: 'category'` 直接铺后端点；`MetricTrendCard.svelte:26-28` 的注释明确指出 category 轴是旧版缺陷——「1 月、3 月、8 月各测一次」会被画成相邻三个点，抹平"中间两个月没测"这个最关键的事实。两者是新旧两代。**不应合并；正确动作是让 sleep 页 6 处 TrendChart 迁到 time 轴**，那是一条独立的重构线，不是组件抽象线 |
| C5 | `Ecg*` 六件套 | `EcgVerdictCard` / `EcgIndexCard` / `EcgComparisonCard` / `EcgDistributionChart` / `EcgTrendChart` / `EcgStripChart` 各只在 ecg 页使用一次，每张承载一套独立的心电语义（众数判定、0-100 量规、周期对比、分类分布、原始波形）。抽象会把 `ecg-levels.ts` 的知识泄漏到通用层。现有粒度（独立文件、页面内组装）是对的 |
| C6 | `Sleep*` 三件套 | `SleepTimeline` / `SleepScheduleChart` / `SleepRhythmChart` 同理，且绑定 `sleep-stages.ts` / `sleep-rhythm.ts` 的领域模型 |
| C7 | sleep 页夜晚步进器 | `stepNight`（`:409-416`）、`selectNight`（`:402-406`）、`?date=`（`readUrl:327-328`）三者耦合，是这一屏独有的主键模型。无第二处消费者 |
| C8 | 概览 vs 睡眠 的分期/结构条 | 概览 `:461-475` 是"昨晚三期占比"（100% 铺满、无参考线）；睡眠 `:786-796` 是"范围内平均结构 + 典型值参考线 + 0–60% 公共尺子"（`STRUCTURE_SCALE_MAX`，`:461`）。前者答"昨晚睡得怎么样"，后者答"我的结构偏离典型多少"。尺度与单位都不同，合并会让任一方失真 |
| C9 | 设置页表单行 | `:275-299`（睡眠时长）与 `:301-321`（预期入睡）表面同构，但只有 2 个实例，且各自带不同的校验（`saveDuration:120-124` 数值区间 vs `saveBedtime:137-140` HH:MM 正则）、不同的令牌与只读提示语义。抽 FormRow 的收益低于一层间接的成本。**除非加到第 4 行，否则保持内联** |
| C10 | `TrendListView` 的表头排序 | 只此一处，且依赖该文件内的 `SortKey` 联合类型（`:47`）。提取会引入向上依赖 |

## 3. 现有实现中的不一致点

### 3.1 命名混乱：同一个词表示四种东西

| 名字 | 出现处 | 实际含义 |
| --- | --- | --- |
| `open` | `MetricPicker:38` / `workouts:14` / `EcgIndexCard:25` / `EcgVerdictCard:37` | 折叠态 / 选中行 / 说明面板 / 说明面板 |
| `selected` | `trends:49` / `ecg:33` / `MetricPicker:26` | 指标 id **数组** / 单条记录 / 选中集合 |
| `loading` | 全项目 ~12 处 / `settings:45` `busy` / `sleep:263` `stageLoading` / `ecg:23` `waveLoading` / `settings:49` `exportBusy` | 五种命名表达同一件事 |
| `error` | 全项目 / `+page.svelte:68` `errors[]` / `settings:46` `msg` / `settings:50` `exportMsg` / `sleep:264` `stageError` / `ecg:24` `waveError` | 六种命名，两种容器（字符串 vs 数组） |

建议采取渐进策略：新组件执行严格命名（`selectedMetrics` / `selectedRecord` / `helpOpen` / `pickerOpen` / `isBusy`），旧代码不动（改名会制造无意义 diff）。

### 3.2 回调命名：「选中」有三种叫法

- `onchange`：`RangePicker:23`、`Segmented:6`、`MetricPicker:28`（值变更）
- `onselect`：`MetricTrendCard:66`、`TrendListView:42`、`SleepScheduleChart`（选中某项）
- `onclear` / `onbackfill` / `onbackfilloff` / `onclose` / `onfilter`：`MetricPicker:29`、`MetricTrendCard:68/70`、`WorkoutDetail:11`、`EcgDistributionChart`

建议：`onchange` 专用于"值变更"，`onselect` 专用于"选中某一项"，其余语义用 `on<动词>`。

### 3.3 交互行为不一致

| 行为 | 现状 | 问题 |
| --- | --- | --- |
| 卡片/行可点击 | `MetricTrendCard:379-397` 用 `div + role=button + tabindex + onkeydown`（`handleCardKey` 支持 Enter/Space）；`TrendListView:142` 用 `<button>`；`sleep:1043` / `workouts:140` / `ecg:234` 用裸 `<tr onclick>` | 后三处**键盘完全不可达**，也无 `role`/`aria`。| 在 DataTable 落位后统一处理方式 |
| 嵌套按钮事件 | `MetricTrendCard:365-370` 手写 `runInner` + `e.stopPropagation()`，`handleCardClick` 里再判断 `t?.closest('button, a')` | 这是 `<tr>` 做点击目标时的通用坑，应该由 `DataTable` 的 `onrowclick` 统一处理，而不是每个页面各写一遍 |
| 破坏性操作确认 | 仅 `settings:427-434` 的 `resetArmed` 两段式；清除目标（`settings:293/314`）单击即删 | 前者是因为"重置本机偏好"覆盖面大，后者因为按钮只在有值时出现。差异合理，但缺少书面的口径约定 |
| 忙碌态反馈 | `settings:290/312` 手写 `busy === '睡眠时长目标' ? '保存中…' : '保存'` | 每加一个按钮复制一遍；应进 `Button` 的 `busy` 参数 |

### 3.4 样式口径不一致

| 项 | 现状 | 建议 |
| --- | --- | --- |
| 次级文字透明度后缀 | `/80`（13 处）、`/70`（5 处）、`/60`、`/50`、`/25` 混用于**同样的次级说明** | 收敛到两档：正文级 `text-[var(--color-ink-500)]`、弱化级 `/70`。`/50`、`/60`、`/25` 只留给 hover 背景与底色，不用于文字 |
| 进度条轨道 | `bg-[var(--color-line)]`（`GoalProgress:56`、概览 `:461`）vs `/60`（`sleep:587/786`） | 统一到 `--color-line`；`/60` 是想"更淡"，但在斜暗两端表现不一致 |
| 进度条高度 | h-1.5（5 处）vs **h-2**（概览 `:461`） | 统一 h-1.5，除非是页级主视觉 |
| 谁负责 `card` | `EcgVerdictCard.svelte:40` / `MetricTrendCard.svelte:390` / `MetricCard.svelte:52` 自带 card；而 `EcgTrendChart` / `EcgDistributionChart` / `EcgComparisonCard` 不自带，由 `ecg/+page.svelte:191/199/186` 外包 `<div class="card p-4">` | 无约定。建议**新组件一律不自带 card**，容器由页面决定（这让组件能被塞进任何容器）。旧组件不动，避免一次性大改 25 个文件 |
| 语义色叠加透明度 | `bg-[var(--color-warn)]/10`（`ecg:306`）、`/15`（`MetricTrendCard:436/575`、`sleep:651` 用 warn 文字）、`{color}1a`（`sleep:564/767`）、`{color}1f`（`EcgIndexCard:50`） | 四种方法做同一件事（浅色底 + 深色字）。建议统一 `{color}1f`，暗黑模式下由 theme token 翻转已可覆盖 |
| 表单控件默认样式 | `app.css:79-88` 显式接管 input/select/textarea 的 background 与 color | 这条已经很好（注释说明了 Safari 白底胶囊问题），冒号后应保持，不要因为抽组件而拆散 |

## 4. 抽象层次建议

自上而下四层，依赖方向严格单向：

```
业务页 routes/*/+page.svelte
        ↓
业务组件层  MetricCard / MetricTrendCard / TrendListView / MetricPicker
           FreshnessBar / GoalProgress / DensityStrip / Ecg* / Sleep*
           DataTable  ← 本层最上沿，只做外壳
        ↓
状态反馈层  ErrorBanner / EmptyState / LoadingState / InfoNote
        ↓
基础控件层  Button / Chip / Select / ProgressBar / Gauge / HelpToggle
           Segmented / RangePicker（已有）
        ↓
布局容器层  Card / CardHeader / PageHeader / Field
```

三条约束：

1. **基础控件层不认识业务概念。** 它可以有 `Chip{variant}`，但不能有 `MetricChip{metric}`。
2. **业务组件层可以自定义空态文案**，不允许把数据语义（如 `emptyKind` 的三分支判断）下沉到 `EmptyState` 内部。
3. **布局容器层零逻辑**，`Card` 只负责 `background/ border / radius`（沿用 `app.css:61-65` 的 `.card`），`CardHeader` 只负责标题 + 右侧 action 槽。

## 5. 收敛建议：优先级、涉及页面、风险与顺序

### P0 —— 零风险提纯（预计 1 个 PR，~15 个文件）

抽取：`Card` / `CardHeader` / `ErrorBanner` / `PageHeader` / `InfoNote` / `HelpToggle`。

- 涉及页面：全部 7 页 + `EcgIndexCard` / `EcgVerdictCard` / `MetricCard` / `MetricTrendCard` / `TrendListView` / `MetricPicker` / `WorkoutDetail`。
- 风险：**视觉微调级**。这六个组件不含任何状态或异步，纯替换 DOM 结构。最坏结果是某处间距差 1px。
- 排序建议：`HelpToggle`（仅 2 处）→ `ErrorBanner`（11 处）→ `PageHeader`（7 页）→ `Card`/`CardHeader`（38 处，最大面）→ `InfoNote`（19 处）。
- 副作用收益：`ErrorBanner` 支持 `string[]` 后，概览 `:353-355` 的多错误渲染与其余页的单错误渲染可以共用同一入口，顺带把 `sleep:901`、`ecg:287`、`settings:352` 三处"裸文本错误"拉回统一口径。

### P1 —— 低风险替换（按页分批，每页一个 PR）

抽取：`Chip` / `Button` / `StatTile` / `DataTable` 外壳 / `EmptyState` / `LoadingState`。

页面推进顺序（由易到难）：

1. **运动页**（`workouts/+page.svelte`，185 行）：3 张 StatTile + 1 张表格 + WorkoutDetail。表格只有 hover + 选中 + 点击，是 DataTable 的最优首例。
2. **数据源页**（`sync/+page.svelte`，160 行）：2 张纯只读表格，无交互，用来验证 DataTable 的 `onrowclick` 缺省时的行为。
3. **概览页**（`routes/+page.svelte`，618 行）：chip 组（`:385`）+ StatTile 候选（"近 7 日运动"卡 `:497-530`）+ 错误横幅数组。**顺手把 `:541` 的 `<select>` 换成 `MetricPicker` 或至少换成 `Select` 组件** —— 51 个指标用原生下拉是不合适的。
4. **趋势页**（`trends/+page.svelte`，683 行）：chip 组（`:609`）+ 三档空态。注意这里 `EmptyState` 必须接收来自 `cardOf():127-128` 的 `emptyKind`，不要把判断挪进组件。
5. **睡眠页**（`sleep/+page.svelte`，1067 行）：最大的一处。4 张 StatTile + 1 张表格 + 6 处图区加载/空态占位。切表格时必须保 `selectNight` → `?date=` 的 URL 回写（`sleep:334-353`）不被 `stopPropagation` 影响。
6. **心电页**（`ecg/+page.svelte`，339 行）：最后一个，因为它的表格带行内展开 + `use:reveal`。DataTable **必须**保留 `expandable` 逃生舱，不要试图把 reveal action 收进组件。

风险与缓解：`web/tests` 下只有 5 个纯逻辑测试（`metric-frequency` / `metric-render` / `metric-search` / `metric-tiers` / `trend-buckets`），**没有任何组件或 E2E 测试**。因此 P1 的回归验证只能靠 `pnpm check`（svelte-check）+ `pnpm lint` + 逐页手点。这正是必须按页小步推进而非一次性大重构的原因。

### P2 —— 中风险口径整治（单独 PR，先决策后动手）

1. `ProgressBar` / `Gauge` 抽取与高度/轨道色统一（涉及 `GoalProgress`、`sleep:461/587/786`、`EcgIndexCard:66-79`）。
2. `#lib/format.ts` 的 `num()`，替换 `TrendChart:55`、`MetricTrendCard:109`、`TrendListView:92`。
3. 行选中色口径决策（`surface-3` vs `brand-soft`）→ 二选一后进 `DataTable` 的 `selectedKey` 实现。
4. 透明度后缀收敛到两档。
5. 把 `MetricTrendCard:365-383` 的嵌套点击处理挪进 `DataTable.onrowclick`，让 sleep/workouts/ecg 三处 `<tr onclick>` 获得键盘可达性。

每一项都触及"看起来一样、其实不同"的灰色地带，需要一次视觉确认再动。

### P3 —— 明确不做（并记录理由）

| 不做的事 | 记下的理由 |
| --- | --- |
| 引入 Modal / Drawer / Toast | 破坏 URL 可分享约定，零需求 |
| 引入 Pagination | 三处限量是三条业务规则，无翻页需求 |
| 合并 `MetricCard` + `MetricTrendCard` | 语义不同（今日结论 vs 区间形态），见 §2-C3 |
| 合并 `TrendChart` + `MetricTrendCard` | 口径分歧（category vs time 轴），见 §2-C4 |
| 抽 settings 的 FormRow | 仅 2 实例 + 各自校验，收益 < 成本 |
| 统一概览与睡眠的结构条 | 尺度与语义不同（100% 铺满 vs 0–60% 公共尺 + 典型值参考线） |

## 6. 可以立刻记下的两条工程约定

1. **「数据是摆完之后，容器由主人决定」**：新写的组件不自带 `card`，容器和留白一律由页面给。这样组件才能被塞进网格、表格单元、卡片组而不用改 props。
2. **「每一次「这类出差的三种可能」都要写出来」**：`MetricTrendCard:473-506` 的 never/pending/stale 三分支是本项目空态的标准答案。后续任何新页面的空态都应进逐同一口径 —— 说清成因（从未接入 / 今天还没同步 / 区间内没有但历史有），而不是一句"无记录"。

## 附：频次统计速查

| 模式 | 出现次数 | 文件数 |
| --- | --- | --- |
| `<h1 class="text-[15px] font-medium">` | 7 | 7 |
| `card p-4` | 16 | 6 |
| `text-[13px] font-medium`（区块标题） | 22 | 7 |
| 错误横幅（含变体） | 11 | 8 |
| 「加载中…」文本 | 15 | 7 |
| `animate-pulse` 骨架 | 4 | 3 |
| 占位区块（`flex h-[Npx] items-center justify-center`） | 7 | 2 |
| 空态文案（暂无/尚未/无记录…） | 14 | 11 |
| `<table>` | 5（+1 紧凑变体） | 5 |
| 表格 `colspan` 空态行 | 4 | 3 |
| 统计小卡（20px num） | 7 | 2 |
| 胶囊 chip | 6 | 3 |
| 隐藏/普通按钮（各类手写 `<button>`） | 20+ | 9 |
| `<select>` | 1 | 1 |
| 口径脚注 | 19 | 3 |
| 「?」说明开关 | 2 | 2 |
| 进度/量表条 | 6 | 4 |
| 数值 `fmt` 实现 | 4 份 | 4 |
| Modal / Dialog / Toast / Pagination | 0 | 0 |
