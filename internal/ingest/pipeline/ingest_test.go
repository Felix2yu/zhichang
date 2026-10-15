package pipeline

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/config"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/ingest/hae"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
	"github.com/Felix2yu/zhichang/internal/testdb"
)

// TestMain 起停本包的测试库；ZHICHANG_TEST_DSN 存在时直接用它。
func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

// 集成测试：跑在自举的 PostgreSQL 上（ZHICHANG_TEST_DSN 指向真库时优先用那个）。
func openTestDB(t *testing.T) *postgres.DB {
	t.Helper()
	ctx := context.Background()
	db, err := postgres.Open(ctx, config.Config{DBDSN: testdb.DSN(t)})
	if err != nil {
		t.Skipf("数据库不可用: %v", err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if _, err := db.SeedMetrics(ctx, metric.Defaults()); err != nil {
		t.Fatalf("字典写入失败: %v", err)
	}
	for _, q := range []string{
		`TRUNCATE samples, workout_routes, workouts, sleep_sessions, ecg_records, ecg_voltages, ingest_runs, rollup_queue, sync_watermark, sources, daily_metrics, daily_metrics_day`,
	} {
		if _, err := db.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("清理失败: %v", err)
		}
	}
	t.Cleanup(db.Close)
	return db
}

func TestIngestEndToEnd(t *testing.T) {
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
	svc := New(db, reg, home, "strict")

	f, err := os.Open("../../../testdata/hae_export_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	payload, err := hae.ParsePayload(f)
	if err != nil {
		t.Fatal(err)
	}

	out, err := svc.Ingest(ctx, payload, postgres.RunMeta{Transport: "rest", AutomationID: "auto-1", SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("首次摄入失败: %v", err)
	}
	if out.Inserted != 8 || out.Duplicates != 0 {
		t.Fatalf("首次应插入 8 条且无重复，实际 inserted=%d duplicates=%d", out.Inserted, out.Duplicates)
	}
	if out.Workouts != 1 || out.Sleep != 1 {
		t.Fatalf("运动/睡眠写入错误: workouts=%d sleep=%d", out.Workouts, out.Sleep)
	}
	if out.DuplicateRun {
		t.Fatal("首次不应判定为重复")
	}

	// 同一 session 重复提交：幂等回放
	again, err := svc.Ingest(ctx, payload, postgres.RunMeta{Transport: "rest", AutomationID: "auto-1", SessionID: "sess-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !again.DuplicateRun || again.Inserted != 8 {
		t.Fatalf("重复 session 应回放上次结果: %+v", again)
	}

	// 新 session 但相同数据：全部命中去重
	third, err := svc.Ingest(ctx, payload, postgres.RunMeta{Transport: "rest", AutomationID: "auto-1", SessionID: "sess-2"})
	if err != nil {
		t.Fatal(err)
	}
	if third.Inserted != 0 || third.Duplicates != 8 {
		t.Fatalf("重复数据应全部命中去重: inserted=%d duplicates=%d", third.Inserted, third.Duplicates)
	}

	// 落库校验
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM samples`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 8 {
		t.Fatalf("库中样本应为 8，实际 %d", count)
	}
	var offset int
	if err := db.Pool.QueryRow(ctx, `SELECT tz_offset_min FROM samples WHERE metric='step_count' LIMIT 1`).Scan(&offset); err != nil {
		t.Fatal(err)
	}
	if offset != 480 {
		t.Fatalf("tz_offset_min 应为 480，实际 %d", offset)
	}
	var vmin, vmax float64
	if err := db.Pool.QueryRow(ctx, `SELECT value_min, value_max FROM samples WHERE metric='heart_rate' ORDER BY start_time LIMIT 1`).Scan(&vmin, &vmax); err != nil {
		t.Fatal(err)
	}
	if vmin != 60 || vmax != 88 {
		t.Fatalf("心率极值落库错误: min=%v max=%v", vmin, vmax)
	}
	var sleepDate string
	if err := db.Pool.QueryRow(ctx, `SELECT local_date::text FROM sleep_sessions LIMIT 1`).Scan(&sleepDate); err != nil {
		t.Fatal(err)
	}
	if sleepDate != "2026-10-12" {
		t.Fatalf("睡眠应归属 2026-10-12，实际 %s", sleepDate)
	}
	var queued int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM rollup_queue`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued == 0 {
		t.Fatal("rollup 队列应有待重算项")
	}
	var srcCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM sources`).Scan(&srcCount); err != nil {
		t.Fatal(err)
	}
	if srcCount != 2 {
		t.Fatalf("应登记 2 个来源（iPhone / Apple Watch），实际 %d", srcCount)
	}
	var wm int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM sync_watermark`).Scan(&wm); err != nil {
		t.Fatal(err)
	}
	if wm == 0 {
		t.Fatal("同步水位应有记录")
	}
}
