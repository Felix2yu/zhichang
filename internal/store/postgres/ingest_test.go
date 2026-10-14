package postgres

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// 本文件锁 ingest.go 的写入口径：去重模式到底差在哪、幂等键是什么、
// 审计行与水位是否真的落了库。

// openIngestDB 复用测试库并清空摄入相关的表。
func openIngestDB(t *testing.T) *DB {
	t.Helper()
	db := openRollupDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `TRUNCATE ingest_runs, workouts, sleep_sessions, ecg_records, sources, sync_watermark`); err != nil {
		t.Fatalf("清理摄入表失败: %v", err)
	}
	return db
}

func lookupRun(t *testing.T, db *DB, id int64) map[string]any {
	t.Helper()
	ctx := context.Background()
	var transport, automationID, automationName, sessionID, period, aggregation, sha, archived, status string
	var bytes int64
	err := db.Pool.QueryRow(ctx, `SELECT transport, COALESCE(automation_id,''), COALESCE(automation_name,''),
		COALESCE(session_id,''), COALESCE(period,''), COALESCE(aggregation,''), COALESCE(payload_sha256,''),
		COALESCE(archived_path,''), status, payload_bytes
		FROM ingest_runs WHERE id = $1`, id).
		Scan(&transport, &automationID, &automationName, &sessionID, &period, &aggregation, &sha, &archived, &status, &bytes)
	if err != nil {
		t.Fatalf("读取 run 失败: %v", err)
	}
	return map[string]any{
		"transport": transport, "automation_id": automationID, "automation_name": automationName,
		"session_id": sessionID, "period": period, "aggregation": aggregation,
		"payload_sha256": sha, "archived_path": archived, "status": status, "payload_bytes": bytes,
	}
}

