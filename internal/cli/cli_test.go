package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/testdb"
)

// TestMain 起停本包的测试库；ZHICHANG_TEST_DSN 存在时直接用它。
func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

// setOpsEnv 把运维命令需要的配置通过环境变量接上：命令内部走 config.LoadForCLI()，
// 没有别的注入口，所以环境变量就是唯一的接线方式。
func setOpsEnv(t *testing.T, dsn string) {
	t.Helper()
	t.Setenv("ZHICHANG_DB_DSN", dsn)
	// 故意不设 ingest token：LoadForCLI 只放过「缺 ZHICHANG_INGEST_TOKEN」这一种错误，
	// 运维命令因此必须能在没有写入 token 的机器上跑起来。这条断言锁的就是这个口径。
	t.Setenv("ZHICHANG_INGEST_TOKEN", "")
	t.Setenv("ZHICHANG_HOME_TZ", "Asia/Shanghai")
}

// pool 直连测试库，只用于断言数据库里真实发生了什么（不看命令的返回值）。
func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	p, err := pgxpool.New(ctx, testdb.DSN(t))
	if err != nil {
		t.Skipf("数据库不可用: %v", err)
	}
	if err := p.Ping(ctx); err != nil {
		t.Skipf("数据库不可用: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func execSQL(t *testing.T, p *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, err := p.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("准备测试数据失败: %v（SQL: %s）", err, q)
	}
}

func countSQL(t *testing.T, p *pgxpool.Pool, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := p.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("查询失败: %v（SQL: %s）", err, q)
	}
	return n
}

// capturedRun 在捕获 stdout 的同时执行一次命令。
//
// 生产实现是硬编码的 fmt.Println 到 os.Stdout；测试在这里把 os.Stdout 换成管道，
// 这样既能断言运维输出的口径，又不必为了让测试可断言而改动生产代码的打印方式。
func capturedRun(t *testing.T, run func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("建管道失败: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := run()
	os.Stdout = old
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out), runErr
}

func migrationsApplied(t *testing.T, p *pgxpool.Pool) []string {
	t.Helper()
	rows, err := p.Query(context.Background(), `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("查询迁移记录失败: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("扫描迁移记录失败: %v", err)
		}
		got = append(got, v)
	}
	return got
}

func tableExists(t *testing.T, p *pgxpool.Pool, name string) bool {
	t.Helper()
	return countSQL(t, p, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name=$1`, name) == 1
}

// TestMigrateIsIdempotent 锁住迁移的两条运维口径：
// 第一次真的补上未应用的迁移，第二次既不改数据库也不报错。
func TestMigrateIsIdempotent(t *testing.T) {
	setOpsEnv(t, testdb.DSN(t))
	p := pool(t)

	// 回到「0003 尚未应用」的起点：同一个测试库会被多个测试复用，
	// 不显式回滚的话「第一次真的建表」这条断言就可能在已经迁移过的库上失去意义。
	execSQL(t, p, `DROP TABLE IF EXISTS goals`)
	execSQL(t, p, `DELETE FROM schema_migrations WHERE version = '0003_goals.sql'`)

	before := migrationsApplied(t, p)
	out1, err := capturedRun(t, func() error { return Migrate(nil) })
	if err != nil {
		t.Fatalf("第一次 migrate 失败: %v（输出: %s）", err, out1)
	}
	if strings.Contains(out1, "迁移已是最新") {
		t.Errorf("明明有未应用的迁移，却说已是最新: %q", out1)
	}
	if !strings.Contains(out1, "0003_goals.sql") {
		t.Errorf("输出应当说清楚应用了哪个迁移文件: %q", out1)
	}
	if !tableExists(t, p, "goals") {
		t.Error("migrate 之后 goals 表必须存在")
	}
	after1 := migrationsApplied(t, p)
	if reflect.DeepEqual(before, after1) {
		t.Errorf("迁移记录应当新增 0003，实际前后相同: %v", after1)
	}

	// 留一行数据：第二次 migrate 若把表重建，这行会消失。
	execSQL(t, p, `INSERT INTO goals (metric, target) VALUES ('step_count', 8000)`)

	out2, err := capturedRun(t, func() error { return Migrate(nil) })
	if err != nil {
		t.Fatalf("第二次 migrate 不该报错（幂等是运维命令的底线）: %v（输出: %s）", err, out2)
	}
	if !strings.Contains(out2, "迁移已是最新") {
		t.Errorf("无迁移可应用时应明确说明: %q", out2)
	}
	if after2 := migrationsApplied(t, p); !reflect.DeepEqual(after1, after2) {
		t.Errorf("第二次 migrate 重复登记了迁移: %v -> %v", after1, after2)
	}
	if got := countSQL(t, p, `SELECT count(*) FROM goals`); got != 1 {
		t.Errorf("第二次 migrate 不该动已有数据，goals 行数=%d", got)
	}
}

