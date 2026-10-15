package pipeline

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/ingest/hae"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// 本文件补 Service.Ingest 的分支：去重模式差异、睡眠/运动落库、水位与重算队列，
// 以及每个阶段出错时错误怎么向上透传。主干端到端见 ingest_test.go（TestMain 也在那里）。

// onePoint 是「单个指标点」的最小负载，字段形态照抄 testdata/hae_export_v2.json，
// 手搓错字段名会让 Normalize 静默丢数据，测到的就不是入库分支了。
func onePoint(name, date string, qty float64, source string) string {
	return fmt.Sprintf(`{"data":{"metrics":[{"name":%q,"units":"count","data":[`+
		`{"date":%q,"qty":%g,"source":%q}]}]}}`, name, date, qty, source)
}

const (
	// 聚合睡眠：形态取自真实样例（totalSleep/asleep/core/deep/rem/sleepStart…），
	// 它落 sleep_sessions 而不是 samples。
	aggregateSleepPayload = `{"data":{"metrics":[{"name":"sleep_analysis","units":"hr","data":[{` +
		`"date":"2026-10-13","totalSleep":7.5,"asleep":7.0,"core":3.5,"deep":1.5,"rem":2.0,` +
		`"sleepStart":"2026-10-12 23:30:00 +0800","sleepEnd":"2026-10-13 07:00:00 +0800",` +
		`"inBed":8.0,"inBedStart":"2026-10-12 23:10:00 +0800","inBedEnd":"2026-10-13 07:10:00 +0800",` +
		`"source":"Apple Watch"}]}]}}`

	wrongValuePayload = `{"data":{"metrics":[{"name":"step_count","units":"count",` +
		`"data":[{"date":"2026-10-13 08:00:00 +0800","qty":999999,"source":"iPhone"}]}]}}`

	// 时长刻意大于 100 分钟，用来触发注入的 duration_min < 100 约束。
	// duration 按新版 HAE 的写法给秒数（与 end-start 一致），判据取隐含时长更接近起止差的那种解释。
	longWorkoutPayload = `{"data":{"workouts":[{"name":"Walking",` +
		`"start":"2026-10-13 08:00:00 +0800","end":"2026-10-13 11:00:00 +0800","duration":10800,` +
		`"totalEnergy":{"qty":120,"units":"kcal"},"totalDistance":{"qty":2.4,"units":"km"},` +
		`"source":"Apple Watch"}]}}`

	impossibleSleepPayload = `{"data":{"metrics":[{"name":"sleep_analysis","units":"hr","data":[{` +
		`"date":"2026-10-13","totalSleep":48,"sleepStart":"2026-10-12 23:30:00 +0800",` +
		`"source":"Apple Watch"}]}]}}`

	watchSourcePayload = `{"data":{"metrics":[{"name":"step_count","units":"count",` +
		`"data":[{"date":"2026-10-13 08:00:00 +0800","qty":10,"source":"Apple Watch"}]}]}}`

	multiSourcePayload = `{"data":{"metrics":[{"name":"step_count","units":"count","data":[` +
		`{"date":"2026-10-13 08:00:00 +0800","qty":10,"source":""}]}],` +
		`"workouts":[{"name":"Cycling","start":"2026-10-13 09:00:00 +0800",` +
		`"end":"2026-10-13 09:30:00 +0800","duration":30,` +
		`"totalEnergy":{"qty":200,"units":"kcal"},"totalDistance":{"qty":8,"units":"km"},` +
		`"source":"iPad"}]}}`
)

func mustParse(t *testing.T, raw string) *hae.Payload {
	t.Helper()
	p, err := hae.ParsePayload(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("负载解析失败: %v", err)
	}
	return p
}

// service 起一个跑在真库上的摄入服务，dedupMode 由调用方决定。
func service(t *testing.T, mode string) (*postgres.DB, *Service) {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	reg, _, err := db.LoadMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	home, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	return db, New(db, reg, home, mode)
}

