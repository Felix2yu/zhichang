package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// sleepInBedMigrationVersion 是「卧床时长恒 0 时按起止时刻回填」的历史数据搬迁。
const sleepInBedMigrationVersion = "0015_sleep_in_bed_backfill.sql"

// 摄入侧已经改成「0 或空缺时按起止差值补算」，但 sleep_sessions 是 ON CONFLICT DO NOTHING：
// 重放同一份导出不会更新已有行，历史行会一直停在 0 上。0 不是无害的缺省值——
// 睡眠评分拿它当分母，那一项直接归 0。
//
// 这里逐行比对 SQL 与 Go 两套判据：两处各写一份、靠人眼同步必然漂移，
// 漂移的后果正是这条迁移要修掉的东西又分裂成两套数。
func TestSleepInBedBackfillMatchesGoRule(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	const src = "示例的Apple Watch"
	ptr := func(v float64) *float64 { return &v }
	// 钟点写成「相对 2026-05-03 00:00 (+08) 的小数小时」，超过 24 即跨到次日；
	// 空串表示该列为 NULL。
	type row struct {
		day     string
		inBedHr any
		start   string
		end     string
		want    *float64
	}
	base := time.Date(2026, 5, 3, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	at := func(spec string) any {
		if spec == "" {
			return nil
		}
		var h, m int
		if _, err := fmt.Sscanf(spec, "%d:%d", &h, &m); err != nil {
			t.Fatalf("用例钟点写错了: %q", spec)
		}
		return base.Add(time.Duration((float64(h) + float64(m)/60) * float64(time.Hour)))
	}
	rows := []row{
		{"2026-05-03", 0.0, "23:10", "31:40", ptr(8.5)},   // 上游给 0 → 按起止补算
		{"2026-05-04", nil, "23:00", "31:00", ptr(8.0)},   // 该列本就 NULL → 补算
		{"2026-05-05", 8.25, "23:00", "31:00", ptr(8.25)}, // 有效值不动它
		{"2026-05-06", 0.0, "", "31:00", ptr(0.0)},        // 缺起点 → 不猜
		{"2026-05-07", 0.0, "23:00", "", ptr(0.0)},        // 缺终点 → 不猜
		{"2026-05-08", 0.0, "31:00", "23:00", ptr(0.0)},   // 终点早于起点 → 时刻不可信
		{"2026-05-09", 0.0, "23:00", "49:00", ptr(0.0)},   // 跨度 26 小时 → 超出一晚，不写
	}
	for _, r := range rows {
		mustExec(t, db, `INSERT INTO sleep_sessions (local_date, source, total_sleep_hr, in_bed_hr, in_bed_start, in_bed_end, tz_offset_min)
			VALUES ($1,$2,7.0,$3,$4,$5,480)`, r.day, src, r.inBedHr, at(r.start), at(r.end))
	}

	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = $1`, sleepInBedMigrationVersion)
	applied, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("回填迁移失败: %v", err)
	}
	if len(applied) != 1 || applied[0] != sleepInBedMigrationVersion {
		t.Fatalf("应只补上 %s，实际 %v", sleepInBedMigrationVersion, applied)
	}

	check := func(why string) {
		t.Helper()
		for _, r := range rows {
			var got *float64
			if err := db.Pool.QueryRow(ctx,
				`SELECT in_bed_hr FROM sleep_sessions WHERE local_date = $1 AND source = $2`, r.day, src).Scan(&got); err != nil {
				t.Fatalf("%s：查 %s 失败 %v", why, r.day, err)
			}
			if got == nil {
				t.Fatalf("%s：%s 应为 %v，实际 NULL", why, r.day, *r.want)
			}
			if d := *got - *r.want; d > 1e-6 || d < -1e-6 {
				t.Fatalf("%s：%s 应为 %v，实际 %v", why, r.day, *r.want, *got)
			}
		}
	}
	check("回填后")

	// 幂等：回填过的行不再命中 WHERE，重放不该动任何一行。
	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = $1`, sleepInBedMigrationVersion)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("迁移重放失败: %v", err)
	}
	check("重放后")
}