// TestDoctorReportsEmptyDB 锁 doctor 在空库上的输出：空库不是错误，必须照常报告状态。
func TestDoctorReportsEmptyDB(t *testing.T) {
	setOpsEnv(t, testdb.DSN(t))
	p := pool(t)
	execSQL(t, p, `TRUNCATE metric_defs`)
	execSQL(t, p, `TRUNCATE samples`)
	execSQL(t, p, `TRUNCATE ingest_runs`)

	out, err := capturedRun(t, func() error { return Doctor(nil) })
	if err != nil {
		t.Fatalf("空库上 doctor 失败: %v（输出: %s）", err, out)
	}
	for _, want := range []string{"PostgreSQL:", "TimescaleDB:", "Hypertables:"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor 输出缺少 %q 这一行:\n%s", want, out)
		}
	}
	// 表数量必须真的报出来（非 0），否则说明库连错了。
	if !strings.Contains(out, "表数量: ") || strings.Contains(out, "表数量: 0 ") {
		t.Errorf("doctor 应当报出 public 下的表数量:\n%s", out)
	}
	if !strings.Contains(out, "指标定义: 0  样本数: 0") {
		t.Errorf("空库应报 0 指标 0 样本:\n%s", out)
	}
	if !strings.Contains(out, "最近同步: 尚无记录") {
		t.Errorf("ingest_runs 为空时应报「尚无记录」:\n%s", out)
	}
}

// TestDoctorReportsPopulatedDB 锁 doctor 在「有数据」时的读数真的来自库里的事实。
func TestDoctorReportsPopulatedDB(t *testing.T) {
	setOpsEnv(t, testdb.DSN(t))
	p := pool(t)
	execSQL(t, p, `TRUNCATE metric_defs`)
	execSQL(t, p, `TRUNCATE samples`)
	execSQL(t, p, `TRUNCATE ingest_runs`)

	if _, err := capturedRun(t, func() error { return SeedMetrics(nil) }); err != nil {
		t.Fatalf("seed-metrics 失败: %v", err)
	}
	defs := len(metric.Defaults())
	execSQL(t, p, `INSERT INTO samples (start_time, end_time, metric, source, value, unit, local_date, tz_offset_min)
		VALUES ('2026-10-13T00:00:00Z', '2026-10-13T00:00:00Z', 'step_count', 'iPhone', 1234, 'count', '2026-10-13', 480)`)
	receivedAt := time.Date(2026, 10, 13, 1, 2, 3, 0, time.UTC)
	execSQL(t, p, `INSERT INTO ingest_runs (transport, received_at, sample_count) VALUES ('hae', $1, 1)`, receivedAt)

	out, err := capturedRun(t, func() error { return Doctor(nil) })
	if err != nil {
		t.Fatalf("有数据的库上 doctor 失败: %v（输出: %s）", err, out)
	}
	wantCounts := fmt.Sprintf("指标定义: %d  样本数: 1", defs)
	if !strings.Contains(out, wantCounts) {
		t.Errorf("doctor 报的指标/样本数应与库里一致，期望 %q，实际:\n%s", wantCounts, out)
	}
	if !strings.Contains(out, receivedAt.Local().Format(time.RFC3339)) {
		t.Errorf("最近同步应打印那条 ingest_runs 的时间（按进程本地时区格式化）:\n%s", out)
	}
	if strings.Contains(out, "尚无记录") {
		t.Errorf("已经有同步记录时不该说「尚无记录」:\n%s", out)
	}
}

