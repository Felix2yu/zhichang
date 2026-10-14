package postgres

import (
	"context"
	"testing"
)

// 集成测试（需 ZHICHANG_TEST_DSN）：锁定目标读写的口径。
// 写接口是全服务唯一的写口子，所以「未登记的指标不能设目标」「空结果必须是 []」
// 这两条要在库里锁住，不能只靠 api 层的判断。
func TestGoalsUpsertAndRejectUnknown(t *testing.T) {
	db := openSleepDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `TRUNCATE goals`); err != nil {
		t.Fatalf("清理 goals 失败: %v", err)
	}

	got, err := db.Goals(ctx)
	if err != nil {
		t.Fatalf("Goals 失败: %v", err)
	}
	if got == nil {
		t.Fatal("空结果不能是 nil slice，会被编码成 JSON null")
	}
	if len(got) != 0 {
		t.Fatalf("初始应有 0 条目标，实际 %d", len(got))
	}

	// 未登记指标：拒，而不是静默写进去一条页面上永远看不到的目标
	if err := db.SetGoal(ctx, "not_a_metric", 7, false); err == nil {
		t.Fatal("未登记的指标不该能设目标")
	}
	// 目标值必须为正
	if err := db.SetGoal(ctx, "sleep_analysis", 0, true); err == nil {
		t.Fatal("0 目标不该被接受")
	}

	if err := db.SetGoal(ctx, "sleep_analysis", 7.5, true); err != nil {
		t.Fatalf("写入目标失败: %v", err)
	}
	// 同指标再写一次是覆盖，不是攒出两行
	if err := db.SetGoal(ctx, "sleep_analysis", 8, true); err != nil {
		t.Fatalf("更新目标失败: %v", err)
	}
	rows, err := db.Goals(ctx)
	if err != nil {
		t.Fatalf("Goals 失败: %v", err)
	}
	if len(rows) != 1 || rows[0].Target != 8 {
		t.Fatalf("应覆盖为一条 8，实际 %d 条 %+v", len(rows), rows)
	}

	if err := db.DeleteGoal(ctx, "sleep_analysis"); err != nil {
		t.Fatalf("删除目标失败: %v", err)
	}
	// 删不存在的指标是幂等成功，不是报错
	if err := db.DeleteGoal(ctx, "sleep_analysis"); err != nil {
		t.Fatalf("重复删除不该报错: %v", err)
	}
	if rows, _ := db.Goals(ctx); len(rows) != 0 {
		t.Fatalf("删除后应剩 0 条，实际 %d", len(rows))
	}
}
