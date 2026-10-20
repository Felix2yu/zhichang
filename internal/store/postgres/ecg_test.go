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

// 本文件锁心电的读写口径：
//   - 15360 个点必须原样往返（波形是这一节唯一的大数据，精度被截断也看不出来）；
//   - 重复提交同一份导出既不能多出一行记录，也不能多出一条波形；
//   - 「先只有记录、之后波形才到」时波形要能补上；
//   - 列表的点数取自波形表的实际长度，不取本次提交的值（否则补了波形列表还是 0）。
//   - 列表接口在空结果集上必须给 []，不能给 null。

func openECGDB(t *testing.T) *DB {
	t.Helper()
	db := openRollupDB(t)
	if _, err := db.Pool.Exec(context.Background(),
		`TRUNCATE ecg_voltages, ecg_records, ingest_runs`); err != nil {
		t.Fatalf("清理心电表失败: %v", err)
	}
	return db
}

// synthVoltages 造一段可复现的电压序列。刻意用不规则的小数位：
// 一旦某处按 float32 存，逐点相等断言会立刻炸出来。
func synthVoltages(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Sin(float64(i)/97.3)*421.3891234567 + math.Cos(float64(i)/13.7)*11.5000000001
	}
	return out
}

func ecgRecordAt(start time.Time, n int) sample.ECGRecord {
	rec := sample.ECGRecord{
		Source:             "移动心电图房颤提示软件",
		StartTime:          start,
		EndTime:            start.Add(30 * time.Second),
		LocalDate:          start.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02"),
		TZOffsetMin:        480,
		Classification:     "窦性心律",
		ClassificationCode: ecg.CodeSinusRhythm,
		VoltageUnit:        "mcV",
	}
	hr := 78.0
	fs := 512
	rec.AverageHeartRate = &hr
	rec.SamplingFrequency = &fs
	rec.Voltages = synthVoltages(n)
	rec.VoltageCount = len(rec.Voltages)
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

	// 同一份导出再提交一次：记录与波形都不该多出来
	if n, err = db.WriteECG(ctx, 0, []sample.ECGRecord{rec}); err != nil {
		t.Fatalf("重复写入失败: %v", err)
	} else if n != 0 {
		t.Fatalf("重复写入不该新增记录，实际 %d", n)
	}
	var recs, waves int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM ecg_records`).Scan(&recs); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM ecg_voltages`).Scan(&waves); err != nil {
		t.Fatal(err)
	}
	if recs != 1 || waves != 1 {
		t.Fatalf("记录/波形都应为 1 行，实际 %d/%d", recs, waves)
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

	wf, err := db.ECGVoltages(ctx, got.ID)
	if err != nil {
		t.Fatalf("读波形失败: %v", err)
	}
	if wf == nil {
		t.Fatal("波形不该为空")
	}
	if len(wf.Voltages) != 15360 {
		t.Fatalf("波形点数应为 15360，实际 %d", len(wf.Voltages))
	}
	// 逐点严格相等：任何按 float32 落库的实现都会在这里露馅
	for i, want := range rec.Voltages {
		if wf.Voltages[i] != want {
			t.Fatalf("第 %d 点不相等: %v != %v", i, wf.Voltages[i], want)
		}
	}
	if wf.Unit != "mcV" {
		t.Fatalf("单位应原样保留 mcV，实际 %q", wf.Unit)
	}
	if math.Abs(wf.DurationSec-30) > 1e-9 {
		t.Fatalf("波形时长应由点数/采样率推出 30 秒，实际 %v", wf.DurationSec)
	}
}

