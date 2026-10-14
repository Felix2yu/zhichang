package postgres

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/Felix2yu/zhichang/internal/config"
	"github.com/Felix2yu/zhichang/internal/testdb"
)

// closedPort 占一个端口再立刻释放，保证接下来的连接一定被拒。
func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("取空闲端口失败: %v", err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}

// TestOpenBadDSN 锁定「DSN 语法错」的报错口径：必须带上「解析 DB_DSN 失败」，
// 否则运维拿到一个裸错误根本不知道该去看哪个环境变量。
func TestOpenBadDSN(t *testing.T) {
	ctx := context.Background()
	for _, dsn := range []string{
		"这不是连接串",
		"postgresql://user:pass@127.0.0.1:5432/db?sslmode=bogus",
		"postgres://user@host:不是端口/db",
		"tcp://127.0.0.1:5432",
		"postgres://user@host/db?pool_max_conns=abc",
	} {
		db, err := Open(ctx, config.Config{DBDSN: dsn})
		if err == nil {
			t.Fatalf("DSN %q 竟然连上了", dsn)
		}
		if db != nil {
			t.Fatalf("报错时必须回 nil DB，避免调用方拿着半个池子继续用：%+v", db)
		}
		if !strings.Contains(err.Error(), "解析 DB_DSN 失败") {
			t.Fatalf("DSN %q 的报错口径不对: %v", dsn, err)
		}
	}
}

// TestOpenConnectFailure 锁定「DSN 能解析但连不上」：报错口径必须与「解析失败」不同，
// 且失败的池子要被关闭。空 DSN 也归这里——pgx 把它当「用本地 socket 连默认库」，
// 语法是合法的，失败发生在连接阶段。
func TestOpenConnectFailure(t *testing.T) {
	cases := []string{
		"",
		fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%s/none?sslmode=disable", closedPort(t)),
		"postgres://postgres:postgres@127.0.0.1:1/none?sslmode=disable",
	}
	for _, dsn := range cases {
		db, err := Open(context.Background(), config.Config{DBDSN: dsn})
		if err == nil {
			if db != nil {
				db.Close()
			}
			t.Fatalf("DSN %q 竟然连上了", dsn)
		}
		if db != nil {
			db.Close()
			t.Fatal("报错时必须回 nil DB")
		}
		if !strings.Contains(err.Error(), "连接数据库失败") {
			t.Fatalf("DSN %q 的报错口径应是「连接失败」而不是「解析失败」: %v", dsn, err)
		}
	}
}

// TestCloseIsNilSafe 服务在 Open 失败的路径上仍会 defer db.Close()，
// 所以 nil 接收者与零值 DB 都不能 panic。
func TestCloseIsNilSafe(t *testing.T) {
	var nilDB *DB
	nilDB.Close() // 不能 panic
	(&DB{}).Close()
	db := openRollupDB(t)
	db.Close()
	db.Close() // 重复关闭也不能 panic
}

// TestInfoWithoutTimescale 锁定 doctor 自检的降级口径：
// 测试库是普通 PostgreSQL 18（没有 timescaledb 扩展），Info 必须
//   - 整体不报错（自检脚本要能继续往下走）；
//   - PGVersion 拿到真实版本；
//   - TSDBVersion 回落成「(未安装 timescaledb 扩展)」这句人话；
//   - hypertable 清单查不到时是空而不是错（timescaledb_information 这个 schema 根本不存在）。
func TestInfoWithoutTimescale(t *testing.T) {
	db := openRollupDB(t)
	info, err := db.Info(context.Background())
	if err != nil {
		t.Fatalf("没有 timescaledb 时 Info 也必须成功: %v", err)
	}
	if !strings.Contains(info.PGVersion, "PostgreSQL") {
		t.Fatalf("PGVersion 应来自 version()，实际 %q", info.PGVersion)
	}
	if info.TSDBVersion != "(未安装 timescaledb 扩展)" {
		t.Fatalf("TSDBVersion 回落文案不对，实际 %q", info.TSDBVersion)
	}
	if len(info.Hypertables) != 0 {
		t.Fatalf("非 timescale 库不该有 hypertable，实际 %v", info.Hypertables)
	}
	// ChunkInterval 由采集方另填，Info 不猜值
	if info.ChunkInterval != "" {
		t.Fatalf("Info 不该凭空给出 chunk 间隔，实际 %q", info.ChunkInterval)
	}
}

// TestInfoQueryFailure 覆盖 Info 在库整个不可用时的报错分支（version() 都拿不到）。
func TestInfoQueryFailure(t *testing.T) {
	db := openMissingSchemaDB(t)
	db.Pool.Close() // 池子关了：任何查询都失败
	_, err := db.Info(context.Background())
	if err == nil {
		t.Fatal("库不可用时 Info 必须报错")
	}
}

