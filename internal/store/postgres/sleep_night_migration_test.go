package postgres

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/tz"
	"github.com/Felix2yu/zhichang/migrations"
)

// sleepNightMigrationVersion 是「睡眠日归属改按本夜」的历史数据搬迁。
const sleepNightMigrationVersion = "0009_sleep_night_date.sql"

// TestSleepNightDateMigrationMatchesGoNightDate 逐行比对 SQL 与 Go 两套实现。
//
// 迁移里内联了一份 18:00 判定，tz.NightDate 里有一份实现。两处各写一份、
// 靠人眼同步必然漂移，而漂移的后果正是这条迁移要解决的问题：历史行与新入库的行
// 再次分裂成两套归属，天级值一边把两晚压成一天、另一边把一晚挪到隔壁，
// 界面上看不出任何错误。
func TestSleepNightDateMigrationMatchesGoNightDate(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	// 每行给定：迁移前的旧归属（入睡时刻的日历日）、入睡本地钟点、记录自带偏移、
	// 以及 Go 口径下应归到的夜晚。覆盖分界两侧、跨月、负偏移，和没有入睡时刻的行。
	type row struct {
		old    string
		start  string // 空串表示 sleep_start IS NULL
		offMin int
		want   string
	}
	rows := []row{
		{"2026-08-27", "2026-08-27 23:39:00", 480, "2026-08-27"},
		{"2026-08-27", "2026-08-27 02:23:00", 480, "2026-08-26"},
		{"2026-09-01", "2026-09-01 00:00:00", 480, "2026-08-31"},
		{"2026-08-27", "2026-08-27 18:00:00", 480, "2026-08-27"},
		{"2026-08-27", "2026-08-27 17:59:00", 480, "2026-08-26"},
		{"2026-08-27", "2026-08-27 01:10:00", -480, "2026-08-26"},
		{"2026-08-24", "", 480, "2026-08-24"},
	}
	for _, r := range rows {
		if r.start == "" {
			mustExec(t, db, `INSERT INTO sleep_sessions (local_date, source, total_sleep_hr, tz_offset_min)
				VALUES ($1,'Apple Watch',6.0,$2)`, r.old, r.offMin)
			continue
		}
		st := parseAtLocal(r.start, r.offMin)
		mustExec(t, db, `INSERT INTO sleep_sessions (local_date, source, sleep_start, sleep_end, total_sleep_hr, tz_offset_min)
			VALUES ($1,'Apple Watch',$2,$3,6.0,$4)`, r.old, st, st.Add(6*time.Hour), r.offMin)
	}

	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = $1`, sleepNightMigrationVersion)
	applied, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("本夜归属迁移失败: %v", err)
	}
	if len(applied) != 1 || applied[0] != sleepNightMigrationVersion {
		t.Fatalf("应只补上 %s，实际 %v", sleepNightMigrationVersion, applied)
	}

	check := func(why string) {
		t.Helper()
		for _, r := range rows {
			var got string
			q := `SELECT local_date::text FROM sleep_sessions WHERE tz_offset_min = $1 AND total_sleep_hr = 6.0`
			if r.start == "" {
				q += ` AND sleep_start IS NULL`
			} else {
				q += ` AND sleep_start = $2`
			}
			var err error
			if r.start == "" {
				err = db.Pool.QueryRow(ctx, q, r.offMin).Scan(&got)
			} else {
				err = db.Pool.QueryRow(ctx, q, r.offMin, parseAtLocal(r.start, r.offMin)).Scan(&got)
			}
			if err != nil {
				t.Fatalf("%s：查 %s 失败 %v", why, r.start, err)
			}
			if got != r.want {
				t.Fatalf("%s：%s(偏移 %d) 归到 %s，Go 口径应为 %s", why, r.start, r.offMin, got, r.want)
			}
		}
	}
	check("迁移后")

	// 新旧两天都要排进重算队列：旧日期少了一晚，新日期多了一晚。
	// 不排的话界面继续显示迁移前的天级值，而迁移本身已经成功——最难查的那种错。
	var queued int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM rollup_queue WHERE metric='sleep_analysis' AND local_date IN
			('2026-08-26','2026-08-27','2026-08-31','2026-09-01','2026-08-24')`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued < 4 {
		t.Fatalf("受影响的日期只排进 %d 天，迁移前的天级值会留在库里", queued)
	}

	// 幂等：归属是 (sleep_start, tz_offset_min) 的纯函数，重放不该再动任何行。
	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = $1`, sleepNightMigrationVersion)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("迁移重放失败: %v", err)
	}
	check("重放后")
}

// 迁移里的分界钟点与 tz.NightStartHour 必须是同一个数：
// 只改 Go 侧会让新数据与历史数据按两套口径归属，且没有任何一处会报错。
func TestSleepNightMigrationUsesGoBoundaryHour(t *testing.T) {
	files, err := migrations.Files()
	if err != nil {
		t.Fatal(err)
	}
	var sql string
	for _, f := range files {
		if f.Version == sleepNightMigrationVersion {
			sql = f.SQL
		}
	}
	if sql == "" {
		t.Fatalf("找不到迁移 %s", sleepNightMigrationVersion)
	}
	if want := "EXTRACT(HOUR FROM wall) < " + strconv.Itoa(tz.NightStartHour); !strings.Contains(sql, want) {
		t.Fatalf("迁移里找不到 %q：分界钟点改了必须两处一起改", want)
	}
}

// parseAtLocal 按记录自带偏移把「本地钟点」还原成 UTC 时刻。
func parseAtLocal(local string, offMin int) time.Time {
	wall, err := time.Parse("2006-01-02 15:04:05", local)
	if err != nil {
		panic(err)
	}
	return wall.Add(-time.Duration(offMin) * time.Minute).UTC()
}
