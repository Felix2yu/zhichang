package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/ecg"
)

const ecgWordingMigrationVersion = "0014_ecg_inconclusive_wording.sql"

// TestECGInconclusiveWordingMigrationMatchesClassifier 逐行比对：
// 迁移搬完之后，每行的 classification_code 必须等于 Go 分类器对原文的判定。
//
// 只改 Go 侧的话，历史行会与新入库的行分裂成两组——按判定统计心电时，
// 那几条「未定论」会凭空消失在 unknown 里，而 unknown 在界面上是兜底展示原文，
// 看上去像「上游没给结论」，实际是我们不认识那个词。
func TestECGInconclusiveWordingMigrationMatchesClassifier(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	// 每行：上游原文 + 迁移前库里存的码（模拟修复前的落库结果）
	type row struct {
		raw    string
		before string
	}
	rows := []row{
		{"未定论", "unknown"},
		{"未定論", "unknown"},
		{"无法定论", "unknown"},
		{"记录不佳，结论不明", "inconclusive_poor_recording"},
		{"窦性心律", "sinus_rhythm"},
		{"完全没见过的说法", "unknown"},
	}
	for i, r := range rows {
		at := time.Date(2026, 3, 1, 12, 0, i, 0, time.UTC)
		mustExec(t, db, `INSERT INTO ecg_records (start_time, end_time, classification, classification_code, source, local_date)
			VALUES ($1,$1,$2,$3,'手表','2026-03-01')`, at, r.raw, r.before)
	}

	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = $1`, ecgWordingMigrationVersion)
	applied, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("判定词表迁移失败: %v", err)
	}
	if len(applied) != 1 || applied[0] != ecgWordingMigrationVersion {
		t.Fatalf("应只补上 %s，实际 %v", ecgWordingMigrationVersion, applied)
	}

	for _, r := range rows {
		var got string
		if err := db.Pool.QueryRow(ctx,
			`SELECT classification_code FROM ecg_records WHERE classification = $1`, r.raw).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if want := string(ecg.Classify(r.raw)); got != want {
			t.Fatalf("%q：迁移后库里是 %s，Go 分类器判 %s", r.raw, got, want)
		}
	}

	// 幂等：重放不该再动任何行
	mustExec(t, db, `DELETE FROM schema_migrations WHERE version = $1`, ecgWordingMigrationVersion)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("迁移重放失败: %v", err)
	}
	var n int
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM ecg_records WHERE classification = '未定论' AND classification_code = 'inconclusive'`).
		Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("重放后「未定论」的码应当不变，实际命中 %d 行", n)
	}
}
