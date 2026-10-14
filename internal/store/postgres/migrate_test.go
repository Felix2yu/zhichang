package postgres

import (
	"context"
	"strings"
	"testing"
)

// migrate_test.go 锁 Migrate / execScript 的口径：迁移是「按序、单事务、可重入」的，
// 坏脚本必须整份回滚而不是留下半成品 schema。

func tableExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	var ok bool
	err := db.Pool.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&ok)
	if err != nil {
		t.Fatalf("探测表 %s 失败: %v", name, err)
	}
	return ok
}

// TestMigrateIsIdempotent 锁定重复启动的口径：
//   - 第一次跑要真的把 testdb 故意留下的 0003_goals.sql 补上（applied 里能看到它）；
//   - 第二次跑 applied 必须是空——服务每次重启都重放一遍 DDL 会让运维以为迁移在做事；
//   - applied 的顺序就是文件名顺序（后面的迁移能看到前面的成果）。
func TestMigrateIsIdempotent(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	// testdb 建库时跳过 0003，openRollupDB 里的 Migrate 已经补上；
	// 这里退回「未应用」状态，才能观察到 Migrate 真实的应用过程。
	mustExec(t, db, `DROP TABLE IF EXISTS goals`)
	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = '0003_goals.sql'`)

	applied, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("补迁移失败: %v", err)
	}
	if len(applied) != 1 || applied[0] != "0003_goals.sql" {
		t.Fatalf("应只补上 0003_goals.sql，实际 %v", applied)
	}
	if !tableExists(t, db, "goals") {
		t.Fatal("迁移完 goals 表必须在")
	}

	// 第二次：什么都没做
	again, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("重复迁移不该报错: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("重复迁移 applied 应为空，实际 %v", again)
	}

	// goals 表上的 CHECK (target > 0) 是真的：store 层的校验之外，库也拦一道
	if err := db.SetGoal(ctx, "step_count", 7, true); err != nil {
		t.Fatalf("写入目标失败: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE goals SET target = 0 WHERE metric = 'step_count'`); err == nil {
		t.Fatal("goals.target 的 CHECK (target > 0) 约束没生效")
	} else if !strings.Contains(err.Error(), "goals_target_check") {
		t.Fatalf("应被 CHECK 约束拦下，实际: %v", err)
	}
	// 未登记指标与 target<=0 在 store 层就拒掉，绝不下到库
	if err := db.SetGoal(ctx, "step_count", -1, true); err == nil {
		t.Fatal("target <= 0 必须被 store 层拒绝")
	} else if !strings.Contains(err.Error(), "目标必须大于 0") {
		t.Fatalf("拒绝原因口径不对: %v", err)
	}
	if err := db.SetGoal(ctx, "who_knows", 5, false); err == nil || !strings.Contains(err.Error(), "指标未登记") {
		t.Fatalf("未登记指标必须回 ErrUnknownGoalMetric，实际: %v", err)
	}
	mustExec(t, db, `TRUNCATE goals`)
}

