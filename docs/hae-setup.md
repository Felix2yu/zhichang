# Health Auto Export 接入指南

本指南给出与知常配套的完整配置。**主通道用 REST API**；MQTT 仅作旁路，原因见文末。

## 1. 创建自动化

1. 打开 Health Auto Export → 侧边栏 **自动化** → **新建自动化** → 类型选 **REST API**
2. 名称：`知常同步`

### URL

```
https://<你的域名>/api/ingest/hae
```

### HTTP 标头

| 键 | 值 |
|---|---|
| `X-Zhichang-Token` | `ZHICHANG_INGEST_TOKEN` 的值 |

（也支持 `Authorization: Bearer <token>` 形式。）

请求超时建议 30 秒以上。

## 2. 数据类型设置

- **数据类型**：勾选 **健康指标**（需要运动数据则再勾 **锻炼**）
- **选择健康指标**：只勾你真正要看的。**空指标也会拖慢 App**，别全选
- **首选来源**：把 Apple Watch 排在 iPhone 之前（手表采样更密）

建议起步集：步数、活动能量、基础能量、静息心率、心率、心率变异性、血氧、睡眠分析、体重、步行+跑步距离、爬楼层数。

### 采集面自查：字典有、上游没发

指标字典是全集，不代表你的自动化在发。一年真实导出对照字典的结果：**上游出现 40 类，字典登记 51 条**，差额里有一批是「字典里有卡片、但自动化从没发过」——它们在趋势页就是一个永远空的 chip，看着像应用没记录，实际是采集端没开。

| 指标 | 要它有数据需要什么 |
|---|---|
| 血糖 `blood_glucose` | HAE 勾选血糖；通常需要 CGM/血糖仪写入健康 App |
| 体温 `body_temperature`、睡眠腕温 | 腕温由 watchOS 自动记录；体温需 HAE 勾选对应类型 |
| 体重 `body_mass`、体脂率、BMI、去脂体重、身高 | 体脂秤或手动记录；只勾体重的话其余四项会一直空 |
| 用力肺活量、最大呼气流速 | 需肺活量计类 App 写入健康 App |
| 洗手次数、心理状态 | HAE 勾选「类别」型数据（这两项是分类记录，不是数值） |
| 跑步速度、跑步功率、性生活次数 | 已在字典里；跑步两项只在含该数据的运动后出现，量极少属正常 |

自查命令（不依赖界面）：

```bash
zhichangd doctor                       # 总览样本数与最近同步
psql "$ZHICHANG_DB_DSN" -c "
  SELECT m.metric, m.display_name_zh,
         (SELECT count(*) FROM samples s WHERE s.metric = m.metric)          AS 样本,
         (SELECT count(*) FROM daily_metrics_day d WHERE d.metric = m.metric) AS 天级
    FROM metric_defs m
   WHERE (SELECT count(*) FROM samples s WHERE s.metric = m.metric) = 0
     AND (SELECT count(*) FROM daily_metrics_day d WHERE d.metric = m.metric) = 0
   ORDER BY 1;"
```

两列都要看：`sleep_analysis` 天生没有样本（会话只进 `sleep_sessions`），只看样本列会把它误报成空指标。

查出来是空的指标有两种可能：HAE 没勾，或你根本没有这个数据源。前者去自动化里补勾，后者放着就行——**别为了让卡片不空而造数据**。

## 3. 导出设置

| 项 | 推荐值 | 原因 |
|---|---|---|
| 导出格式 | **JSON** | 结构化、支持批量请求 |
| 导出版本 | **v2** | 当前格式，锻炼数据更完整 |
| 日期范围 | **自上次同步** | 增量最小；补历史数据用手动导出 |
| 汇总数据 | **开启** | 非聚合的秒/分钟级数据会显著拖慢 App、放大负载 |
| 时间分组 | **小时** | 累计量按小时分桶；更细粒度会撞上 iOS 后台 30 秒限制 |
| 批量请求 | **开启** | 大 payload 拆成多个请求，避免超时/413 |

## 4. 同步频率与 iOS 的现实约束

App 能否按时跑，取决于 iOS，而不是知常。以下情况自动化**不会**运行：

