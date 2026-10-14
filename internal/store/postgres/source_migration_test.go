package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/migrations"
)

// sourceMigrationVersion 是负责来源名归一的迁移文件名。
const sourceMigrationVersion = "0007_source_normalize.sql"

// 真实导出里的形态：来源名用的是 NBSP 而非普通空格。
const (
	dirtySource = "子翼的Apple\u00a0Watch"
	cleanSource = "子翼的Apple Watch"
)

// historicalRowsHaveDirtySource 检查表里是否还有没归一干净的来源名。
// 直接用 NBSP 字符判定，避免复用被测实现的归一表达式（那会自我印证）。
func historicalRowsHaveDirtySource(t *testing.T, db *DB, table string) int {
	t.Helper()
	var n int
	q := `SELECT count(*) FROM ` + table + ` WHERE position(U&'\00A0' in source) > 0`
	if err := db.Pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("统计 %s 的脏来源失败: %v", table, err)
	}
	return n
}

// TestSourceNormalizeMigrationMergesHistoricalRows 在真库上跑一遍历史数据搬迁。
//
// 这条迁移同样「会动用户数据」，所以每条分支都要有断言：
//   - 归一后撞上唯一索引/约束的重复行删旧留新（否则整个迁移会被唯一约束打回，
//     启动直接失败——比不迁移更糟）；
//   - 不冲突的行改名保留（否则历史数据直接消失）；
//   - sources 表合并成一行，first/last seen 取并集、preferred 取或；
//   - 受影响的日期排进重算队列（否则界面拿到的还是归一前的天级值）。
func TestSourceNormalizeMigrationMergesHistoricalRows(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	// 样本：一对会撞车的（同 start_time/metric/value，只有来源的空白不同）
	// + 一条不撞车的（归一后改名保留）。
	insSample := func(ts, source string, value float64) {
		t.Helper()
		mustExec(t, db, `INSERT INTO samples
			(start_time, end_time, metric, source, value, unit, local_date, tz_offset_min)
			VALUES ($1::timestamptz, $1::timestamptz, 'step_count', $2, $3, 'count', '2026-10-01'::date, 480)`,
			ts, source, value)
	}
	insSample("2026-10-01 00:00:00+00", dirtySource, 100)
	insSample("2026-10-01 00:00:00+00", cleanSource, 100)
	insSample("2026-10-01 01:00:00+00", dirtySource, 200)

	// 运动：UNIQUE (name, source, start_time)
	mustExec(t, db, `INSERT INTO workouts (start_time, end_time, name, source, local_date, tz_offset_min) VALUES
		('2026-10-01 02:00:00+00','2026-10-01 03:00:00+00','Walking',$1,'2026-10-01',480),
		('2026-10-01 02:00:00+00','2026-10-01 03:00:00+00','Walking',$2,'2026-10-01',480)`,
		dirtySource, cleanSource)

	// 睡眠：UNIQUE (local_date, source, sleep_start)
	mustExec(t, db, `INSERT INTO sleep_sessions (local_date, sleep_start, sleep_end, total_sleep_hr, source, tz_offset_min) VALUES
		('2026-10-01','2026-09-30 23:00:00+00','2026-10-01 07:00:00+00',7.5,$1,480),
		('2026-10-01','2026-09-30 23:00:00+00','2026-10-01 07:00:00+00',7.5,$2,480)`,
		dirtySource, cleanSource)

	// 心电：UNIQUE (start_time, source)
	mustExec(t, db, `INSERT INTO ecg_records
		(start_time, end_time, classification, classification_code, source, local_date, tz_offset_min) VALUES
		('2026-10-01 09:00:00+00','2026-10-01 09:00:30+00','窦性心律','sinus_rhythm',$1,'2026-10-01',480),
		('2026-10-01 09:00:00+00','2026-10-01 09:00:30+00','窦性心律','sinus_rhythm',$2,'2026-10-01',480)`,
		dirtySource, cleanSource)

	// 来源清单：脏行 first_seen 更早、干净行 last_seen 更晚，合并后两者都要保住。
	mustExec(t, db, `INSERT INTO sources (source, device_class, preferred, first_seen_at, last_seen_at) VALUES
		($1,'watch',false,'2026-09-01 00:00:00+00','2026-09-10 00:00:00+00'),
		($2,'watch',true, '2026-09-05 00:00:00+00','2026-09-20 00:00:00+00')`,
		dirtySource, cleanSource)

	// 迁移在 testdb 自举时已经跑过（空库上是空操作），这里退回「未应用」再真实跑一遍。
	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = $1`, sourceMigrationVersion)
	applied, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("来源归一迁移失败: %v（唯一约束没处理干净会把整份迁移打回）", err)
	}
	if len(applied) != 1 || applied[0] != sourceMigrationVersion {
		t.Fatalf("应只补上 %s，实际 %v", sourceMigrationVersion, applied)
	}

	// —— 原始样本：撞车的一对删旧留新，不撞的改名保留 ——
	var n int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM samples WHERE metric = 'step_count'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("样本应剩 2 条（1 条删旧留新 + 1 条改名），实际 %d 条", n)
	}
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM samples WHERE source = $1`, cleanSource).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("两条样本都应收敛到干净来源，实际 %d 条", n)
	}
	// 归一后同 (start_time, metric, source, value) 只能剩一条：值相同的那对合并了
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM samples WHERE value = 100`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("值相同的重复点应删旧留新为 1 条，实际 %d 条", n)
	}

	// —— 其余事件表：都是删旧留新 ——
	for _, tbl := range []string{"workouts", "sleep_sessions", "ecg_records"} {
		var total int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&total); err != nil {
			t.Fatal(err)
		}
		if total != 1 {
			t.Fatalf("%s 应剩 1 条（删旧留新），实际 %d 条", tbl, total)
		}
		if dirty := historicalRowsHaveDirtySource(t, db, tbl); dirty != 0 {
			t.Fatalf("%s 仍残留 %d 条 NBSP 来源", tbl, dirty)
		}
	}

	// —— 来源清单：合并成一行，first/last seen 取并集，preferred 取或 ——
	var src, cls string
	var preferred bool
	var firstSeen, lastSeen string
	if err := db.Pool.QueryRow(ctx,
		`SELECT source, device_class, preferred,
		        first_seen_at::text, last_seen_at::text FROM sources`).
		Scan(&src, &cls, &preferred, &firstSeen, &lastSeen); err != nil {
		t.Fatalf("来源清单应只剩一行，实际读不到: %v", err)
	}
	if src != cleanSource {
		t.Fatalf("来源名应归一，实际 %q", src)
	}
	if cls != "watch" {
		t.Fatalf("设备类别不该被改动，实际 %q", cls)
	}
	if !preferred {
		t.Fatal("合并时 preferred 应取或（干净行是首选，合并后仍应是首选）")
	}
	if !strings.HasPrefix(firstSeen, "2026-09-01") {
		t.Fatalf("first_seen 应取更早的那个，实际 %s", firstSeen)
	}
	if !strings.HasPrefix(lastSeen, "2026-09-20") {
		t.Fatalf("last_seen 应取更晚的那个，实际 %s", lastSeen)
	}

	// —— 重算队列：受影响的 (日期×指标) 必须排进去 ——
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM rollup_queue WHERE local_date = '2026-10-01' AND metric = 'step_count'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("受影响的日期应排进重算队列，实际 %d 条", n)
	}
}

// TestSourceNormalizeMigrationSkipsCleanData 锁定空操作分支：
// 库里没有脏值时迁移必须什么都不改（可用性上这意味着升级不会无故重写全表）。
func TestSourceNormalizeMigrationSkipsCleanData(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	mustExec(t, db, `INSERT INTO samples
		(start_time, end_time, metric, source, value, unit, local_date, tz_offset_min)
		VALUES ('2026-10-01 00:00:00+00','2026-10-01 00:00:00+00','step_count',$1,100,'count','2026-10-01',480)`,
		cleanSource)
	mustExec(t, db, `INSERT INTO sources (source, device_class) VALUES ($1,'watch')`, cleanSource)

	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = $1`, sourceMigrationVersion)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("干净数据上迁移不该报错: %v", err)
	}

	var src string
	if err := db.Pool.QueryRow(ctx, `SELECT source FROM samples`).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if src != cleanSource {
		t.Fatalf("干净来源不该被改写，实际 %q", src)
	}
	var pending int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM rollup_queue`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("没有脏值时不该排重算，实际 %d 条", pending)
	}
}

// TestSourceNormalizeMatchesGoCleanSource 是 SQL 与 Go 两份归一的等价性契约。
//
// 迁移里内联了一份归一表达式，sample.CleanSource 里有一份实现。两处各写一份，
// 靠人眼同步必然漂移：漂移的后果正是这条迁移要解决的问题——历史数据与之后新入库
// 的数据再次分裂成两个来源。
func TestSourceNormalizeMatchesGoCleanSource(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	files, err := migrations.Files()
	if err != nil {
		t.Fatal(err)
	}
	var sql string
	for _, f := range files {
		if f.Version == sourceMigrationVersion {
			sql = f.SQL
		}
	}
	if sql == "" {
		t.Fatalf("找不到迁移 %s", sourceMigrationVersion)
	}

	// 迁移末尾会把函数删掉（不在库里留第二份事实来源），所以这里把函数定义
	// 单独取出来在测试库上建一次，再逐用例比对。
	const head = "CREATE OR REPLACE FUNCTION zhichang_clean_source"
	const tail = "\n$$;"
	start := strings.Index(sql, head)
	if start < 0 {
		t.Fatalf("迁移里找不到 %q", head)
	}
	end := strings.Index(sql[start:], tail)
	if end < 0 {
		t.Fatalf("迁移里找不到函数定义的结尾 %q", tail)
	}
	ddl := sql[start : start+end+len(tail)]

	var dropped bool
	drop := func() {
		if !dropped {
			dropped = true
			_, _ = db.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS zhichang_clean_source(text)`)
		}
	}
	defer drop()

	if _, err := db.Pool.Exec(ctx, ddl); err != nil {
		t.Fatalf("建函数失败: %v\n%s", err, ddl)
	}

	cases := []string{
		"",
		"子翼的Apple\u00a0Watch",
		"子翼的Apple Watch",
		"子翼的Apple\u3000Watch",
		"子翼的Apple\u2009Watch",
		"子翼的Apple\u202fWatch",
		"子翼的Apple\u2007Watch",
		"Apple\t\nWatch",
		"Apple   Watch",
		"  Apple Watch  ",
		"\u00a0子翼的 \t Apple\u3000Watch\u00a0",
		"Apple\u200bWatch",
		"Apple\u200dWatch",
		"Apple\u200cWatch",
		"Apple\u2060Watch",
		"\ufeffApple Watch",
		"Apple \u200b Watch",
		"子翼的Apple Watch|子翼的iPhone",
		"子翼的Apple Watch |子翼的iPhone",
		"血氧\u00a0| 子翼的Apple\u00a0Watch",
		"a||b",
		"|a|",
		"\u00a0\u3000",
		"\u200b\ufeff",
		"apple watch",
		"某某健康App（备用）",
		"移动心电图房颤提示软件",
		"子翼的iPhone",
	}
	for _, in := range cases {
		var got string
		if err := db.Pool.QueryRow(ctx, `SELECT zhichang_clean_source($1)`, in).Scan(&got); err != nil {
			t.Fatalf("执行 SQL 归一失败（输入 %q）: %v", in, err)
		}
		if want := sample.CleanSource(in); got != want {
			t.Errorf("SQL 与 Go 不一致：输入 %q，SQL 得 %q，Go 得 %q", in, got, want)
		}
	}
}