// TestSeedMetricsFillsDictionaryWithoutOverwriting 锁 seed-metrics 的口径：
// 只补缺失的，已存在的一律不动，重复跑不翻倍。
func TestSeedMetricsFillsDictionaryWithoutOverwriting(t *testing.T) {
	setOpsEnv(t, testdb.DSN(t))
	p := pool(t)
	execSQL(t, p, `TRUNCATE metric_defs`)
	total := int64(len(metric.Defaults()))

	out, err := capturedRun(t, func() error { return SeedMetrics(nil) })
	if err != nil {
		t.Fatalf("首次 seed-metrics 失败: %v（输出: %s）", err, out)
	}
	if !strings.Contains(out, fmt.Sprintf("新增指标定义: %d", total)) {
		t.Errorf("空字典时应补齐全部 %d 条: %q", total, out)
	}
	if got := countSQL(t, p, `SELECT count(*) FROM metric_defs`); got != total {
		t.Errorf("metric_defs 行数应为 %d，实际 %d", total, got)
	}

	// 第二次：一行都不该新增。
	out, err = capturedRun(t, func() error { return SeedMetrics(nil) })
	if err != nil {
		t.Fatalf("重复 seed-metrics 失败: %v（输出: %s）", err, out)
	}
	if !strings.Contains(out, "新增指标定义: 0") {
		t.Errorf("重复跑应报 0 新增: %q", out)
	}
	if got := countSQL(t, p, `SELECT count(*) FROM metric_defs`); got != total {
		t.Errorf("重复 seed-metrics 让字典翻倍了: %d", got)
	}

	// 改过的一条 + 缺的两条：只补缺的那两条，改过的口径不能被覆盖回去。
	var changed string
	if err := p.QueryRow(context.Background(), `SELECT metric FROM metric_defs ORDER BY metric LIMIT 1`).Scan(&changed); err != nil {
		t.Fatalf("取一条指标失败: %v", err)
	}
	execSQL(t, p, `UPDATE metric_defs SET display_name_zh = '人工改名' WHERE metric = $1`, changed)
	var missing []string
	rows, err := p.Query(context.Background(), `SELECT metric FROM metric_defs ORDER BY metric DESC LIMIT 2`)
	if err != nil {
		t.Fatalf("取待删指标失败: %v", err)
	}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			t.Fatalf("扫描指标名失败: %v", err)
		}
		missing = append(missing, m)
	}
	rows.Close()
	execSQL(t, p, `DELETE FROM metric_defs WHERE metric = ANY($1)`, missing)

	out, err = capturedRun(t, func() error { return SeedMetrics(nil) })
	if err != nil {
		t.Fatalf("补齐缺失指标失败: %v（输出: %s）", err, out)
	}
	if !strings.Contains(out, fmt.Sprintf("新增指标定义: %d", len(missing))) {
		t.Errorf("应只补回 %d 条缺失: %q", len(missing), out)
	}
	if got := countSQL(t, p, `SELECT count(*) FROM metric_defs`); got != total {
		t.Errorf("补齐后行数应回到 %d，实际 %d", total, got)
	}
	if got := countSQL(t, p, `SELECT count(*) FROM metric_defs WHERE metric = $1 AND display_name_zh = '人工改名'`, changed); got != 1 {
		t.Errorf("seed-metrics 不该覆盖已存在的指标定义（%s 的人工改名被冲掉了）", changed)
	}
}

// TestCommandErrorsAreHumanReadable 锁失败路径：连不上库、DSN 写错、时区非法时
// 运维命令必须返回人话错误，而不是 panic 或静默成功。
func TestCommandErrorsAreHumanReadable(t *testing.T) {
	dsn := testdb.DSN(t)
	commands := map[string]func([]string) error{
		"migrate":      Migrate,
		"doctor":       Doctor,
		"seed-metrics": SeedMetrics,
	}
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want string // 错误里必须出现的关键字
	}{
		{
			name: "DSN 端口非法",
			env:  map[string]string{"ZHICHANG_DB_DSN": "postgres://postgres:postgres@127.0.0.1:99999/zhichang?sslmode=disable"},
			want: "解析 DB_DSN 失败",
		},
		{
			name: "库不存在",
			env:  map[string]string{"ZHICHANG_DB_DSN": strings.Replace(dsn, "zhichang_test", "zhichang_does_not_exist", 1)},
			want: "连接数据库失败",
		},
		{
			name: "端口没人监听",
			env:  map[string]string{"ZHICHANG_DB_DSN": "postgres://postgres:postgres@127.0.0.1:1/zhichang?sslmode=disable"},
			want: "连接数据库失败",
		},
		{
			name: "HOME_TZ 非法",
			env:  map[string]string{"ZHICHANG_DB_DSN": dsn, "ZHICHANG_HOME_TZ": "Mars/Olympus_Mons"},
			want: "ZHICHANG_HOME_TZ 非法",
		},
	}

	for _, name := range sortedKeys(commands) {
		for _, c := range cases {
			t.Run(name+"/"+c.name, func(t *testing.T) {
				for k, v := range c.env {
					t.Setenv(k, v)
				}
				// 这些用例一律不设 ingest token，顺带确认缺 token 不是错误来源。
				t.Setenv("ZHICHANG_INGEST_TOKEN", "")
				_, err := capturedRun(t, func() error { return commands[name](c.args) })
				if err == nil {
					t.Fatalf("%s 在配置坏了却返回 nil，运维无从判断", name)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("错误应当说清问题所在，期望包含 %q，实际: %v", c.want, err)
				}
			})
		}
	}
}

