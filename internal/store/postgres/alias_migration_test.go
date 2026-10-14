package postgres

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/migrations"
)

// aliasMigrationVersion 是负责别名收敛的迁移文件名。
const aliasMigrationVersion = "0004_metric_aliases.sql"

// TestMetricAliasMigrationMergesHistoricalRows 在真库上跑一遍历史数据搬迁。
//
// 这条迁移是「会动用户数据」的那一类，所以每条分支都要有断言：
//   - 旧名行改名到规范名（否则老数据永远回不到卡片上）；
//   - 撞上唯一约束的行删旧留新（否则整个迁移会被唯一索引打回，
//     启动直接失败——比不迁移更糟）；
//   - 各派生表（天级中间量、天级结果、重算队列、同步水位、目标值）一起搬；
//   - 字典里的旧条目被删掉（否则趋势页两个同名 chip，其中一个永远没数据）。
func TestMetricAliasMigrationMergesHistoricalRows(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	mustExec(t, db, `TRUNCATE sync_watermark`)
	mustExec(t, db, `TRUNCATE goals`)

	const ts = `'2026-10-01 00:00:00+00'`
	insertSample := func(metricName, source string, value float64, date string) {
		t.Helper()
		mustExec(t, db, `INSERT INTO samples
			(start_time, end_time, metric, source, value, unit, local_date, tz_offset_min)
			VALUES ($1::timestamptz, $1::timestamptz, $2, $3, $4, 'x', $5::date, 480)`,
			ts, metricName, source, value, date)
	}
	// 血氧：旧名历史行 + 规范名已有行。第二条旧名行与规范名行
	// 在 (start_time, metric, source, value) 上完全重合 → 必须删旧留新。
	insertSample("blood_oxygen_saturation", "Watch", 95.5, "2026-10-01")
	insertSample("blood_oxygen_saturation", "Watch", 96.1, "2026-10-01")
	insertSample("oxygen_saturation", "Watch", 96.1, "2026-10-01")
	// 站立：分钟数与小时数是两个指标，改名后不能互相撞上
	insertSample("apple_stand_time", "Watch", 120, "2026-10-01")
	insertSample("apple_stand_hour", "Watch", 12, "2026-10-01")

	mustExec(t, db, `INSERT INTO daily_metrics
		(local_date, metric, source, sample_count, sum_value) VALUES
		('2026-10-01','blood_oxygen_saturation','Watch',2,191.6),
		('2026-10-01','oxygen_saturation','Watch',1,96.1),
		('2026-10-02','apple_exercise_time','Watch',1,30)`)
	mustExec(t, db, `INSERT INTO daily_metrics_day (local_date, metric, value) VALUES
		('2026-10-01','blood_oxygen_saturation',95.8),
		('2026-10-01','oxygen_saturation',96.1),
		('2026-10-02','apple_exercise_time',30)`)
	mustExec(t, db, `INSERT INTO rollup_queue (local_date, metric) VALUES
		('2026-10-01','blood_oxygen_saturation'),
		('2026-10-02','apple_exercise_time')`)
	mustExec(t, db, `INSERT INTO sync_watermark (automation_id, metric, last_start_time, last_local_date) VALUES
		('auto-1','blood_oxygen_saturation','2026-10-01 00:00:00+00','2026-10-01'),
		('auto-1','oxygen_saturation','2026-10-01 00:00:00+00','2026-10-01'),
		('auto-2','apple_stand_time','2026-10-01 00:00:00+00','2026-10-01')`)
	mustExec(t, db, `INSERT INTO goals (metric, target) VALUES
		('distance_walking_running', 5),
		('walking_running_distance', 3),
		('weight_&_body_mass', 70)`)
	mustExec(t, db, `INSERT INTO metric_defs
		(metric, display_name_zh, kind, day_agg, cross_source_agg, unit, preferred_unit, value_field, chart, enabled, sort_order)
		VALUES
		('distance_walking_running','步行+跑步距离','cumulative','sum','max','km','km','qty','bar',true,51),
		('weight_&_body_mass','体重','instant','mean','mean','kg','kg','qty','line',true,191),
		('blood_oxygen_saturation','血氧饱和度','instant','mean','mean','%','%','qty','line',true,150)
		ON CONFLICT (metric) DO NOTHING`)

	// 迁移在 testdb 自举时已经跑过（空库上是空操作），这里退回「未应用」再真实跑一遍。
	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = $1`, aliasMigrationVersion)
	applied, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("别名迁移失败: %v", err)
	}
	if len(applied) != 1 || applied[0] != aliasMigrationVersion {
		t.Fatalf("应只补上 %s，实际 %v", aliasMigrationVersion, applied)
	}

	// —— 原始样本 ——
	if n := countMetricRows(t, db, "samples", "blood_oxygen_saturation"); n != 0 {
		t.Fatalf("旧名行必须清空，实际还剩 %d 条", n)
	}
	if n := countMetricRows(t, db, "samples", "oxygen_saturation"); n != 2 {
		t.Fatalf("血氧规范名下应有 2 条（重名的那条删旧留新），实际 %d 条", n)
	}
	if n := countMetricRows(t, db, "samples", "stand_time"); n != 1 {
		t.Fatalf("站立时长应改名到 stand_time，实际 %d 条", n)
	}
	if n := countMetricRows(t, db, "samples", "stand_hour"); n != 1 {
		t.Fatalf("站立小时数应改名到 stand_hour（不能并进 stand_time），实际 %d 条", n)
	}
	if n := countMetricRows(t, db, "samples", "apple_stand_time"); n != 0 {
		t.Fatalf("旧名 apple_stand_time 必须清空，实际 %d 条", n)
	}

	// —— 派生表 ——
	if n := countMetricRows(t, db, "daily_metrics", "blood_oxygen_saturation"); n != 0 {
		t.Fatalf("daily_metrics 里的旧名残留 %d 条", n)
	}
	if n := countMetricRows(t, db, "daily_metrics", "oxygen_saturation"); n != 1 {
		t.Fatalf("daily_metrics 同 (日期,来源) 应删旧留新为 1 条，实际 %d 条", n)
	}
	if n := countMetricRows(t, db, "daily_metrics", "exercise_time"); n != 1 {
		t.Fatalf("daily_metrics 不冲突的旧名行应改名保留，实际 %d 条", n)
	}
	if n := countMetricRows(t, db, "daily_metrics_day", "blood_oxygen_saturation"); n != 0 {
		t.Fatalf("daily_metrics_day 里的旧名残留 %d 条", n)
	}
	if n := countMetricRows(t, db, "daily_metrics_day", "oxygen_saturation"); n != 1 {
		t.Fatalf("daily_metrics_day 应删旧留新为 1 条，实际 %d 条", n)
	}
	if n := countMetricRows(t, db, "rollup_queue", "blood_oxygen_saturation"); n != 0 {
		t.Fatalf("重算队列里的旧名残留 %d 条", n)
	}
	if n := countMetricRows(t, db, "sync_watermark", "blood_oxygen_saturation"); n != 0 {
		t.Fatalf("同步水位里的旧名残留 %d 条", n)
	}
	if n := countMetricRows(t, db, "sync_watermark", "stand_time"); n != 1 {
		t.Fatalf("另一个 automation 的水位应改名保留，实际 %d 条", n)
	}
	if n := countMetricRows(t, db, "goals", "distance_walking_running"); n != 0 {
		t.Fatalf("目标值里的旧名残留 %d 条", n)
	}
	if n := countMetricRows(t, db, "goals", "walking_running_distance"); n != 1 {
		t.Fatalf("目标值应删旧留新为 1 条，实际 %d 条", n)
	}
	if n := countMetricRows(t, db, "goals", "body_mass"); n != 1 {
		t.Fatalf("体重目标应改名到 body_mass，实际 %d 条", n)
	}
	// 删除旧条目时不能误伤别的指标
	if n := countMetricRows(t, db, "metric_defs", "blood_oxygen_saturation"); n != 0 {
		t.Fatalf("字典里的旧条目必须删除，实际 %d 条", n)
	}

	// —— 重算队列：受影响的日期必须排进去，否则界面拿不到改名后的天级值 ——
	var pending int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM rollup_queue WHERE metric IN ('oxygen_saturation','stand_time','stand_hour')`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending < 3 {
		t.Fatalf("受影响指标应排进重算队列，实际 %d 条", pending)
	}
}