// TestInfoListsHypertables 补上 hypertable 清单那条循环：测试库不是 TimescaleDB，
// 所以手工造一个同名视图来锁口径——清单必须按表名升序（doctor 输出要可 diff），
// 且这一句查询失败时 Info 仍然整体成功（降级优先）。
func TestInfoListsHypertables(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	mustExec(t, db, `CREATE SCHEMA IF NOT EXISTS timescaledb_information`)
	mustExec(t, db, `CREATE OR REPLACE VIEW timescaledb_information.hypertables AS
		SELECT * FROM (VALUES ('samples'), ('daily_metrics')) AS t(hypertable_name)`)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS timescaledb_information CASCADE`)
	})

	info, err := db.Info(ctx)
	if err != nil {
		t.Fatalf("有 hypertable 视图时 Info 不该报错: %v", err)
	}
	if len(info.Hypertables) != 2 {
		t.Fatalf("期望 2 张 hypertable，实际 %v", info.Hypertables)
	}
	if info.Hypertables[0] != "daily_metrics" || info.Hypertables[1] != "samples" {
		t.Fatalf("清单必须按名字升序：%v", info.Hypertables)
	}
	// 扩展没装仍然是「没装」这句人话，不能因为查到 hypertable 就假装版本存在
	if info.TSDBVersion != "(未安装 timescaledb 扩展)" {
		t.Fatalf("TSDBVersion 回落文案不对：%q", info.TSDBVersion)
	}
}

// openSearchPathDB 连同一个测试库，但把 search_path 换成指定 schema。
func openSearchPathDB(t *testing.T, schema string) *DB {
	t.Helper()
	dsn := testdb.DSN(t)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN 解析失败: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	db, err := Open(context.Background(), config.Config{DBDSN: u.String()})
	if err != nil {
		t.Fatalf("连接 schema %q 失败: %v", schema, err)
	}
	t.Cleanup(db.Close)
	return db
}

// openMissingSchemaDB 连的是同一个测试库，但把 search_path 指到一个不存在的 schema：
// 连接、Ping 都正常，而任何表都「看不见」。用它廉价地打各查询的失败分支，
// 又不会破坏真实 schema（别的测试还在用同一份库）。
func openMissingSchemaDB(t *testing.T) *DB {
	t.Helper()
	return openSearchPathDB(t, "zhichang_no_such_schema")
}

// TestQueryErrorsOnMissingRelations 锁定「库坏了要报错，不能静默给空结果」：
// 每个查询在关系不可见时都必须把错误抛出来。空结果与错误在 JSON 里长得一样
// （都是 []），但前端要的是「同步失败」的提示，不是「今天没走路」。
func TestQueryErrorsOnMissingRelations(t *testing.T) {
	db := openMissingSchemaDB(t)
	ctx := context.Background()
	from, to := "2026-10-01", "2026-10-13"

	if got, err := db.DailySeries(ctx, []string{"step_count"}, from, to); err == nil || got != nil {
		t.Fatalf("DailySeries 应报错且回 nil，实际 %#v / %v", got, err)
	}
	if got, err := db.Summary(ctx, from, nil); err == nil || got != nil {
		t.Fatalf("Summary 应报错且回 nil，实际 %#v / %v", got, err)
	}
	if got, err := db.Samples(ctx, "step_count", from, to, 10); err == nil || got != nil {
		t.Fatalf("Samples 应报错且回 nil，实际 %#v / %v", got, err)
	}
	if got, err := db.SleepRange(ctx, from, to); err == nil || got != nil {
		t.Fatalf("SleepRange 应报错且回 nil，实际 %#v / %v", got, err)
	}
	if got, err := db.WorkoutRange(ctx, from, to); err == nil || got != nil {
		t.Fatalf("WorkoutRange 应报错且回 nil，实际 %#v / %v", got, err)
	}
	if got, err := db.Sources(ctx); err == nil || got != nil {
		t.Fatalf("Sources 应报错且回 nil，实际 %#v / %v", got, err)
	}
	if got, err := db.Goals(ctx); err == nil || got != nil {
		t.Fatalf("Goals 应报错且回 nil，实际 %#v / %v", got, err)
	}
	if got, err := db.SleepStagesRange(ctx, from, to); err == nil || got != nil {
		t.Fatalf("SleepStagesRange 应报错且回 nil，实际 %#v / %v", got, err)
	}
	if got, err := db.SleepStages(ctx, from); err == nil || got != nil {
		t.Fatalf("SleepStages 应报错且回 nil，实际 %#v / %v", got, err)
	}
	if got, err := db.LatestSleepStageDate(ctx); err == nil || got != "" {
		t.Fatalf("LatestSleepStageDate 应报错且回空串，实际 %q / %v", got, err)
	}
	if got, err := db.SleepNightMetrics(ctx, from, to, []string{"heart_rate"}); err == nil || got != nil {
		t.Fatalf("SleepNightMetrics 应报错且回 nil，实际 %#v / %v", got, err)
	}
	st, err := db.SyncStatusOf(ctx, 7)
	if err == nil {
		t.Fatal("SyncStatusOf 应报错")
	}
	// 契约：即使报错，三个 slice 字段也已经是非 nil 空数组，调用方可以直接编码返回
	if st.RecentRuns == nil || st.Watermarks == nil || st.MissingDays == nil {
		t.Fatalf("出错路径也要保证 JSON 里没有 null：%+v", st)
	}
	if err := db.SetGoal(ctx, "step_count", 8, true); err == nil {
		t.Fatal("表不可见时 SetGoal 必须报错")
	}
	if err := db.DeleteGoal(ctx, "step_count"); err == nil {
		t.Fatal("表不可见时 DeleteGoal 必须报错")
	}
}