func realPayload(t *testing.T) *hae.Payload {
	t.Helper()
	b, err := os.ReadFile("../../../testdata/hae_export_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	return mustParse(t, string(b))
}

// countRows 统计 `FROM <from>` 之后的行数；from 全是本文件里的字面量（表名或「表 WHERE …」）。
func countRows(t *testing.T, db *postgres.DB, from string) int {
	t.Helper()
	var n int
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM `+from).Scan(&n); err != nil {
		t.Fatalf("统计 %s 失败: %v", from, err)
	}
	return n
}

// TestNewDefaultsToStrict 锁 dedupMode 缺省回落：配置留空时必须是最安全的 strict，
// 而不是静默变成会覆盖历史值的 latest。
func TestNewDefaultsToStrict(t *testing.T) {
	home, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("缺少时区数据")
	}
	reg := metric.New(metric.Defaults())
	tests := []struct {
		in   string
		want string
	}{
		{"", "strict"},
		{"strict", "strict"},
		{"latest", "latest"},
		{"nonsense", "nonsense"}, // 原样透传：拼错的值会在 WriteSamples 落到 default 分支，不该被静默改写
	}
	for _, tt := range tests {
		s := New(nil, reg, home, tt.in)
		if s.mode != tt.want {
			t.Fatalf("mode(%q) 应为 %q，实际 %q", tt.in, tt.want, s.mode)
		}
		if s.metrics != reg || s.home != home {
			t.Fatal("构造器必须原样保存字典与时区")
		}
	}
}

// TestIngestPersistsWorkoutAndSleep 用真实样例锁 workout / sleep 两条落库路径的具体列值。
// 主干测试只看了计数，计数对了但值错位（比如时区、能耗单位）一样会让下游全错。
func TestIngestPersistsWorkoutAndSleep(t *testing.T) {
	db, svc := service(t, "strict")
	ctx := context.Background()

	out, err := svc.Ingest(ctx, realPayload(t), postgres.RunMeta{Transport: "rest", AutomationID: "auto-ws"})
	if err != nil {
		t.Fatal(err)
	}

	var (
		name, source, localDate, energyUnit, distUnit string
		startUTC, endUTC                              string
		duration, energy, distance                    float64
		runID                                         int64
	)
	if err := db.Pool.QueryRow(ctx,
		`SELECT name, source, local_date::text,
		        to_char(start_time AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS'),
		        to_char(end_time   AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS'),
		        duration_min, total_energy, energy_unit, total_distance, distance_unit, ingest_run_id
		 FROM workouts`).Scan(&name, &source, &localDate, &startUTC, &endUTC,
		&duration, &energy, &energyUnit, &distance, &distUnit, &runID); err != nil {
		t.Fatalf("运动应有 1 行: %v", err)
	}
	if name != "Walking" || source != "Apple Watch" || localDate != "2026-10-13" {
		t.Fatalf("运动身份列错误: %q/%q/%q", name, source, localDate)
	}
	// 08:00 +0800 必须存成 00:00 UTC：timestamptz 写错时区，日历与图表会整体偏移 8 小时。
	if startUTC != "2026-10-13 00:00:00" || endUTC != "2026-10-13 00:30:00" {
		t.Fatalf("运动起止时间应为 UTC 00:00/00:30，实际 %s~%s", startUTC, endUTC)
	}
	if duration != 30 || energy != 120 || distUnit != "km" || distance != 2.4 || energyUnit != "kcal" {
		t.Fatalf("运动量/单位落库错误: dur=%v energy=%v/%q dist=%v/%q", duration, energy, energyUnit, distance, distUnit)
	}
	if runID != out.RunID {
		t.Fatalf("运动的 ingest_run_id 应指向本次 run，实际 %d vs %d", runID, out.RunID)
	}

	var (
		sLocalDate, sSource            string
		sSleepStart, sSleepEnd         string
		sInBedStart, sInBedEnd         string
		total, asleep, core, deep, rem float64
		awake                          *float64
		inBed                          float64
		sTZ                            int
		sRunID                         int64
	)
	if err := db.Pool.QueryRow(ctx,
		`SELECT local_date::text, source,
		        to_char(sleep_start AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS'),
		        to_char(sleep_end   AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS'),
		        to_char(in_bed_start AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS'),
		        to_char(in_bed_end   AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS'),
		        total_sleep_hr, asleep_hr, core_hr, deep_hr, rem_hr, awake_hr, in_bed_hr,
		        tz_offset_min, ingest_run_id
		 FROM sleep_sessions`).Scan(&sLocalDate, &sSource, &sSleepStart, &sSleepEnd, &sInBedStart, &sInBedEnd,
		&total, &asleep, &core, &deep, &rem, &awake, &inBed, &sTZ, &sRunID); err != nil {
		t.Fatalf("睡眠应有 1 行: %v", err)
	}
	// 归属日按 sleepStart 判定：跨零点的那晚属于「入睡那天」，不是醒来那天。
	if sLocalDate != "2026-10-12" || sSource != "Apple Watch" || sTZ != 480 {
		t.Fatalf("睡眠归属/来源/偏移错误: %q/%q/%d", sLocalDate, sSource, sTZ)
	}
	if sSleepStart != "2026-10-12 15:30:00" || sSleepEnd != "2026-10-12 23:00:00" {
		t.Fatalf("睡眠起止应为 UTC 15:30/23:00，实际 %s~%s", sSleepStart, sSleepEnd)
	}
	if sInBedStart != "2026-10-12 15:10:00" || sInBedEnd != "2026-10-12 23:10:00" {
		t.Fatalf("在床起止应为 UTC 15:10/23:10，实际 %s~%s", sInBedStart, sInBedEnd)
	}
	if total != 7.5 || asleep != 7.0 || core != 3.5 || deep != 1.5 || rem != 2.0 || inBed != 8.0 {
		t.Fatalf("分期时长落库错误: total=%v asleep=%v core=%v deep=%v rem=%v inBed=%v",
			total, asleep, core, deep, rem, inBed)
	}
	// 样例里没有 awake 字段 → 必须留 NULL 而不是 0：0 会被读成「醒过 0 小时」，
	// 下游的清醒时长统计会凭空多出一段。
	if awake != nil {
		t.Fatalf("缺失的 awake 应为 NULL，实际 %v", *awake)
	}
	if sRunID != out.RunID {
		t.Fatalf("睡眠的 ingest_run_id 应指向本次 run，实际 %d vs %d", sRunID, out.RunID)
	}

	// 聚合睡眠不产生样本（只有分段形态才会），否则日聚合会把同一晚算两遍。
	if n := countRows(t, db, `samples WHERE metric = 'sleep_analysis'`); n != 0 {
		t.Fatalf("聚合睡眠不该产生 samples，实际 %d 条", n)
	}

	// 重推同一份数据（新 session）：运动与睡眠都要按各自唯一键去重，不能双计。
	again, err := svc.Ingest(ctx, realPayload(t), postgres.RunMeta{Transport: "rest", AutomationID: "auto-ws"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Workouts != 0 || again.Sleep != 0 {
		t.Fatalf("重复数据的运动/睡眠应全部命中去重，实际 workouts=%d sleep=%d", again.Workouts, again.Sleep)
	}
	if n := countRows(t, db, "workouts"); n != 1 {
		t.Fatalf("库里运动应仍是 1 条，实际 %d", n)
	}
	if n := countRows(t, db, "sleep_sessions"); n != 1 {
		t.Fatalf("库里睡眠应仍是 1 条，实际 %d", n)
	}
}

// TestDedupModeStrictKeepsCorrectedValue 锁 strict 的真实口径：
// 唯一键含 value，所以「同一时刻改了值」会并存两行（视为不同测量），
// 只有完全相同的点才被丢弃。这是宁可重复也不覆盖历史值的选择。
func TestDedupModeStrictKeepsCorrectedValue(t *testing.T) {
	db, svc := service(t, "strict")
	ctx := context.Background()
	meta := postgres.RunMeta{Transport: "rest"}

	first, err := svc.Ingest(ctx, mustParse(t, onePoint("step_count", "2026-10-13 08:00:00 +0800", 100, "iPhone")), meta)
	if err != nil {
		t.Fatal(err)
	}
	if first.Inserted != 1 || first.Updated != 0 || first.Duplicates != 0 {
		t.Fatalf("首次应 inserted=1/updated=0/duplicates=0，实际 %+v", first.IngestStats)
	}

	// 同一 (时刻, 指标, 来源) 换了值 → strict 下是第二条样本
	second, err := svc.Ingest(ctx, mustParse(t, onePoint("step_count", "2026-10-13 08:00:00 +0800", 150, "iPhone")), meta)
	if err != nil {
		t.Fatal(err)
	}
	if second.Inserted != 1 || second.Updated != 0 {
		t.Fatalf("strict 下修正值应作为新行插入，实际 inserted=%d updated=%d", second.Inserted, second.Updated)
	}
	if n := countRows(t, db, "samples"); n != 2 {
		t.Fatalf("strict 模式应保留两行，实际 %d", n)
	}

	// 完全相同的数据 → 一条都不进
	third, err := svc.Ingest(ctx, mustParse(t, onePoint("step_count", "2026-10-13 08:00:00 +0800", 100, "iPhone")), meta)
	if err != nil {
		t.Fatal(err)
	}
	if third.Inserted != 0 || third.Duplicates != 1 || third.Updated != 0 {
		t.Fatalf("重复点应只计 duplicates，实际 %+v", third.IngestStats)
	}
	if n := countRows(t, db, "samples"); n != 2 {
		t.Fatalf("重复推送不能加行，实际 %d", n)
	}
}

// latestIndex 是给 latest 模式临时补的唯一索引。
//
// 原因（已知缺陷，主代理已确认，不改生产代码）：0001 只在 samples 上建了
// (start_time, metric, source, value) 唯一索引，而 WriteSamples 的 latest 分支
// 按 (metric, source, start_time) 做 ON CONFLICT，现网 schema 下这条语句必然
// 报 42P10（no unique or exclusion constraint matching）。要在测试里验证 latest 的
// 覆盖语义，只能把那个索引临时建出来；测完删除，避免污染 strict 模式的用例
// （strict 的「同刻不同值并存两行」会被这个索引挡住）。
const latestIndexDDL = `CREATE UNIQUE INDEX IF NOT EXISTS ing_test_latest_uidx ON samples (metric, source, start_time)`
const latestIndexDrop = `DROP INDEX IF EXISTS ing_test_latest_uidx`

// TestDedupModeLatestOverwritesValue 锁 latest 与 strict 的实际差异：
// 同一 (指标, 来源, 时刻) 的新值覆盖旧行，库里永远只有最新那一条。
func TestDedupModeLatestOverwritesValue(t *testing.T) {
	db, svc := service(t, "latest")
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, latestIndexDDL); err != nil {
		t.Fatalf("临时建 latest 唯一索引失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), latestIndexDrop)
	})
	meta := postgres.RunMeta{Transport: "rest", AutomationID: "auto-latest"}

	first, err := svc.Ingest(ctx, mustParse(t, onePoint("step_count", "2026-10-13 08:00:00 +0800", 100, "iPhone")), meta)
	if err != nil {
		t.Fatalf("latest 首插失败: %v", err)
	}
	// latest 把 RowsAffected 同时当 inserted 与 updated 报（合并语句区分不开两种），
	// 这是它的真实口径：首插也会记 updated=1。
	if first.Inserted != 1 || first.Updated != 1 || first.Duplicates != 0 {
		t.Fatalf("latest 首插应为 inserted=1/updated=1，实际 %+v", first.IngestStats)
	}

	second, err := svc.Ingest(ctx, mustParse(t, onePoint("step_count", "2026-10-13 08:00:00 +0800", 150, "iPhone")), meta)
	if err != nil {
		t.Fatal(err)
	}
	if second.Inserted != 1 || second.Updated != 1 {
		t.Fatalf("latest 覆盖也应记 affected=1，实际 %+v", second.IngestStats)
	}
	var (
		n     int
		value float64
		rid   int64
	)
	if err := db.Pool.QueryRow(ctx, `SELECT count(*), max(value), max(ingest_run_id) FROM samples`).
		Scan(&n, &value, &rid); err != nil {
		t.Fatal(err)
	}
	// 与 strict 的分水岭：latest 覆盖后仍是 1 行，且值与归属 run 都换成了新的。
	if n != 1 {
		t.Fatalf("latest 应只保留 1 行，实际 %d", n)
	}
	if value != 150 {
		t.Fatalf("latest 应把值覆盖成 150，实际 %v", value)
	}
	if rid != second.RunID {
		t.Fatalf("覆盖后的样本应归属最新 run，实际 %d vs %d", rid, second.RunID)
	}

	// 值没变时 DO UPDATE 的 WHERE 不成立 → 一条 affected 都没有。
	third, err := svc.Ingest(ctx, mustParse(t, onePoint("step_count", "2026-10-13 08:00:00 +0800", 150, "iPhone")), meta)
	if err != nil {
		t.Fatal(err)
	}
	if third.Inserted != 0 || third.Updated != 0 || third.Duplicates != 1 {
		t.Fatalf("同值重推不该产生 affected，应只计 duplicates，实际 %+v", third.IngestStats)
	}
}

type wmRow struct {
	metric    string
	lastStart string
	lastDate  string
}

func readWatermarks(t *testing.T, db *postgres.DB, automationID string) []wmRow {
	t.Helper()
	rows, err := db.Pool.Query(context.Background(),
		`SELECT metric, to_char(last_start_time AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS'), last_local_date::text
		 FROM sync_watermark WHERE automation_id = $1 ORDER BY metric`, automationID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []wmRow
	for rows.Next() {
		var r wmRow
		if err := rows.Scan(&r.metric, &r.lastStart, &r.lastDate); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestWatermarkAndRollupQueue 锁摄入编排的两条「下游触发」：
// 水位按 automation × metric 记到最晚的那个点，重算队列按 (日期 × 指标) 入队。
func TestWatermarkAndRollupQueue(t *testing.T) {
	db, svc := service(t, "strict")
	ctx := context.Background()
	const auto = "auto-wm"

	out, err := svc.Ingest(ctx, realPayload(t), postgres.RunMeta{Transport: "rest", AutomationID: auto})
	if err != nil {
		t.Fatal(err)
	}
	if out.AffectedFrom != "2026-10-13" || out.AffectedTo != "2026-10-13" {
		t.Fatalf("样例只在 2026-10-13，实际 %q~%q", out.AffectedFrom, out.AffectedTo)
	}

	// 水位只来自 samples：聚合睡眠不落样本，所以它没有水位行（step/心率/体重/环境声 4 条）。
	want := []wmRow{
		{"body_mass", "2026-10-12 23:00:00", "2026-10-13"},
		{"environmental_audio_exposure", "2026-10-13 04:00:00", "2026-10-13"},
		{"heart_rate", "2026-10-13 01:15:00", "2026-10-13"},
		{"step_count", "2026-10-13 01:00:00", "2026-10-13"},
	}
	got := readWatermarks(t, db, auto)
	if len(got) != len(want) {
		t.Fatalf("水位行数应为 %d，实际 %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("水位第 %d 条不符: 期望 %+v 实际 %+v", i, want[i], got[i])
		}
	}

	// 重算队列：每个 (日期 × 指标) 一条，与字典里的 enabled 无关（未知指标也要重算）。
	// 聚合睡眠不产生 samples，但它自己就是「睡眠时长」天级值的唯一来源，
	// 所以按本夜（18:00 分界）排一条 sleep_analysis——不排的话趋势页会一直空着。
	var pairs string
	if err := db.Pool.QueryRow(ctx,
		`SELECT string_agg(local_date::text || ':' || metric, ',' ORDER BY metric) FROM rollup_queue`).
		Scan(&pairs); err != nil {
		t.Fatal(err)
	}
	const wantPairs = "2026-10-13:body_mass,2026-10-13:environmental_audio_exposure,2026-10-13:heart_rate,2026-10-12:sleep_analysis,2026-10-13:step_count"
	if pairs != wantPairs {
		t.Fatalf("rollup_queue 内容不符:\n 期望 %s\n 实际 %s", wantPairs, pairs)
	}

	// 补一次只含更早数据的手工导出：水位不能被旧数据拉回去（GREATEST 语义）。
	older := mustParse(t, onePoint("step_count", "2026-10-01 08:00:00 +0800", 500, "iPhone"))
	if _, err := svc.Ingest(ctx, older, postgres.RunMeta{Transport: "rest", AutomationID: auto}); err != nil {
		t.Fatal(err)
	}
	after := readWatermarks(t, db, auto)
	for _, r := range after {
		if r.metric == "step_count" && (r.lastStart != "2026-10-13 01:00:00" || r.lastDate != "2026-10-13") {
			t.Fatalf("旧数据把水位拉回了: %+v", r)
		}
	}
	// 但那一天确实需要重算，所以队列里新增一条（4 个样本指标 + 1 条睡眠 + 这个新日期 = 6）；
	// 同 (日期,指标) 再来一次不会重复入队。
	if n := countRows(t, db, "rollup_queue"); n != 6 {
		t.Fatalf("新增 2026-10-01 后队列应为 6 条，实际 %d", n)
	}
	if _, err := svc.Ingest(ctx, older, postgres.RunMeta{Transport: "rest", AutomationID: auto}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, "rollup_queue"); n != 6 {
		t.Fatalf("重算队列必须幂等，实际 %d 条", n)
	}
}

// TestNoAutomationIDSkipsWatermark 锁两个守卫分支：
// 没有 automation-id（手机手动导出）不写水位；只有聚合睡眠（没有任何样本）也不写。
// 这两种情况重算队列照常入队，否则手动导出后图表不会刷新——
// 聚合睡眠入的是自己那一行 sleep_analysis（它不产生样本，但它是睡眠时长天级值的唯一来源）。
func TestNoAutomationIDSkipsWatermark(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		automation string
		wantQueue  int
	}{
		{"没有 automation-id", onePoint("step_count", "2026-10-13 08:00:00 +0800", 100, "iPhone"), "", 1},
		{"只有聚合睡眠", aggregateSleepPayload, "auto-sleep-only", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, svc := service(t, "strict")
			ctx := context.Background()
			if _, err := svc.Ingest(ctx, mustParse(t, tt.raw),
				postgres.RunMeta{Transport: "rest", AutomationID: tt.automation}); err != nil {
				t.Fatal(err)
			}
			if n := countRows(t, db, "sync_watermark"); n != 0 {
				t.Fatalf("这两种形态都不该写水位，实际 %d 行", n)
			}
			if n := countRows(t, db, "rollup_queue"); n != tt.wantQueue {
				t.Fatalf("重算队列应有 %d 条，实际 %d", tt.wantQueue, n)
			}
		})
	}
	// 聚合睡眠那一支还得真的落一条睡眠，否则队列里那 1 条只是空转：
	// 数据在解析第一步就被丢掉，聚合也无从算起，界面上只会显示「暂无数据」。
	db, svc := service(t, "strict")
	if _, err := svc.Ingest(context.Background(), mustParse(t, aggregateSleepPayload),
		postgres.RunMeta{Transport: "rest", AutomationID: "auto-sleep-only"}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, "sleep_sessions"); n != 1 {
		t.Fatalf("聚合睡眠应落 1 行，实际 %d", n)
	}
}

// TestSourcesCollectedFromSamplesAndWorkouts 锁来源登记：
// 样本里 source 为空的点不能登记成一行空字符串，只在运动里出现的设备也要登记上。
func TestSourcesCollectedFromSamplesAndWorkouts(t *testing.T) {
	db, svc := service(t, "strict")
	ctx := context.Background()
	if _, err := svc.Ingest(ctx, mustParse(t, multiSourcePayload),
		postgres.RunMeta{Transport: "rest", AutomationID: "auto-src"}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Pool.Query(ctx, `SELECT source, device_class FROM sources ORDER BY source`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var s, c string
		if err := rows.Scan(&s, &c); err != nil {
			t.Fatal(err)
		}
		got[s] = c
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// 只有 iPad 一条：样本的 source 是空串，运动才带来真设备名。
	if len(got) != 1 || got["iPad"] != "phone" {
		t.Fatalf("来源登记应为 {iPad: phone}，实际 %v", got)
	}
	if _, ok := got[""]; ok {
		t.Fatal("空 source 不该被登记")
	}
	// 样本没来源，但照样入库（不能因为 source 缺失就丢数据）。
	var src string
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT source, count(*) FROM samples GROUP BY source`).Scan(&src, &n); err != nil {
		t.Fatal(err)
	}
	if src != "" || n != 1 {
		t.Fatalf("空 source 的样本应原样存 1 条，实际 %q / %d", src, n)
	}
}

// TestIngestErrorsPropagate 逐个阶段注入失败，锁两件事：
//  1. 错误一定向上透传（调用方拿到 err，且审计里 status='error' + 原因）；
//  2. 已经写成功的前置阶段不会被回滚——部分写入是这个编排的真实形态，
//     运维必须知道「失败的那次 run 里也可能已经有样本了」。
//
// 注入方式用 CHECK 约束而不是删表：贴近真实失败（约束/类型不符），
// 且收尾 DROP 即可恢复，不影响同库的其他用例。
func TestIngestErrorsPropagate(t *testing.T) {
	tests := []struct {
		name       string
		ddl        string
		constraint string
		raw        string
		automation string
		wantErr    string
		rows       map[string]int
		runStatus  string // "" 表示 run 根本没建起来
	}{
		{
			name:       "登记审计就失败",
			ddl:        `ALTER TABLE ingest_runs ADD CONSTRAINT ing_c_runs_transport CHECK (transport <> 'rest')`,
			constraint: "ing_c_runs_transport",
			raw:        onePoint("step_count", "2026-10-13 08:00:00 +0800", 100, "iPhone"),
			wantErr:    "violates check constraint \"ing_c_runs_transport\"",
			rows:       map[string]int{"ingest_runs": 0, "samples": 0},
		},
		{
			name:       "写样本失败",
			ddl:        `ALTER TABLE samples ADD CONSTRAINT ing_c_samples_value CHECK (value < 1000)`,
			constraint: "ing_c_samples_value",
			raw:        wrongValuePayload,
			wantErr:    "合并进 samples 失败",
			rows:       map[string]int{"samples": 0, "ingest_runs": 1},
			runStatus:  "error",
		},
		{
			name:       "写运动失败",
			ddl:        `ALTER TABLE workouts ADD CONSTRAINT ing_c_workouts_dur CHECK (duration_min < 100)`,
			constraint: "ing_c_workouts_dur",
			raw:        longWorkoutPayload,
			wantErr:    "violates check constraint \"ing_c_workouts_dur\"",
			// 样本阶段已经过了：这条 run 的失败点之后没有写入，但审计必须存在。
			rows:      map[string]int{"workouts": 0, "ingest_runs": 1},
			runStatus: "error",
		},
		{
			name:       "写睡眠失败",
			ddl:        `ALTER TABLE sleep_sessions ADD CONSTRAINT ing_c_sleep_hr CHECK (total_sleep_hr < 24)`,
			constraint: "ing_c_sleep_hr",
			raw:        impossibleSleepPayload,
			wantErr:    "violates check constraint \"ing_c_sleep_hr\"",
			rows:       map[string]int{"sleep_sessions": 0, "ingest_runs": 1},
			runStatus:  "error",
		},
		{
			name:       "登记来源失败",
			ddl:        `ALTER TABLE sources ADD CONSTRAINT ing_c_sources_cls CHECK (device_class <> 'watch')`,
			constraint: "ing_c_sources_cls",
			raw:        watchSourcePayload,
			wantErr:    "violates check constraint \"ing_c_sources_cls\"",
			// 样本与来源登记是先后两段：样本留下了，来源一行都没有。
			rows:      map[string]int{"samples": 1, "sources": 0, "ingest_runs": 1},
			runStatus: "error",
		},
		{
			name:       "写水位失败",
			ddl:        `ALTER TABLE sync_watermark ADD CONSTRAINT ing_c_wmark_auto CHECK (automation_id <> 'auto-bad')`,
			constraint: "ing_c_wmark_auto",
			raw:        onePoint("step_count", "2026-10-13 08:00:00 +0800", 100, "iPhone"),
			automation: "auto-bad",
			wantErr:    "violates check constraint \"ing_c_wmark_auto\"",
			rows:       map[string]int{"samples": 1, "sources": 1, "sync_watermark": 0, "rollup_queue": 0, "ingest_runs": 1},
			runStatus:  "error",
		},
		{
			name:       "入队重算失败",
			ddl:        `ALTER TABLE rollup_queue ADD CONSTRAINT ing_c_rollup_metric CHECK (metric <> 'step_count')`,
			constraint: "ing_c_rollup_metric",
			raw:        onePoint("step_count", "2026-10-13 08:00:00 +0800", 100, "iPhone"),
			wantErr:    "violates check constraint \"ing_c_rollup_metric\"",
			// 队列排在最后：数据、来源都已经在库里。
			rows:      map[string]int{"samples": 1, "sources": 1, "ingest_runs": 1},
			runStatus: "error",
		},
		{
			name:       "收尾统计失败但数据已入库",
			ddl:        `ALTER TABLE ingest_runs ADD CONSTRAINT ing_c_runs_status CHECK (status <> 'ok')`,
			constraint: "ing_c_runs_status",
			raw:        onePoint("step_count", "2026-10-13 08:00:00 +0800", 100, "iPhone"),
			wantErr:    "violates check constraint \"ing_c_runs_status\"",
			// FinishRun 是最后一步：数据在库里，run 却停在 running（不是 error，
			// 因为写 error 状态用的还是同一条 UPDATE，一样会被约束挡掉）。
			rows:      map[string]int{"samples": 1, "ingest_runs": 1},
			runStatus: "running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, svc := service(t, "strict")
			ctx := context.Background()
			if _, err := db.Pool.Exec(ctx, tt.ddl); err != nil {
				t.Fatalf("注入约束失败: %v", err)
			}
			t.Cleanup(func() {
				_, _ = db.Pool.Exec(context.Background(),
					fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s", constraintTable[tt.constraint], tt.constraint))
			})

			out, err := svc.Ingest(ctx, mustParse(t, tt.raw),
				postgres.RunMeta{Transport: "rest", AutomationID: tt.automation})
			if err == nil {
				t.Fatalf("失败应当向上透传，实际 out=%+v", out)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("错误应说明根因 %q，实际 %v", tt.wantErr, err)
			}
			for table, want := range tt.rows {
				if got := countRows(t, db, table); got != want {
					t.Fatalf("%s 应有 %d 行，实际 %d", table, want, got)
				}
			}
			if tt.runStatus == "" {
				// BeginRun 就失败：没有 run，也就没有 Outcome。
				if out != nil {
					t.Fatalf("登记失败时应返回 nil Outcome，实际 %+v", out)
				}
				return
			}
			if out == nil {
				t.Fatal("进入 run 之后失败也应返回带 RunID 的 Outcome，供调用方审计")
			}
			if out.RunID == 0 {
				t.Fatal("Outcome 必须带上这次失败的 run id")
			}
			var status, errMsg string
			if err := db.Pool.QueryRow(ctx,
				`SELECT status, COALESCE(error,'') FROM ingest_runs WHERE id = $1`, out.RunID).
				Scan(&status, &errMsg); err != nil {
				t.Fatal(err)
			}
			if status != tt.runStatus {
				t.Fatalf("审计状态应为 %q，实际 %q", tt.runStatus, status)
			}
			if tt.runStatus == "error" && errMsg == "" {
				t.Fatal("失败的 run 必须记下原因，否则只能看到「手机推过了但库里没有」")
			}
			if tt.runStatus == "error" && !strings.Contains(errMsg, tt.wantErr) {
				t.Fatalf("审计里的原因应与返回给调用方的错误一致: %q", errMsg)
			}
		})
	}
}

// constraintTable 给出每个注入约束所在的表，供收尾 DROP 用。
var constraintTable = map[string]string{
	"ing_c_runs_transport": "ingest_runs",
	"ing_c_runs_status":    "ingest_runs",
	"ing_c_samples_value":  "samples",
	"ing_c_workouts_dur":   "workouts",
	"ing_c_sleep_hr":       "sleep_sessions",
	"ing_c_sources_cls":    "sources",
	"ing_c_wmark_auto":     "sync_watermark",
	"ing_c_rollup_metric":  "rollup_queue",
}

// TestDuplicateRunStatsLoadFailure 锁重复分支里的失败：
// 同一 session 重推时要读回上次的统计，这一步失败必须直接报错（而不是当成首次重摄入，
// 那会把已经落库的数据再算一遍）。
func TestDuplicateRunStatsLoadFailure(t *testing.T) {
	db, svc := service(t, "strict")
	ctx := context.Background()
	meta := postgres.RunMeta{Transport: "rest", SessionID: "dup-load-fail"}

	if _, err := svc.Ingest(ctx, mustParse(t, onePoint("step_count", "2026-10-13 08:00:00 +0800", 100, "iPhone")), meta); err != nil {
		t.Fatal(err)
	}
	// 把统计列藏起来：BeginRun 的冲突判定不碰它，LoadRunStats 的 SELECT 必然失败。
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE ingest_runs RENAME COLUMN sample_count TO ing_hidden_sample_count`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `ALTER TABLE ingest_runs RENAME COLUMN ing_hidden_sample_count TO sample_count`)
	})

	out, err := svc.Ingest(ctx, mustParse(t, onePoint("step_count", "2026-10-13 08:00:00 +0800", 100, "iPhone")), meta)
	if err == nil {
		t.Fatalf("读不回上次统计时应失败，实际 out=%+v", out)
	}
	if out != nil {
		t.Fatal("回放分支失败时没有可返回的统计，应返回 nil")
	}
	// 老数据不能被再写一遍，run 也只有原来那一条。
	if n := countRows(t, db, "samples"); n != 1 {
		t.Fatalf("重复分支失败不应补写样本，实际 %d 条", n)
	}
	if n := countRows(t, db, "ingest_runs"); n != 1 {
		t.Fatalf("不该新开 run，实际 %d 条", n)
	}
}

// TestIngestStatsShape 把 Outcome 的组成口径钉住：
// received 是原始点数（含不产生样本的聚合睡眠那一条），inserted 才是落库样本数，
// duplicates 由 len(samples)-inserted 推出来，unknown/skipped 各自独立。
func TestIngestStatsShape(t *testing.T) {
	db, svc := service(t, "strict")
	ctx := context.Background()

	out, err := svc.Ingest(ctx, realPayload(t), postgres.RunMeta{Transport: "rest"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Received != 10 {
		t.Fatalf("received 应为原始 10 条（9 个点 + 1 条运动），实际 %d", out.Received)
	}
	if out.Inserted != 8 || out.Duplicates != 0 {
		t.Fatalf("首插应为 inserted=8/duplicates=0，实际 %d/%d", out.Inserted, out.Duplicates)
	}
	if out.Skipped != 0 {
		t.Fatalf("干净数据不该有 skipped，实际 %d", out.Skipped)
	}
	// 样例里已没有字典外的指标（环境音量已登记）。「未知指标照常入库」这条口径
	// 改由 TestIngestKeepsUnregisteredMetric 用构造出来的假名字覆盖。
	if len(out.UnknownMetrics) != 0 {
		t.Fatalf("样例里不该还有未登记指标，实际 %v", out.UnknownMetrics)
	}
	// 字典外的指标不参与天级聚合，但绝不能被静默丢掉；这里顺带确认它在库里。
	var n int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM samples WHERE metric = 'environmental_audio_exposure'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("environmental_audio_exposure 应入库，实际 %d 条", n)
	}

	// 脏数据：缺 qty 的点与解析不了时间的点都进 skipped，一条都不落库。
	dirty := `{"data":{"metrics":[{"name":"step_count","units":"count","data":[` +
		`{"date":"2026-10-13 08:00:00 +0800","source":"iPhone"},` +
		`{"date":"完全不是时间","qty":10,"source":"iPhone"}]}]}}`
	res := hae.Normalize(mustParse(t, dirty), metric.New(metric.Defaults()), mustLoc(t))
	if res.Skipped != 2 || len(res.Samples) != 0 {
		t.Fatalf("脏数据应全部 skip，实际 skipped=%d samples=%d", res.Skipped, len(res.Samples))
	}
	// Normalize 判脏之后，编排层没有样本可写：received 仍是原始 2 条。
	dirtyOut, err := svc.Ingest(ctx, mustParse(t, dirty), postgres.RunMeta{Transport: "rest"})
	if err != nil {
		t.Fatal(err)
	}
	if dirtyOut.Received != 2 || dirtyOut.Inserted != 0 || dirtyOut.Skipped != 2 {
		t.Fatalf("脏数据计数错误: %+v", dirtyOut.IngestStats)
	}
	if dirtyOut.AffectedFrom != "" || dirtyOut.AffectedTo != "" {
		t.Fatalf("没有样本时 affected 必须留空，实际 %q~%q", dirtyOut.AffectedFrom, dirtyOut.AffectedTo)
	}
	// 全脏的 run 仍然要留下审计，否则手机侧「推过了但库里没东西」无从查起。
	var status string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM ingest_runs ORDER BY id DESC LIMIT 1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "ok" {
		t.Fatalf("脏数据不是失败，run 状态应为 ok，实际 %q", status)
	}
}

// TestIngestKeepsUnregisteredMetric：字典外的指标照常入库并回传，
// 只是不参与天级聚合（字典只约束聚合口径，不该静默丢数据）。
//
// 指标名刻意用构造出来的假名，而不是「当下恰好未登记」的真实指标：
// 后者一旦被登记进字典，这条测试就会静默地不再测任何东西
// （environmental_audio_exposure 就经历过这个过程）。
func TestIngestKeepsUnregisteredMetric(t *testing.T) {
	db, svc := service(t, "strict")
	ctx := context.Background()

	out, err := svc.Ingest(ctx,
		mustParse(t, onePoint("zzz_probe_unregistered", "2026-10-13 08:00:00 +0800", 7, "iPhone")),
		postgres.RunMeta{Transport: "rest"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.UnknownMetrics) != 1 || out.UnknownMetrics[0] != "zzz_probe_unregistered" {
		t.Fatalf("未登记指标必须回传，实际 %v", out.UnknownMetrics)
	}
	if out.Inserted != 1 {
		t.Fatalf("未登记指标也要入库，实际 inserted=%d", out.Inserted)
	}
	var n int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM samples WHERE metric = 'zzz_probe_unregistered'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("未登记指标应落 samples，实际 %d 条", n)
	}
	// 也要进重算队列：聚合侧对未登记指标用 sum 兜底口径，
	// 不排进队列的话它永远只有原始样本、界面上拿不到天级值。
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM rollup_queue WHERE metric = 'zzz_probe_unregistered'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("未登记指标也要排进聚合队列，实际 %d 条", n)
	}
}

func mustLoc(t *testing.T) *time.Location {
	t.Helper()
	l, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("缺少时区数据")
	}
	return l
}