// TestMissingDBDSNFallsBackToDefault 验证不设 ZHICHANG_DB_DSN 时用的是 config 里的默认串。
// 只有本机 5432 没人监听时才跑：默认串指向 postgres@127.0.0.1:5432/zhichang，
// 若开发者真在那儿跑着一个库，这个测试就会去动那个库。
func TestMissingDBDSNFallsBackToDefault(t *testing.T) {
	if c, err := net.DialTimeout("tcp", "127.0.0.1:5432", 300*time.Millisecond); err == nil {
		c.Close()
		t.Skip("本机 127.0.0.1:5432 有库在跑，不拿默认串去连它，以免碰到真实数据")
	}
	t.Setenv("ZHICHANG_DB_DSN", "")
	t.Setenv("ZHICHANG_INGEST_TOKEN", "")

	_, err := capturedRun(t, func() error { return Doctor(nil) })
	if err == nil {
		t.Fatal("默认串指向的库不存在，doctor 必须报错")
	}
	// 报错里带上地址，运维才能一眼看出连的是默认库而不是自己以为的那个。
	if !strings.Contains(err.Error(), "5432") || !strings.Contains(err.Error(), "zhichang") {
		t.Errorf("错误应暴露默认 DSN 的目标地址与实际库名: %v", err)
	}
}

// TestDSNFlagOverridesEnv 锁 -dsn 覆盖环境变量的口径。
func TestDSNFlagOverridesEnv(t *testing.T) {
	dsn := testdb.DSN(t)
	bad := "postgres://postgres:postgres@127.0.0.1:1/zhichang?sslmode=disable"

	// 环境变量是坏串，-dsn 给好串 → 应当成功（说明 -dsn 真的写进了配置读取的那条路）。
	t.Setenv("ZHICHANG_DB_DSN", bad)
	t.Setenv("ZHICHANG_INGEST_TOKEN", "")
	out, err := capturedRun(t, func() error { return Doctor([]string{"-dsn", dsn}) })
	if err != nil {
		t.Fatalf("-dsn 没能覆盖环境变量: %v（输出: %s）", err, out)
	}
	if !strings.Contains(out, "PostgreSQL:") {
		t.Errorf("-dsn 覆盖后 doctor 仍应打印库信息:\n%s", out)
	}

	// 反过来：环境变量是好串，-dsn 给坏串 → 必须失败，且 -dsn 优先。
	t.Setenv("ZHICHANG_DB_DSN", dsn)
	if _, err := capturedRun(t, func() error { return Migrate([]string{"-dsn", bad}) }); err == nil {
		t.Fatal("-dsn 指定的坏连接串被忽略了，migrate 竟然成功")
	}
}

// TestOpsCommandsRejectUnknownFlag 运维命令的 -dsn 之外不认任何参数：
// 打错的参数必须当场报错，否则会被静默忽略，运维以为改了库其实没改。
func TestOpsCommandsRejectUnknownFlag(t *testing.T) {
	setOpsEnv(t, testdb.DSN(t))
	for _, c := range []struct {
		name string
		run  func([]string) error
	}{
		{"migrate", Migrate},
		{"doctor", Doctor},
		{"seed-metrics", SeedMetrics},
	} {
		err := c.run([]string{"-database", "whatever"})
		if err == nil {
			t.Fatalf("%s 接受了未知参数却没报错", c.name)
		}
		if !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Errorf("%s 的错误应当指向具体参数: %v", c.name, err)
		}
	}
}

