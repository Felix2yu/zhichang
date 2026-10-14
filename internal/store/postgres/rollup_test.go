package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/config"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/testdb"
)

// TestMain 起停本包的测试库；ZHICHANG_TEST_DSN 存在时直接用它。
func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

func openRollupDB(t *testing.T) *DB {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	db, err := Open(ctx, config.Config{DBDSN: dsn})
	if err != nil {
		t.Skipf("数据库不可用: %v", err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `TRUNCATE samples, daily_metrics, daily_metrics_day, rollup_queue`); err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// TestRollupAggregationSemantics 校验核心口径：
// 累计量日内 sum 且跨来源取 max；瞬时量日内 mean 且跨来源按样本数加权 mean。
func TestRollupAggregationSemantics(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	reg := metric.New(metric.Defaults())
	date := "2026-10-13"
	at := func(h, m int) time.Time { return time.Date(2026, 10, 13, h-8, m, 0, 0, time.UTC) } // +0800 → UTC

	samples := []sample.Sample{
		{Metric: "step_count", Source: "iPhone", StartTime: at(8, 0), Value: 1200, Unit: "count", LocalDate: date, TZOffsetMin: 480},
		{Metric: "step_count", Source: "Apple Watch", StartTime: at(8, 0), Value: 1200, Unit: "count", LocalDate: date, TZOffsetMin: 480},
		{Metric: "step_count", Source: "Apple Watch", StartTime: at(9, 0), Value: 800, Unit: "count", LocalDate: date, TZOffsetMin: 480},
		{Metric: "step_count", Source: "iPhone", StartTime: at(9, 0), Value: 850, Unit: "count", LocalDate: date, TZOffsetMin: 480},
		{Metric: "heart_rate", Source: "Apple Watch", StartTime: at(8, 15), Value: 72, Unit: "bpm", LocalDate: date, TZOffsetMin: 480},
		{Metric: "heart_rate", Source: "Apple Watch", StartTime: at(9, 15), Value: 76, Unit: "bpm", LocalDate: date, TZOffsetMin: 480},
	}
	for i := range samples {
		samples[i].EndTime = samples[i].StartTime
	}

	if _, _, _, _, err := db.WriteSamples(ctx, 1, samples, "strict"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	r := NewRollup(db, reg)
	if err := r.Recompute(ctx, []string{date}, []string{"step_count", "heart_rate"}); err != nil {
		t.Fatalf("重算失败: %v", err)
	}

	got := map[string]float64{}
	rows, err := db.Pool.Query(ctx, `SELECT metric, value FROM daily_metrics_day WHERE local_date = $1`, toDateArray([]string{date})[0])
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		var v float64
		if err := rows.Scan(&m, &v); err != nil {
			t.Fatal(err)
		}
		got[m] = v
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}

	// 各来源日内求和：iPhone 2050、Watch 2000 → 跨来源取 max = 2050
	if got["step_count"] != 2050 {
		t.Fatalf("步数应为 2050（跨来源取最大），实际 %v", got["step_count"])
	}
	// 心率日内 mean = (72+76)/2 = 74
	if got["heart_rate"] != 74 {
		t.Fatalf("心率应为 74（日内均值），实际 %v", got["heart_rate"])
	}

	// 幂等：重复重算结果不变
	if err := r.Recompute(ctx, []string{date}, []string{"step_count", "heart_rate"}); err != nil {
		t.Fatal(err)
	}
	var cnt int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM daily_metrics_day WHERE local_date = $1`, toDateArray([]string{date})[0]).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 2 {
		t.Fatalf("重复重算不应产生重复行，实际 %d", cnt)
	}
}