- iPhone 处于锁定状态（Apple 不允许访问加密的健康数据）
- 关闭了后台 App 刷新、低电量模式、长时间未使用设备、系统资源紧张

另外：后台任务通常只有约 30 秒窗口，配置过于激进（全量指标 + 秒级分组）会被系统终止。

提高成功率：

- 建议频率 **15～60 分钟**
- 充电时同步更频繁（iOS 限制更少）
- 用 iPhone 镜像时设备视为已解锁，更容易成功
- 把自动化小组件放到主屏幕，有助于后台执行

## 5. 手动补数

- 在自动化页面点 **手动导出**，选择日期范围（如过去 7 天）即可补历史数据
- 也可导出为文件后用同一端点导入：`POST /api/ingest/hae`（同一 JSON 结构）
- 重复导入是安全的：知常按 `(start_time, metric, source, value)` 去重，重复数据会被丢弃并计入 `duplicates`
- **整年导出可能超单次上限**：实测含心电图波形的一年导出约 650MB，而 `ZHICHANG_MAX_BODY_BYTES` 默认 512MB，整包 POST 会被 413 拒。按段分开送（指标 / 运动 / 心电各一批，心电再按 30~40 条切），或临时调大上限。摄入端点把整个请求体读进内存再解析，实测 85MB 负载 → 进程约 640MB 常驻，调大上限前先看机器有多少内存
- **运动轨迹不在同步范围内**：HAE 的 REST 只发 JSON，逐点经纬度落在导出目录的 `.gpx` 文件里，用本机命令导入（`zhichangd import-routes <目录>`，可加 `-dry-run` 看匹配报告）。位置数据不走网络通道，理由与口径见 README 的「运动轨迹（GPX）」。导入后在**运动页点开任意一次运动**即可看轨迹：等比例折线按速度着色、标起终点，**不接地图瓦片**（瓦片要按经纬度向第三方拉取，等于把行踪发出去）

## 6. 验证

```bash
curl -s -X POST https://<你的域名>/api/ingest/hae \
  -H "X-Zhichang-Token: <token>" \
  -H "Content-Type: application/json" \
  --data-binary @export.json
```

正常响应：

```json
{
  "run_id": 42,
  "status": "ok",
  "received": 120,
  "inserted": 118,
  "duplicates": 2,
  "workouts": 1,
  "sleep": 1,
  "affected": { "from": "2026-10-13", "to": "2026-10-13" }
}
```

- `status: duplicate` 表示该 `session-id` 已处理过（幂等）
- `unknown_metrics` 列出尚未登记到指标字典的指标（仍会入库，可在 `metric_defs` 表补定义）

排错入口：自动化 → **查看活动日志**，可看到每次运行的 HTTP 状态码与错误原因（如 “No health data was found in this date range”）。

## 7. 为什么不用 MQTT

| 维度 | REST API | MQTT |
|---|---|---|
| 实时性 | 由 App 自动化调度决定 | **完全等价**（同一调度器，只是出口不同） |
| 部署 | 复用现有 HTTPS 端点，零新组件 | 需额外 broker、端口、鉴权、TLS |
| 可靠性 | HTTP 状态码、活动日志、批量、可重试 | **QoS 0、不保留、无重放**，丢消息不可知 |
| 元数据 | 自动带 `automation-id / session-id / period`，可做幂等与水位 | 只有 topic |

结论：MQTT 的长连接不带来额外实时性（数据不是从设备实时流出，而是 App 被系统调度后才查询 HealthKit 并外发），却引入静默丢数据的风险。若要接 Home Assistant 等 IoT，可另行开启知常的 MQTT 旁路作为附加通道，但不要让它成为唯一通道。

## 8. 可选：MQTT 旁路

```bash
ZHICHANG_MQTT_ENABLED=true
ZHICHANG_MQTT_BROKER=tcp://192.168.1.10:1883
ZHICHANG_MQTT_TOPIC=zhichang/hae/#
```

在 HAE 中另建一个 MQTT 自动化，topic 与上面一致。MQTT 没有 `session-id`，幂等退化为「payload sha256 去重」。