// TestExecScriptRollsBackWholeScript 锁定「一份迁移一个事务」：
// 脚本里第 2 条语句炸了，第 1 条已经建好的表也必须一起消失，
// 否则下次重跑会撞上「表已存在」，把一个可恢复的失败变成永久坏库。
func TestExecScriptRollsBackWholeScript(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	err := db.execScript(ctx, `CREATE TABLE exec_script_probe (id int); SELECT 1/0;`)
	if err == nil {
		t.Fatal("坏脚本必须报错")
	}
	if tableExists(t, db, "exec_script_probe") {
		t.Fatal("脚本失败后必须整体回滚，不能留下半张表")
	}

	// 失败过的连接要还能用（事务状态被 ROLLBACK 干净了）
	var one int
	if err := db.Pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("回滚后连接不可用: %v", err)
	}

	// 好脚本：多语句、DDL + 数据都要落
	if err := db.execScript(ctx, `CREATE TABLE exec_script_probe (id int);
		INSERT INTO exec_script_probe VALUES (1), (2);`); err != nil {
		t.Fatalf("正常脚本失败: %v", err)
	}
	var cnt int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM exec_script_probe`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 2 {
		t.Fatalf("多语句脚本要整份执行，实际 %d 行", cnt)
	}
	mustExec(t, db, `DROP TABLE exec_script_probe`)
}

// TestExecScriptCommitFailure 覆盖「语句都成功、提交才失败」这条路：
// INITIALLY DEFERRED 的外键在 COMMIT 时才校验，脚本执行返回错误后
// 表必须不存在，并且连接要还能服务后续查询（不能把这个连接留在事务里）。
func TestExecScriptCommitFailure(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	err := db.execScript(ctx, `
		CREATE TABLE probe_parent (id int PRIMARY KEY);
		CREATE TABLE probe_child (id int REFERENCES probe_parent(id) DEFERRABLE INITIALLY DEFERRED);
		INSERT INTO probe_child VALUES (1);`)
	if err == nil {
		t.Fatal("延迟外键在提交时必须失败")
	}
	if !strings.Contains(err.Error(), "foreign key constraint") && !strings.Contains(err.Error(), "probe_child") {
		t.Fatalf("报错要能看出是外键问题: %v", err)
	}
	if tableExists(t, db, "probe_child") || tableExists(t, db, "probe_parent") {
		t.Fatal("提交失败的脚本必须整体不留下任何对象")
	}
	var one int
	if err := db.Pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("提交失败后连接不可复用: %v", err)
	}
}

// TestMigrateFailureKeepsErrorContext 覆盖迁移的前置步骤失败：
// schema_migrations 建不出来时错误要带「创建 schema_migrations 失败」，
// 这样 doctor 才知道是权限/库的问题而不是脚本的问题。
func TestMigrateFailureKeepsErrorContext(t *testing.T) {
	db := openMissingSchemaDB(t)
	applied, err := db.Migrate(context.Background())
	if err == nil {
		t.Fatal("库不可用时 Migrate 必须报错")
	}
	if len(applied) != 0 {
		t.Fatalf("一步都没做时 applied 必须是空，实际 %v", applied)
	}
	if !strings.Contains(err.Error(), "创建 schema_migrations 失败") {
		t.Fatalf("报错口径不对: %v", err)
	}
}

// TestMigrateStopsAtFirstFailingScript 在一个干净的空 schema 上跑真实迁移。
// 本机是否装了 timescaledb 决定 0001 的第一句能不能过，两种情况都要有明确断言：
//   - 没装（测试库的常态）：必须报「执行迁移 0001_init.sql 失败」并带上版本号，
//     applied 保持为空，且该 schema 里什么都不留下（脚本级回滚），下次重试仍从 0001 开始；
//   - 装了：整个 schema 必须被干净地建出来，applied 报出全部三份脚本。
func TestMigrateStopsAtFirstFailingScript(t *testing.T) {
	provision := openRollupDB(t)
	ctx := context.Background()
	mustExec(t, provision, `CREATE SCHEMA IF NOT EXISTS probe_migrate_schema`)
	t.Cleanup(func() {
		_, _ = provision.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS probe_migrate_schema CASCADE`)
	})

	var available int
	if err := provision.Pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_available_extensions WHERE name = 'timescaledb'`).Scan(&available); err != nil {
		t.Fatal(err)
	}

	db := openSearchPathDB(t, "probe_migrate_schema")
	applied, err := db.Migrate(ctx)

	if available == 0 {
		if err == nil {
			t.Fatal("没有 timescaledb 扩展时 0001 必须失败，而不是静默建出半套 schema")
		}
		if !strings.Contains(err.Error(), "执行迁移 0001_init.sql 失败") {
			t.Fatalf("报错要带失败的脚本名: %v", err)
		}
		if len(applied) != 0 {
			t.Fatalf("第一份脚本就失败时 applied 必须是空，实际 %v", applied)
		}
		var created bool
		if err := provision.Pool.QueryRow(ctx,
			`SELECT to_regclass('probe_migrate_schema.metric_defs') IS NOT NULL`).Scan(&created); err != nil {
			t.Fatal(err)
		}
		if created {
			t.Fatal("失败的那份迁移必须整体回滚，不能留下半套表")
		}
		return
	}

	// 装了扩展：干净 schema 应该一次建齐，applied 报出全部脚本
	if err != nil {
		t.Fatalf("可用扩展下 Migrate 失败: %v", err)
	}
	if len(applied) != 3 {
		t.Fatalf("三份迁移都应被应用，实际 %v", applied)
	}
}
