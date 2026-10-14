package postgres

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/ecg"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// 本文件锁心电记录的读写口径：
//   - 重复提交同一份导出不能多出一行（唯一约束 (start_time, source) 要真的生效）；
//   - 判定、心率、采样率、点数、时长要原样往返；
//   - 列表接口在空结果集上必须给 []，不能给 null。

func openECGDB(t *testing.T) *DB {
	t.Helper()
	db := openRollupDB(t)
	if _, err := db.Pool.Exec(context.Background(),
		`TRUNCATE ecg_records, ingest_runs`); err != nil {
		t.Fatalf("清理心电表失败: %v", err)
	}
	return db
}

// ecgRecordAt 造一条起始于 start、上游声明 n 个采样点的心电记录。
func ecgRecordAt(start time.Time, n int) sample.ECGRecord {
	rec := sample.ECGRecord{
		Source:             "移动心电图房颤提示软件",
		StartTime:          start,
		EndTime:            start.Add(30 * time.Second),
		LocalDate:          start.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02"),
		TZOffsetMin:        480,
		Classification:     "窦性心律",
		ClassificationCode: ecg.CodeSinusRhythm,
		VoltageCount:       n,
	}
	hr := 78.0
	fs := 512
	rec.AverageHeartRate = &hr
	rec.SamplingFrequency = &fs
	return rec
}

func TestWriteECGRoundTripAndIdempotent(t *testing.T) {
	db := openECGDB(t)
	ctx := context.Background()
	// 真实形态：512 Hz × 30 秒
	rec := ecgRecordAt(time.Date(2026, 10, 13, 15, 16, 15, 0, time.UTC), 15360)

	n, err := db.WriteECG(ctx, 0, []sample.ECGRecord{rec})
	if err != nil {
		t.Fatalf("写入心电失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("首次写入应新增 1 条，实际 %d", n)
	}

	// 同一份导出再提交一次：不该多出记录
	if n, err = db.WriteECG(ctx, 0, []sample.ECGRecord{rec}); err != nil {
		t.Fatalf("重复写入失败: %v", err)
	} else if n != 0 {
		t.Fatalf("重复写入不该新增记录，实际 %d", n)
	}
	var recs int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM ecg_records`).Scan(&recs); err != nil {
		t.Fatal(err)
	}
	if recs != 1 {
		t.Fatalf("记录应只有 1 行，实际 %d", recs)
	}

	rows, err := db.ECGRange(ctx, rec.LocalDate, rec.LocalDate)
	if err != nil {
		t.Fatalf("读列表失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("列表应 1 行，实际 %d", len(rows))
	}
	got := rows[0]
	if got.Classification != "窦性心律" || got.ClassificationCode != string(ecg.CodeSinusRhythm) {
		t.Fatalf("判定下落错误: %+v", got)
	}
	if got.VoltageCount != 15360 || got.SamplingFrequency == nil || *got.SamplingFrequency != 512 {
		t.Fatalf("点数/采样率错误: %+v", got)
	}
	if got.AverageHeartRate == nil || *got.AverageHeartRate != 78 {
		t.Fatalf("平均心率错误: %+v", got.AverageHeartRate)
	}
	if math.Abs(got.DurationSec-30) > 1e-9 {
		t.Fatalf("时长应为 30 秒，实际 %v", got.DurationSec)
	}
	if got.Source != rec.Source {
		t.Fatalf("来源错误: %q", got.Source)
	}
	if got.TZOffsetMin != 480 || got.LocalDate != rec.LocalDate {
		t.Fatalf("偏移/本地日期错误: %d / %s", got.TZOffsetMin, got.LocalDate)
	}
}

func TestECGRangeContract(t *testing.T) {
	db := openECGDB(t)
	ctx := context.Background()

	// 空结果集必须是 []，否则前端 .map() 直接抛 TypeError
	empty, err := db.ECGRange(ctx, "2026-10-01", "2026-10-31")
	if err != nil {
		t.Fatalf("空区间查询失败: %v", err)
	}
	if empty == nil {
		t.Fatal("空结果集必须是空 slice，不是 nil")
	}
	if b, err := json.Marshal(empty); err != nil || string(b) != "[]" {
		t.Fatalf("空结果集应编成 []，实际 %s (err=%v)", b, err)
	}

	a := ecgRecordAt(time.Date(2026, 10, 1, 15, 49, 16, 0, time.UTC), 4)
	b := ecgRecordAt(time.Date(2026, 10, 13, 15, 16, 15, 0, time.UTC), 4)
	b.Classification, b.ClassificationCode = "Sinus Rhythm", ecg.CodeSinusRhythm
	if _, err := db.WriteECG(ctx, 0, []sample.ECGRecord{a, b}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	// 按 local_date 过滤（与运动/睡眠同口径），且按起始时间倒序
	rows, err := db.ECGRange(ctx, "2026-10-10", "2026-10-31")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(rows) != 1 || rows[0].LocalDate != "2026-10-13" {
		t.Fatalf("区间过滤错误: %+v", rows)
	}
	all, err := db.ECGRange(ctx, "2026-10-01", "2026-10-31")
	if err != nil || len(all) != 2 {
		t.Fatalf("应有两行，实际 %d (err=%v)", len(all), err)
	}
	if all[0].LocalDate != "2026-10-13" || all[1].LocalDate != "2026-10-01" {
		t.Fatalf("应按起始时间倒序，实际 %s, %s", all[0].LocalDate, all[1].LocalDate)
	}

	// 非法日期必须报错，而不是悄悄查出一堆东西
	if _, err := db.ECGRange(ctx, "2026-13-99", "2026-10-31"); err == nil {
		t.Fatal("非法起始日期应报错")
	}
}

// 同为窦性心律但不同来源/不同起始秒的两条记录必须都留下：
// 幂等锚点是 (start_time, source)，不是分类码。
func TestWriteECGKeepsDistinctMeasurements(t *testing.T) {
	db := openECGDB(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 13, 15, 16, 15, 0, time.UTC)

	a := ecgRecordAt(base, 15360)
	b := ecgRecordAt(base.Add(30*time.Second), 15360)
	if n, err := db.WriteECG(ctx, 0, []sample.ECGRecord{a, b}); err != nil || n != 2 {
		t.Fatalf("两条不同时刻的记录都应入库: n=%d err=%v", n, err)
	}
	rows, err := db.ECGRange(ctx, a.LocalDate, a.LocalDate)
	if err != nil || len(rows) != 2 {
		t.Fatalf("应有两行，实际 %d (err=%v)", len(rows), err)
	}
}