// TestBeginRunWritesFullMetaAndIsSessionIdempotent 锁定摄入审计：
//   - RunMeta 的每个字段都必须真的落库（排障时只看得到落库的字段）；
//   - 新 run 的 status 是 running，由 FinishRun 收尾；
//   - 同一 session_id 重复提交不新增行，而是回既有 run + duplicate=true（HAE 重试不该攒垃圾）。
func TestBeginRunWritesFullMetaAndIsSessionIdempotent(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	meta := RunMeta{
		Transport: "webhook", AutomationID: "automation-daily", AutomationName: "每日同步",
		SessionID: "sess-0001", Period: "daily", Aggregation: "total",
		PayloadSHA256: "ab12cd34", PayloadBytes: 987654, ArchivedPath: "archive/run-9f.json",
	}
	id, dup, err := db.BeginRun(ctx, meta)
	if err != nil {
		t.Fatalf("BeginRun 失败: %v", err)
	}
	if dup {
		t.Fatal("首次提交不该被判成重复")
	}
	if id <= 0 {
		t.Fatalf("应拿到自增 id，实际 %d", id)
	}
	got := lookupRun(t, db, id)
	want := map[string]any{
		"transport": "webhook", "automation_id": "automation-daily", "automation_name": "每日同步",
		"session_id": "sess-0001", "period": "daily", "aggregation": "total",
		"payload_sha256": "ab12cd34", "archived_path": "archive/run-9f.json", "payload_bytes": int64(987654),
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("字段 %s 未落库或值不对：期望 %v，实际 %v", k, v, got[k])
		}
	}
	if got["status"] != "running" {
		t.Fatalf("新 run 的 status 应是 running（收尾才变 ok），实际 %v", got["status"])
	}

	// 同 session 重放：拿到同一个 id，且表里只有一行
	id2, dup2, err := db.BeginRun(ctx, meta)
	if err != nil {
		t.Fatalf("重放 BeginRun 失败: %v", err)
	}
	if !dup2 || id2 != id {
		t.Fatalf("重放应回既有 run（id=%d dup=%v），实际 id=%d dup=%v", id, dup2, id2, dup2)
	}
	var cnt int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM ingest_runs`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("重放不该新增行，实际 %d 行", cnt)
	}

	// session_id 缺省时是 NULL：部分唯一索引不约束 NULL，所以每次都是新 run，
	// 且各空字段落成 NULL（nullable），不能写成空串混进统计。
	noSession := RunMeta{Transport: "upload", PayloadBytes: 1}
	a, dupA, err := db.BeginRun(ctx, noSession)
	if err != nil || dupA {
		t.Fatalf("无 session 的首次提交应成功，实际 id=%d dup=%v err=%v", a, dupA, err)
	}
	b, dupB, err := db.BeginRun(ctx, noSession)
	if err != nil || dupB || b == a {
		t.Fatalf("无 session 时不做幂等判定，应各自成一行：id=%d/%d dup=%v err=%v", a, b, dupB, err)
	}
	var sessionNull bool
	var automation string
	if err := db.Pool.QueryRow(ctx, `SELECT session_id IS NULL, COALESCE(automation_id, '<NULL>') FROM ingest_runs WHERE id = $1`, b).
		Scan(&sessionNull, &automation); err != nil {
		t.Fatal(err)
	}
	if !sessionNull {
		t.Fatal("session_id 缺省必须落 NULL：部分唯一索引只约束非 NULL 值，落成空串会让第二次提交被误判成重复")
	}
	// 现状口径（留意，与其他可选字段不一致）：automation_id / automation_name 没走 nullable()，
	// 缺省落成空串而不是 NULL。读取侧一律 COALESCE(...,'')，所以行为等价，
	// 但「未提供」在这两列里是 ''、在别的列里是 NULL。
	if automation != "" {
		t.Fatalf("automation_id 缺省的当前口径是空串，实际 %q", automation)
	}
}

// TestFinishRunAndLoadRunStats 锁定收尾统计的口径：
//   - 成功时 status=ok 且 error 是 NULL（NULLIF 把空串折成 NULL）；
//   - 失败时 status=error 且原文进 error；
//   - affected_from/to 空串折成 NULL，读回来还是空串（而不是 0001-01-01）。
func TestFinishRunAndLoadRunStats(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	id, _, err := db.BeginRun(ctx, RunMeta{Transport: "webhook", SessionID: "sess-finish", PayloadBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishRun(ctx, id, IngestStats{
		Received: 12, Inserted: 9, Duplicates: 3, Updated: 1,
		AffectedFrom: "2026-10-01", AffectedTo: "2026-10-13",
	}, nil); err != nil {
		t.Fatalf("FinishRun 失败: %v", err)
	}
	st, err := db.LoadRunStats(ctx, id)
	if err != nil {
		t.Fatalf("LoadRunStats 失败: %v", err)
	}
	if st.RunID != id || st.Received != 12 || st.Inserted != 9 || st.Duplicates != 3 || st.Updated != 1 {
		t.Fatalf("统计回填不符：%+v", st)
	}
	if st.AffectedFrom != "2026-10-01" || st.AffectedTo != "2026-10-13" {
		t.Fatalf("影响日期区间不符：%q ~ %q", st.AffectedFrom, st.AffectedTo)
	}
	var status string
	var runErr *string
	if err := db.Pool.QueryRow(ctx, `SELECT status, error FROM ingest_runs WHERE id = $1`, id).Scan(&status, &runErr); err != nil {
		t.Fatal(err)
	}
	if status != "ok" {
		t.Fatalf("成功收尾的 status 应是 ok，实际 %q", status)
	}
	if runErr != nil {
		t.Fatalf("成功时 error 必须是 NULL（NULLIF 折掉空串），实际 %q", *runErr)
	}

	// 失败路径
	if err := db.FinishRun(ctx, id, IngestStats{Received: 1}, errors.New("解析 payload 失败")); err != nil {
		t.Fatalf("失败收尾报错: %v", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT status, error FROM ingest_runs WHERE id = $1`, id).Scan(&status, &runErr); err != nil {
		t.Fatal(err)
	}
	if status != "error" || runErr == nil || *runErr != "解析 payload 失败" {
		t.Fatalf("失败收尾要留下原因：status=%q error=%v", status, runErr)
	}

	// 没有区间的 run：库里是 NULL，读回应是空串而不是 0001-01-01
	id2, _, err := db.BeginRun(ctx, RunMeta{Transport: "webhook", SessionID: "sess-empty-range"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishRun(ctx, id2, IngestStats{Received: 0}, nil); err != nil {
		t.Fatal(err)
	}
	st2, err := db.LoadRunStats(ctx, id2)
	if err != nil {
		t.Fatal(err)
	}
	if st2.AffectedFrom != "" || st2.AffectedTo != "" {
		t.Fatalf("NULL 区间应读回空串，实际 %q / %q", st2.AffectedFrom, st2.AffectedTo)
	}

	// 不存在的 run：报错，不能给一个零值把「查不到」当成「统计都是 0」
	if _, err := db.LoadRunStats(ctx, 99999999); err == nil {
		t.Fatal("读取不存在的 run 必须报错")
	}
}

func sampleRow(value float64, start time.Time, localDate string) sample.Sample {
	return sample.Sample{
		Metric: "step_count", Source: "iPhone", StartTime: start, EndTime: start.Add(time.Minute),
		Value: value, Unit: "count", LocalDate: localDate, TZOffsetMin: 480,
	}
}

// latestIndex 为 latest 模式补上它需要的唯一索引，用完立刻撤掉。
//
// 生产 schema（migrations/0001_init.sql）里只有 samples_dedup_uidx
// (start_time, metric, source, value)，而 latest 分支写的是
// ON CONFLICT (metric, source, start_time) —— 没有匹配的唯一索引，PG 在生成执行计划
// 阶段就直接报 42P10，所以现网 latest 模式是必然失败的（详见交付报告的 bug 条目）。
// 修这个要动迁移文件，超出本包范围，所以这里临时把索引建起来，
// 好让 latest 的收敛语义本身能被锁住；索引不在用例结束时留着，
// 否则会打断 strict 模式「同刻不同值算两条读数」的设计。
func latestIndex(t *testing.T, db *DB) {
	t.Helper()
	mustExec(t, db, `CREATE UNIQUE INDEX samples_latest_uidx_probe ON samples (metric, source, start_time)`)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DROP INDEX IF EXISTS samples_latest_uidx_probe`)
	})
}

// TestWriteSamplesStrictVsLatest 是本文件的核心：两种去重模式的实际差别。
//
// strict 的冲突键是 (start_time, metric, source, value) —— 只挡「完全相同的重复投递」，
// 改了值的重报会另起一行；latest 的冲突键是 (metric, source, start_time) —— 同一时刻
// 只留最后一次的读数，且值没变时 WHERE 拦掉，不算更新（免得 ingested_at 白白抖动）。
// 这个差别决定了历史数据长什么样，必须在库里锁死。
func TestWriteSamplesStrictVsLatest(t *testing.T) {
	base := time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC)

	t.Run("strict", func(t *testing.T) {
		db := openIngestDB(t)
		ctx := context.Background()

		ins, upd, from, to, err := db.WriteSamples(ctx, 101, []sample.Sample{
			sampleRow(100, base, "2026-10-13"),
			sampleRow(120, base.Add(time.Hour), "2026-10-11"), // 乱序：验 min/max 区间
		}, "strict")
		if err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		if ins != 2 || upd != 0 {
			t.Fatalf("strict 不该报更新，实际 inserted=%d updated=%d", ins, upd)
		}
		if from != "2026-10-11" || to != "2026-10-13" {
			t.Fatalf("影响区间应取样本 local_date 的 min/max，实际 %q ~ %q", from, to)
		}

		// 完全相同的重复投递：一条也进不去
		ins, _, _, _, err = db.WriteSamples(ctx, 102, []sample.Sample{sampleRow(100, base, "2026-10-13")}, "strict")
		if err != nil {
			t.Fatal(err)
		}
		if ins != 0 {
			t.Fatalf("strict 下同一重复样本应 inserted=0，实际 %d", ins)
		}

		// 同一时刻改了值：strict 认为这是另一条读数，会新增一行
		ins, _, _, _, err = db.WriteSamples(ctx, 103, []sample.Sample{sampleRow(105, base, "2026-10-13")}, "strict")
		if err != nil {
			t.Fatal(err)
		}
		if ins != 1 {
			t.Fatalf("strict 的冲突键含 value，改值应新增 1 行，实际 %d", ins)
		}
		var cnt int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM samples WHERE start_time = $1`, base.UTC()).Scan(&cnt); err != nil {
			t.Fatal(err)
		}
		if cnt != 2 {
			t.Fatalf("strict 下同刻两条读数应共存，实际 %d 行", cnt)
		}
	})

	t.Run("latest", func(t *testing.T) {
		db := openIngestDB(t)
		ctx := context.Background()
		latestIndex(t, db) // 见注释：现网缺这个唯一索引，latest 分支本身跑不起来

		ins, upd, _, _, err := db.WriteSamples(ctx, 201, []sample.Sample{sampleRow(100, base, "2026-10-13")}, "latest")
		if err != nil {
			t.Fatal(err)
		}
		if ins != 1 || upd != 1 {
			t.Fatalf("latest 首次写入应 inserted=updated=1，实际 %d/%d", ins, upd)
		}

		// 同值重放：WHERE value IS DISTINCT FROM 拦下，两计数都是 0
		ins, upd, _, _, err = db.WriteSamples(ctx, 202, []sample.Sample{sampleRow(100, base, "2026-10-13")}, "latest")
		if err != nil {
			t.Fatal(err)
		}
		if ins != 0 || upd != 0 {
			t.Fatalf("同值重放在 latest 下不该有更新，实际 %d/%d", ins, upd)
		}
		var ingestedRuns int64
		if err := db.Pool.QueryRow(ctx, `SELECT ingest_run_id FROM samples WHERE start_time = $1`, base.UTC()).Scan(&ingestedRuns); err != nil {
			t.Fatal(err)
		}
		if ingestedRuns != 201 {
			t.Fatalf("同值重放不该改写 ingest_run_id，实际 %d", ingestedRuns)
		}

		// 改值重报：覆盖同一行，不新增
		ins, upd, _, _, err = db.WriteSamples(ctx, 203, []sample.Sample{sampleRow(105, base, "2026-10-13")}, "latest")
		if err != nil {
			t.Fatal(err)
		}
		if ins != 1 || upd != 1 {
			t.Fatalf("latest 改值应覆盖 1 行，实际 %d/%d", ins, upd)
		}
		var (
			value  float64
			runID  int64
			rows   int
			endOff time.Duration
		)
		if err := db.Pool.QueryRow(ctx, `SELECT value, ingest_run_id, end_time - start_time FROM samples WHERE start_time = $1`, base.UTC()).
			Scan(&value, &runID, &endOff); err != nil {
			t.Fatal(err)
		}
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM samples`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("latest 必须收敛成一行（否则步数会被双计），实际 %d 行", rows)
		}
		if value != 105 || runID != 203 {
			t.Fatalf("覆盖后的值与来源 run 不符：value=%v run=%d", value, runID)
		}
		if endOff != time.Minute {
			t.Fatalf("end_time 也要跟着覆盖，实际间隔 %v", endOff)
		}
	})

	// staging 表是临时的，函数返回时必须清掉，不能在库里越堆越多。
	db := openIngestDB(t)
	ctx := context.Background()
	if _, _, _, _, err := db.WriteSamples(ctx, 300, []sample.Sample{sampleRow(1, base, "2026-10-13")}, "strict"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := db.Pool.QueryRow(ctx, `SELECT to_regclass('samples_staging_300') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("staging 表用完没删")
	}
}