// TestAliasMigrationMatchesGoAliasTable 是 Go 别名表与 SQL 迁移表之间的一致性契约。
//
// 两处各写一份，靠人眼同步必然漂移：新增一个别名却忘了改 SQL，
// 历史行就继续躺在旧名下——而这正是这条迁移要解决的问题本身。
func TestAliasMigrationMatchesGoAliasTable(t *testing.T) {
	files, err := migrations.Files()
	if err != nil {
		t.Fatal(err)
	}
	var sql string
	for _, f := range files {
		if f.Version == aliasMigrationVersion {
			sql = f.SQL
		}
	}
	if sql == "" {
		t.Fatalf("找不到迁移 %s", aliasMigrationVersion)
	}

	// 只取 VALUES 列表那一段，避免把别的括号元组（如 IN (...) 列表）也算进来
	const head = "SELECT * FROM (VALUES"
	const tail = ") AS t(old_name, new_name)"
	start := strings.Index(sql, head)
	end := strings.Index(sql, tail)
	if start < 0 || end < 0 || end < start {
		t.Fatalf("迁移里找不到别名 VALUES 列表（表头 %q / 表尾 %q）", head, tail)
	}
	body := sql[start+len(head) : end]

	pair := regexp.MustCompile(`\(\s*'([^']+)'\s*,\s*'([^']+)'\s*\)`)
	inSQL := map[string]string{}
	for _, m := range pair.FindAllStringSubmatch(body, -1) {
		inSQL[m[1]] = m[2]
	}

	inGo := metric.Aliases()
	if len(inSQL) != len(inGo) {
		t.Fatalf("别名条数不一致：SQL %d 条 %v，Go %d 条 %v", len(inSQL), inSQL, len(inGo), inGo)
	}
	for alias, canonical := range inGo {
		got, ok := inSQL[alias]
		if !ok {
			t.Errorf("别名 %q 在 Go 表里有、迁移 SQL 里没有", alias)
			continue
		}
		if got != canonical {
			t.Errorf("别名 %q 收敛目标不一致：SQL %q，Go %q", alias, got, canonical)
		}
	}
}

func countMetricRows(t *testing.T, db *DB, table, metricName string) int {
	t.Helper()
	var n int
	q := `SELECT count(*) FROM ` + table + ` WHERE metric = $1`
	if err := db.Pool.QueryRow(context.Background(), q, metricName).Scan(&n); err != nil {
		t.Fatalf("统计 %s.%s 失败: %v", table, metricName, err)
	}
	return n
}
