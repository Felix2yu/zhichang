package postgres

import (
	"context"
	"testing"
)

// 集成测试（需 ZHICHANG_TEST_DSN）：锁定偏好的读写口径。
// 空结果必须是 [] 而不是 null——前端拿到 null 做 .map() 会整页崩。
func TestAppSettingsUpsertAndDelete(t *testing.T) {
	db := openSleepDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `TRUNCATE app_settings`); err != nil {
		t.Fatalf("清理 app_settings 失败: %v", err)
	}

	got, err := db.AppSettings(ctx)
	if err != nil {
		t.Fatalf("AppSettings 失败: %v", err)
	}
	if got == nil {
		t.Fatal("空结果不能是 nil slice，会被编码成 JSON null")
	}
	if len(got) != 0 {
		t.Fatalf("初始应有 0 条偏好，实际 %d", len(got))
	}

	// 未登记键名：拒，而不是静默写进一条没人读的键
	if err := db.SetAppSetting(ctx, "rogue_key", "x", false); err == nil {
		t.Fatal("未登记的键不该能写入")
	}

	if err := db.SetAppSetting(ctx, "bedtime_target", "22:30", true); err != nil {
		t.Fatalf("写入偏好失败: %v", err)
	}
	// 同键再写一次是覆盖，不是攒出两行
	if err := db.SetAppSetting(ctx, "bedtime_target", "23:00", true); err != nil {
		t.Fatalf("更新偏好失败: %v", err)
	}
	rows, err := db.AppSettings(ctx)
	if err != nil {
		t.Fatalf("AppSettings 失败: %v", err)
	}
	if len(rows) != 1 || rows[0].Value != "23:00" {
		t.Fatalf("应覆盖为一条 23:00，实际 %d 条 %+v", len(rows), rows)
	}

	// 存储层不解释值：空串能不能写由 api 层按键名决定
	// （bedtime_target 不接受空值，清除目标走 DELETE）
	if err := db.SetAppSetting(ctx, "bedtime_target", "", true); err != nil {
		t.Fatalf("写入空值失败: %v", err)
	}
	if rows, _ := db.AppSettings(ctx); len(rows) != 1 || rows[0].Value != "" {
		t.Fatalf("空值应原样存下: %+v", rows)
	}

	if err := db.DeleteAppSetting(ctx, "bedtime_target"); err != nil {
		t.Fatalf("删除偏好失败: %v", err)
	}
	// 删不存在的键是幂等成功，不是报错
	if err := db.DeleteAppSetting(ctx, "bedtime_target"); err != nil {
		t.Fatalf("重复删除不该报错: %v", err)
	}
	if rows, _ := db.AppSettings(ctx); len(rows) != 0 {
		t.Fatalf("删除后应剩 0 条，实际 %d", len(rows))
	}
}