// TestWriteSamplesErrorPaths 锁定写库失败时的错误上下文与「不留脏数据」：
//   - 空列表直接返回零值，不建 staging、不报错；
//   - local_date 非法 → COPY 阶段就失败，samples 一行没进；
//   - latest 模式同一批里出现同键不同值 → PG 拒绝「一次影响同一行两次」，
//     错误必须带「合并进 samples 失败」，否则运维看不出是哪一步炸的。
func TestWriteSamplesErrorPaths(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC)

	ins, upd, from, to, err := db.WriteSamples(ctx, 1, nil, "strict")
	if err != nil || ins != 0 || upd != 0 || from != "" || to != "" {
		t.Fatalf("空样本集应零值返回，实际 %d/%d %q~%q %v", ins, upd, from, to, err)
	}

	bad := sampleRow(10, base, "2026-10-13")
	bad.LocalDate = "不是日期"
	if _, _, _, _, err := db.WriteSamples(ctx, 401, []sample.Sample{bad}, "strict"); err == nil {
		t.Fatal("非法 local_date 必须报错")
	} else if !strings.Contains(err.Error(), "COPY 到 staging 失败") {
		t.Fatalf("报错要指出是 COPY 阶段：%v", err)
	}
	var cnt int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM samples`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("COPY 失败后不该留下半批数据，实际 %d 行", cnt)
	}

	// 现网 schema 里没有 (metric, source, start_time) 的唯一索引：latest 分支在
	// PG 生成计划阶段就被 42P10 拒掉。这条断言是「修 bug 的警报器」——
	// 一旦迁移补上了索引，这里会失败，提醒把上面的用例改回预期语义。
	dup := []sample.Sample{sampleRow(10, base, "2026-10-13"), sampleRow(20, base, "2026-10-13")}
	if _, _, _, _, err := db.WriteSamples(ctx, 402, dup, "latest"); err == nil {
		t.Fatal("缺唯一索引时 latest 分支必须报错")
	} else if !strings.Contains(err.Error(), "no unique or exclusion constraint matching the ON CONFLICT specification") {
		t.Fatalf("latest 模式当前的失败原因变了，请复核 schema 与断言：%v", err)
	}

	// 把索引补上后，同批内同键不同值应报「一次影响同一行两次」，
	// 而不是静默丢掉一条（错误必须带「合并进 samples 失败」这层上下文）。
	latestIndex(t, db)
	if _, _, _, _, err := db.WriteSamples(ctx, 403, dup, "latest"); err == nil {
		t.Fatal("同批内同键不同值在 latest 下必须报错，不能静默丢一条")
	} else if !strings.Contains(err.Error(), "合并进 samples 失败") {
		t.Fatalf("报错要指出是合并阶段：%v", err)
	} else if !strings.Contains(err.Error(), "cannot affect row a second time") {
		t.Fatalf("应命中 PG 的「同一行被影响两次」保护：%v", err)
	}

	// 建 staging 就失败（连到没有表的 schema）：错误原样抛出，不该走到 COPY
	empty := openMissingSchemaDB(t)
	if _, _, _, _, err := empty.WriteSamples(ctx, 1, []sample.Sample{sampleRow(1, base, "2026-10-13")}, "strict"); err == nil {
		t.Fatal("库不可用时 WriteSamples 必须报错")
	}
}

// TestWriteSamplesMetaAndTimeContract 锁定 jsonb 与时刻的存储约定：
//   - meta 为空 / 为不可编码值时落 NULL，读回是 SQL NULL 而不是 '{}'；
//   - start_time / end_time 存的是 UTC：不同输入时区、同一时刻必须撞在同一个键上。
func TestWriteSamplesMetaAndTimeContract(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	utc := time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC)
	// 同一个瞬间的 +0800 墙钟写法：不依赖系统时区库，靠 FixedZone 精确构造。
	shanghai := time.Date(2026, 10, 13, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))
	lo := 1.0
	in, _, _, _, err := db.WriteSamples(ctx, 500, []sample.Sample{
		{Metric: "hr", Source: "Watch", StartTime: utc, EndTime: utc, Value: 60, ValueMin: &lo, Unit: "bpm",
			LocalDate: "2026-10-13", TZOffsetMin: 480, Meta: map[string]any{"stage": "AsleepCore", "n": 3}},
		// 与上一条是同一个瞬间、同一个值，只是用 +0800 的墙钟写出来：
		// 存成 UTC 后必须撞在同一个去重键上，一条都进不去。
		{Metric: "hr", Source: "Watch", StartTime: shanghai, EndTime: shanghai, Value: 60, ValueMin: &lo, Unit: "bpm",
			LocalDate: "2026-10-13", TZOffsetMin: 480},
		{Metric: "hr", Source: "Watch", StartTime: utc.Add(2 * time.Hour), EndTime: utc.Add(2 * time.Hour), Value: 62,
			Unit: "bpm", LocalDate: "2026-10-13", Meta: map[string]any{"nan": math.NaN()}},
		{Metric: "hr", Source: "Watch", StartTime: utc.Add(3 * time.Hour), EndTime: utc.Add(3 * time.Hour), Value: 63,
			Unit: "bpm", LocalDate: "2026-10-13"},
	}, "strict")
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if in != 3 {
		t.Fatalf("两种时区写法的同一瞬间必须收敛成一条，实际入库 %d", in)
	}

	var sameKey, wallLeak int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM samples WHERE start_time = '2026-10-13T01:00:00Z'::timestamptz`).Scan(&sameKey); err != nil {
		t.Fatal(err)
	}
	if sameKey != 1 {
		t.Fatalf("start_time 必须按 UTC 存，同一瞬间只能有一行，实际 %d 行", sameKey)
	}
	// 库内绝不能出现「把 +0800 的墙钟数字当 UTC 存」的写法（那是差 8 小时的脏数据）
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM samples WHERE start_time = '2026-10-13T09:00:00Z'::timestamptz`).Scan(&wallLeak); err != nil {
		t.Fatal(err)
	}
	if wallLeak != 0 {
		t.Fatalf("有 %d 行把本地墙钟当成了 UTC 存", wallLeak)
	}

	// meta 是 jsonb：缺字段读回 NULL，而不是让整行消失
	var stage *string
	var numVal *int
	if err := db.Pool.QueryRow(ctx, `SELECT meta->>'stage', (meta->>'n')::int FROM samples
		WHERE start_time = '2026-10-13T01:00:00Z'::timestamptz`).Scan(&stage, &numVal); err != nil {
		t.Fatal(err)
	}
	if stage == nil || *stage != "AsleepCore" || numVal == nil || *numVal != 3 {
		t.Fatalf("meta 的 jsonb 字段没读回来：%v / %v", stage, numVal)
	}
	// value_min 有值、value_max 缺省：分别是非 NULL 与 NULL
	var vmin, vmax *float64
	if err := db.Pool.QueryRow(ctx, `SELECT value_min, value_max FROM samples
		WHERE start_time = '2026-10-13T01:00:00Z'::timestamptz`).Scan(&vmin, &vmax); err != nil {
		t.Fatal(err)
	}
	if vmin == nil || *vmin != 1 || vmax != nil {
		t.Fatalf("value_min/max 的 NULL 语义丢了：%v / %v", vmin, vmax)
	}
	// 不可编码的 meta 落 NULL，而不是让整批 COPY 失败
	for _, ts := range []string{"2026-10-13T03:00:00Z", "2026-10-13T04:00:00Z"} {
		var isNull bool
		if err := db.Pool.QueryRow(ctx, `SELECT meta IS NULL FROM samples WHERE start_time = $1::timestamptz`, ts).Scan(&isNull); err != nil {
			t.Fatal(err)
		}
		if !isNull {
			t.Fatalf("%s 的 meta 应该是 NULL", ts)
		}
	}
}

// TestWriteWorkoutsAndSleepConflictKeys 锁定 upsert 的冲突键到底是什么：
//   - workouts：(name, source, start_time)，重复投递回 0；
//   - sleep_sessions：(local_date, source, sleep_start)，同一条重复投递回 0；
//   - sleep_start 为 NULL 时唯一索引不约束 NULL，同日同来源两条都会入库（这是
//     Apple 导出里「只给在床时间」的常态，不能当成重复丢掉）。
func TestWriteWorkoutsAndSleepConflictKeys(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	if n, err := db.WriteWorkouts(ctx, 1, nil); err != nil || n != 0 {
		t.Fatalf("空列表应零值返回：%d %v", n, err)
	}
	if n, err := db.WriteSleep(ctx, 1, nil); err != nil || n != 0 {
		t.Fatalf("空睡眠列表应零值返回：%d %v", n, err)
	}

	start := time.Date(2026, 10, 13, 0, 30, 0, 0, time.UTC)
	w := sample.Workout{Name: "户外跑步", Source: "Apple Watch", StartTime: start, EndTime: start.Add(30 * time.Minute),
		DurationMin: 30, LocalDate: "2026-10-13", TZOffsetMin: 480}
	if n, err := db.WriteWorkouts(ctx, 9, []sample.Workout{w}); err != nil || n != 1 {
		t.Fatalf("首次写入运动应 1 条：%d %v", n, err)
	}
	if n, err := db.WriteWorkouts(ctx, 10, []sample.Workout{w}); err != nil || n != 0 {
		t.Fatalf("同 (name, source, start_time) 重放应 0 条：%d %v", n, err)
	}
	// 换个来源就不算重复：来源是冲突键的一部分
	other := w
	other.Source = "iPhone"
	if n, err := db.WriteWorkouts(ctx, 11, []sample.Workout{other}); err != nil || n != 1 {
		t.Fatalf("不同来源的同名运动不该被判重复：%d %v", n, err)
	}
	var runID int64
	if err := db.Pool.QueryRow(ctx, `SELECT ingest_run_id FROM workouts WHERE source = 'Apple Watch'`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if runID != 9 {
		t.Fatalf("重复投递不该改写 ingest_run_id，实际 %d", runID)
	}

	s1 := at0800(10, 12, 23, 30)
	e1 := at0800(10, 13, 7, 0)
	session := sample.SleepSession{LocalDate: "2026-10-12", Source: "Apple Watch", SleepStart: &s1, SleepEnd: &e1,
		TZOffsetMin: 480, TotalSleepHr: fptr(7.5)}
	if n, err := db.WriteSleep(ctx, 20, []sample.SleepSession{session}); err != nil || n != 1 {
		t.Fatalf("首次写入睡眠应 1 条：%d %v", n, err)
	}
	if n, err := db.WriteSleep(ctx, 21, []sample.SleepSession{session}); err != nil || n != 0 {
		t.Fatalf("同 (local_date, source, sleep_start) 重放应 0 条：%d %v", n, err)
	}
	// 只给在床时间（sleep_start 为 NULL）：两条都要留下
	nullStart := sample.SleepSession{LocalDate: "2026-10-11", Source: "Apple Watch", TZOffsetMin: 480,
		InBedStart: ptr(at0800(10, 11, 22, 30)), InBedEnd: ptr(at0800(10, 12, 7, 0)), InBedHr: fptr(8.5)}
	nullStart2 := nullStart
	nullStart2.InBedStart = ptr(at0800(10, 11, 23, 0))
	if n, err := db.WriteSleep(ctx, 22, []sample.SleepSession{nullStart, nullStart2}); err != nil || n != 2 {
		t.Fatalf("sleep_start 为 NULL 的两条都该入库：%d %v", n, err)
	}
	// 同键不同 sleep_start 是另一晚，照常入库
	diff := session
	diff.SleepStart = ptr(at0800(10, 12, 23, 45))
	if n, err := db.WriteSleep(ctx, 23, []sample.SleepSession{diff}); err != nil || n != 1 {
		t.Fatalf("不同入睡时刻不该被判重复：%d %v", n, err)
	}
}

// TestUpdateWatermarkNeverGoesBack 锁定水位口径：GREATEST —— 只进不退。
// 补历史数据的那次投递绝不能把水位拽回过去，否则「同步到哪天了」会越补越旧。
func TestUpdateWatermarkNeverGoesBack(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	newest := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	if err := db.UpdateWatermark(ctx, "auto-1", map[string]Watermark{
		"step_count": {Time: newest, LocalDate: "2026-10-13"},
	}); err != nil {
		t.Fatalf("写水位失败: %v", err)
	}
	// 更旧的一批：时间与日期都必须保持不动
	if err := db.UpdateWatermark(ctx, "auto-1", map[string]Watermark{
		"step_count": {Time: newest.AddDate(0, 0, -5), LocalDate: "2026-10-08"},
	}); err != nil {
		t.Fatal(err)
	}
	var (
		at time.Time
		ld string
	)
	if err := db.Pool.QueryRow(ctx, `SELECT last_start_time, last_local_date::text FROM sync_watermark
		WHERE automation_id = 'auto-1' AND metric = 'step_count'`).Scan(&at, &ld); err != nil {
		t.Fatal(err)
	}
	if !at.Equal(newest) || ld != "2026-10-13" {
		t.Fatalf("水位只进不退失效：%v / %s", at, ld)
	}

	// 换 automation_id 是另一条水位，互不影响
	if err := db.UpdateWatermark(ctx, "auto-2", map[string]Watermark{
		"step_count": {Time: newest.AddDate(0, 0, -20), LocalDate: "2026-09-23"},
	}); err != nil {
		t.Fatal(err)
	}
	var cnt int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM sync_watermark`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 2 {
		t.Fatalf("水位按 (automation_id × metric) 存，实际 %d 行", cnt)
	}

	// 写入的是绝对时刻（timestamptz）：库里的值必须等于我们给的那个瞬间，
	// 与 session TimeZone 无关（::text 会按会话时区渲染，所以不能拿字符串比）。
	var stored time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT last_start_time FROM sync_watermark WHERE automation_id = 'auto-2'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Equal(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("水位时刻存的要与写入的绝对瞬间一致，实际 %v", stored.UTC())
	}
}