// 「先只有记录、之后波形才到」：判定是最有价值的部分，波形缺失不该连带丢记录；
// 而波形后到时必须能补进已存在的记录，而不是永远缺一张图。
func TestWriteECGFillsMissingWaveformLater(t *testing.T) {
	db := openECGDB(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 13, 15, 16, 15, 0, time.UTC)

	bare := ecgRecordAt(start, 0)
	bare.Voltages = nil
	bare.VoltageCount = 0
	if n, err := db.WriteECG(ctx, 0, []sample.ECGRecord{bare}); err != nil || n != 1 {
		t.Fatalf("写无波形记录失败: n=%d err=%v", n, err)
	}
	rows, err := db.ECGRange(ctx, bare.LocalDate, bare.LocalDate)
	if err != nil || len(rows) != 1 {
		t.Fatalf("记录应已入库: rows=%d err=%v", len(rows), err)
	}
	id := rows[0].ID

	// 无波形时：记录在、波形空，且必须是空数组而不是 null
	wf, err := db.ECGVoltages(ctx, id)
	if err != nil || wf == nil {
		t.Fatalf("记录存在就应返回波形对象: wf=%+v err=%v", wf, err)
	}
	if wf.Voltages == nil || len(wf.Voltages) != 0 {
		t.Fatalf("无波形时 Voltages 必须是空数组而不是 null: %#v", wf.Voltages)
	}

	full := ecgRecordAt(start, 32)
	if n, err := db.WriteECG(ctx, 0, []sample.ECGRecord{full}); err != nil || n != 0 {
		t.Fatalf("补写波形不该新增记录: n=%d err=%v", n, err)
	}
	wf, err = db.ECGVoltages(ctx, id)
	if err != nil || wf == nil {
		t.Fatalf("补写后应能读到波形: %v", err)
	}
	if len(wf.Voltages) != 32 {
		t.Fatalf("波形应被补上 32 点，实际 %d", len(wf.Voltages))
	}
	// 列表的点数要跟着波形走：记录行去重后不更新，留着 0 就会与上面这 32 点自相矛盾
	rows, err = db.ECGRange(ctx, bare.LocalDate, bare.LocalDate)
	if err != nil || len(rows) != 1 {
		t.Fatalf("读列表失败: %v", err)
	}
	if rows[0].VoltageCount != 32 {
		t.Fatalf("补上波形后列表点数应为 32，实际 %d", rows[0].VoltageCount)
	}
}

// 遗留脏数据：早期实现只在插入记录时写 voltage_count，之后补上的波形从不回填，
// 于是「波形有 32 点、列表显示 0 点」。重提交同一份导出要把它修回来，
// 而且这次导出没带波形时不能反过来把已有点数抹成 0。
func TestWriteECGSyncsStaleVoltageCount(t *testing.T) {
	db := openECGDB(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 13, 15, 16, 15, 0, time.UTC)

	full := ecgRecordAt(start, 32)
	if _, err := db.WriteECG(ctx, 0, []sample.ECGRecord{full}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE ecg_records SET voltage_count = 0`); err != nil {
		t.Fatalf("造脏点数失败: %v", err)
	}

	bare := ecgRecordAt(start, 0)
	bare.Voltages = nil
	bare.VoltageCount = 0
	if n, err := db.WriteECG(ctx, 0, []sample.ECGRecord{bare}); err != nil || n != 0 {
		t.Fatalf("重提交不该新增记录: n=%d err=%v", n, err)
	}
	rows, err := db.ECGRange(ctx, bare.LocalDate, bare.LocalDate)
	if err != nil || len(rows) != 1 {
		t.Fatalf("读列表失败: %v", err)
	}
	if rows[0].VoltageCount != 32 {
		t.Fatalf("脏点数应由波形表修回 32，实际 %d", rows[0].VoltageCount)
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

func TestECGVoltagesMissingRecord(t *testing.T) {
	db := openECGDB(t)
	wf, err := db.ECGVoltages(context.Background(), 999999)
	if err != nil {
		t.Fatalf("查不存在的记录不该报错: %v", err)
	}
	if wf != nil {
		t.Fatalf("查不存在的记录应返回 nil，实际 %+v", wf)
	}
}

// 记录里没有采样率时不能推出时长（会除零），此时时轴退回采样点序号，
// 时长保持 0——不能编一个数出来。
func TestECGVoltagesWithoutSamplingFrequency(t *testing.T) {
	db := openECGDB(t)
	ctx := context.Background()
	rec := ecgRecordAt(time.Date(2026, 10, 13, 15, 16, 15, 0, time.UTC), 8)
	rec.SamplingFrequency = nil
	if _, err := db.WriteECG(ctx, 0, []sample.ECGRecord{rec}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	rows, err := db.ECGRange(ctx, rec.LocalDate, rec.LocalDate)
	if err != nil || len(rows) != 1 {
		t.Fatalf("读取失败: %v", err)
	}
	if rows[0].SamplingFrequency != nil {
		t.Fatalf("采样率应为 NULL，实际 %v", *rows[0].SamplingFrequency)
	}
	wf, err := db.ECGVoltages(ctx, rows[0].ID)
	if err != nil || wf == nil {
		t.Fatalf("读波形失败: %v", err)
	}
	if wf.DurationSec != 0 {
		t.Fatalf("无采样率时不该推时长，实际 %v", wf.DurationSec)
	}
	if len(wf.Voltages) != 8 {
		t.Fatalf("波形点数应为 8，实际 %d", len(wf.Voltages))
	}
}