// TestCommandsOnHalfMigratedDatabase 用一个手工搭出来的「半拉子库」锁另外几条失败分支：
// 表缺了、或者迁移记录的结构不对时，命令必须报错并点出是哪张表，而不是打印一半状态就当成功。
func TestCommandsOnHalfMigratedDatabase(t *testing.T) {
	admin := pool(t)
	const name = "zhichang_half_cli"
	execSQL(t, admin, `DROP DATABASE IF EXISTS `+name)
	execSQL(t, admin, `CREATE DATABASE `+name)
	// 先登记删库，再登记关连接：t.Cleanup 是后进先出，保证 DROP DATABASE 时已无人连着它。
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
	})
	dsn := strings.Replace(testdb.DSN(t), "zhichang_test", name, 1)

	ctx := context.Background()
	fixture, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("数据库不可用: %v", err)
	}
	t.Cleanup(fixture.Close)
	// metric_defs 只建一列：doctor 数得出行，seed-metrics 写不进去。
	execSQL(t, fixture, `CREATE TABLE metric_defs (metric text PRIMARY KEY)`)
	// schema_migrations 存成整数列：真实迁移登记版本时会在类型上不匹配。
	execSQL(t, fixture, `CREATE TABLE schema_migrations (version integer)`)

	setOpsEnv(t, dsn)

	t.Run("doctor 缺表", func(t *testing.T) {
		out, err := capturedRun(t, func() error { return Doctor(nil) })
		if err == nil {
			t.Fatalf("samples 表不存在时 doctor 不该成功: %s", out)
		}
		if !strings.Contains(err.Error(), "samples") {
			t.Errorf("错误应当点出缺的是 samples: %v", err)
		}
	})

	t.Run("doctor 缺同步记录表", func(t *testing.T) {
		execSQL(t, fixture, `CREATE TABLE samples (start_time timestamptz)`)
		out, err := capturedRun(t, func() error { return Doctor(nil) })
		if err == nil {
			t.Fatalf("ingest_runs 不存在时 doctor 不该成功: %s", out)
		}
		if !strings.Contains(err.Error(), "ingest_runs") {
			t.Errorf("错误应当点出缺的是 ingest_runs: %v", err)
		}
	})

	t.Run("seed-metrics 写不进去", func(t *testing.T) {
		out, err := capturedRun(t, func() error { return SeedMetrics(nil) })
		if err == nil {
			t.Fatalf("metric_defs 结构不对时 seed-metrics 不该成功: %s", out)
		}
		if !strings.Contains(err.Error(), "metric_defs") {
			t.Errorf("错误应当点出是 metric_defs 的问题: %v", err)
		}
	})

	t.Run("migrate 迁移记录结构不对", func(t *testing.T) {
		out, err := capturedRun(t, func() error { return Migrate(nil) })
		if err == nil {
			t.Fatal("schema_migrations 结构不对时 migrate 不该成功")
		}
		if strings.Contains(out, "已应用") {
			t.Errorf("已经失败了却还在报告「已应用」: %s", out)
		}
	})
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestCommandsOnUnmigratedDatabase 造一个「库真的存在但一张表都还没有」的环境：
// 这是刚建好库、还没跑 migrate 时的真实故障形态，命令必须报错并指明缺什么，
// 而不是 panic、也不要在 MCP 那条路上悄悄开一个没有指标字典的空壳服务。
func TestCommandsOnUnmigratedDatabase(t *testing.T) {
	admin := pool(t)
	const name = "zhichang_unmigrated_cli"
	execSQL(t, admin, `DROP DATABASE IF EXISTS `+name)
	execSQL(t, admin, `CREATE DATABASE `+name)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
	})
	setOpsEnv(t, strings.Replace(testdb.DSN(t), "zhichang_test", name, 1))

	t.Run("doctor", func(t *testing.T) {
		out, err := capturedRun(t, func() error { return Doctor(nil) })
		if err == nil {
			t.Fatalf("没迁移过的库上 doctor 不该成功: %s", out)
		}
		if !strings.Contains(err.Error(), "metric_defs") {
			t.Errorf("错误应当指出缺哪张表: %v", err)
		}
	})

	t.Run("mcp", func(t *testing.T) {
		// stdin 立刻给 EOF：连接与字典加载在建立会话之前，错误应当原样返回。
		_, err := capturedRun(t, func() error { return MCP(nil) })
		if err == nil {
			t.Fatal("没有指标字典的库上 MCP 不该启动成功")
		}
		if !strings.Contains(err.Error(), "metric_defs") {
			t.Errorf("错误应当指出缺哪张表: %v", err)
		}
	})
}