// TestEnqueueRollupIdempotent 锁定重算队列的幂等：同一 (日期 × 指标) 反复入队只有一行，
// 否则队列会被重复投递灌爆、同一天的聚合被反复重算。
func TestEnqueueRollupIdempotent(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	dates := map[string]map[string]struct{}{
		"2026-10-13": {"step_count": {}, "heart_rate": {}},
		"2026-10-12": {"step_count": {}},
	}
	if err := db.EnqueueRollup(ctx, dates); err != nil {
		t.Fatalf("入队失败: %v", err)
	}
	if err := db.EnqueueRollup(ctx, dates); err != nil {
		t.Fatalf("重复入队失败: %v", err)
	}
	if err := db.EnqueueRollup(ctx, map[string]map[string]struct{}{}); err != nil {
		t.Fatalf("空集合不该报错: %v", err)
	}
	var cnt int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM rollup_queue`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 3 {
		t.Fatalf("期望 3 个待重算项（重复入队合并），实际 %d", cnt)
	}
}

// TestClassifySource 锁死来源类别的粗判规则：设备类别影响页面上的来源分组。
func TestClassifySource(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Apple Watch Series 9", "watch"},
		{"apple watch", "watch"},
		{"iPhone", "phone"},
		{"iPad", "phone"},
		{"Health App", "app"},
		{"健康", "unknown"},
		{"Whoop", "unknown"},
		{"", "unknown"},
	}
	for _, c := range cases {
		if got := ClassifySource(c.in); got != c.want {
			t.Fatalf("ClassifySource(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestIngestHelpers 锁住几个小工具的实际回落：
//   - metaJSON：nil / 不可编码都回 nil（库里是 NULL），不能把错误冒出来打断整批写入；
//   - nullable：空串 → NULL，非空 → 原值。
func TestIngestHelpers(t *testing.T) {
	if b := metaJSON(nil); b != nil {
		t.Fatalf("空 meta 应落 nil（库里 NULL），实际 %v", b)
	}
	if b := metaJSON(map[string]any{}); b != nil {
		t.Fatalf("长度为 0 的 map 同样算没 meta，实际 %v", b)
	}
	if b := metaJSON(map[string]any{"x": math.NaN()}); b != nil {
		t.Fatalf("编码不了的 meta 要静默降级成 NULL，实际 %v", b)
	}
	if b := metaJSON(map[string]any{"x": 1}); string(b) != `{"x":1}` {
		t.Fatalf("正常 meta 应原样编码，实际 %s", b)
	}
}

// TestIngestErrorPaths 覆盖依赖库不可用的错误分支（连到没有表的 schema）。
func TestIngestErrorPaths(t *testing.T) {
	db := openMissingSchemaDB(t)
	ctx := context.Background()

	if _, _, err := db.BeginRun(ctx, RunMeta{Transport: "webhook", SessionID: "x"}); err == nil {
		t.Fatal("库不可用时 BeginRun 必须报错")
	}
	if err := db.FinishRun(ctx, 1, IngestStats{}, nil); err == nil {
		t.Fatal("库不可用时 FinishRun 必须报错")
	}
	if _, err := db.LoadRunStats(ctx, 1); err == nil {
		t.Fatal("库不可用时 LoadRunStats 必须报错")
	}
	if err := db.UpsertSources(ctx, []string{"Apple Watch"}); err == nil {
		t.Fatal("库不可用时 UpsertSources 必须报错")
	}
	if err := db.UpdateWatermark(ctx, "a", map[string]Watermark{"step_count": {Time: time.Now()}}); err == nil {
		t.Fatal("库不可用时 UpdateWatermark 必须报错")
	}
	if err := db.EnqueueRollup(ctx, map[string]map[string]struct{}{"2026-10-13": {"step_count": {}}}); err == nil {
		t.Fatal("库不可用时 EnqueueRollup 必须报错")
	}
	if _, err := db.WriteWorkouts(ctx, 1, []sample.Workout{{Name: "x", StartTime: time.Now(), LocalDate: "2026-10-13"}}); err == nil {
		t.Fatal("库不可用时 WriteWorkouts 必须报错")
	}
	s := time.Now()
	if _, err := db.WriteSleep(ctx, 1, []sample.SleepSession{{LocalDate: "2026-10-13", SleepStart: &s}}); err == nil {
		t.Fatal("库不可用时 WriteSleep 必须报错")
	}
}

// TestIngestWithContextCancelled 覆盖拿不到连接时的分支（Acquire / QueryRow 直接失败）。
func TestIngestWithContextCancelled(t *testing.T) {
	db := openIngestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := db.BeginRun(ctx, RunMeta{Transport: "webhook"}); err == nil {
		t.Fatal("取消的 ctx 下 BeginRun 必须报错")
	}
	if err := db.FinishRun(ctx, 1, IngestStats{}, nil); err == nil {
		t.Fatal("取消的 ctx 下 FinishRun 必须报错")
	}
	if err := db.EnqueueRollup(ctx, map[string]map[string]struct{}{"2026-10-13": {"step_count": {}}}); err == nil {
		t.Fatal("取消的 ctx 下 EnqueueRollup 必须报错")
	}
	if _, err := db.WriteWorkouts(ctx, 1, []sample.Workout{{Name: "x", LocalDate: "2026-10-13"}}); err == nil {
		t.Fatal("取消的 ctx 下 WriteWorkouts 必须报错")
	}
	if err := db.UpsertSources(ctx, []string{"Apple Watch"}); err == nil {
		t.Fatal("取消的 ctx 下 UpsertSources 必须报错")
	}

	// execScript 在拿不到连接时把错误原样抛出（Migrate 依赖它，不能吞）
	if err := db.execScript(ctx, "SELECT 1"); err == nil {
		t.Fatal("取消的 ctx 下 execScript 必须报错")
	}
}
