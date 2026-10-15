package api

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/ecg"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// 本文件锁 /api/ecg 与 /api/ecg/waveform 的对外契约：
//   - 判定中文化在服务端完成（上游原文随手机语言变化，不能直接给前端）；
//   - 认不出判定时回落原文并明确标注来路；
//   - 列表绝不带波形（那是 120KB），波形单独一条接口；
//   - 空结果集是 []，不是 null。

func addECG(t *testing.T, db *postgres.DB, runID int64, recs ...sample.ECGRecord) {
	t.Helper()
	if _, err := db.WriteECG(context.Background(), runID, recs); err != nil {
		t.Fatalf("写心电失败: %v", err)
	}
}

func ecgRec(date string, hour, minute int, classification string) sample.ECGRecord {
	at := wallAt(date, hour, minute)
	rec := sample.ECGRecord{
		Source:             "移动心电图房颤提示软件",
		StartTime:          at.UTC(),
		EndTime:            at.UTC().Add(30 * time.Second),
		LocalDate:          date,
		TZOffsetMin:        480,
		Classification:     classification,
		ClassificationCode: ecg.Classify(classification),
		VoltageUnit:        "mcV",
	}
	hr := 78.0
	fs := 512
	rec.AverageHeartRate = &hr
	rec.SamplingFrequency = &fs
	return rec
}

func TestECGListContract(t *testing.T) {
	h, db := newRouter(t, "")
	runID := newRun(t, db)

	withWave := ecgRec(dateInLoc(0), 23, 16, "窦性心律")
	withWave.Voltages = []float64{-63.28, 0, 66.42}
	withWave.VoltageCount = len(withWave.Voltages)

	bare := ecgRec(dateInLoc(-3), 22, 45, "Sinus Rhythm") // 英文原文，归一到同一个码
	odd := ecgRec(dateInLoc(-6), 21, 48, "室性早搏")          // 认不出的判定
	addECG(t, db, runID, withWave, bare, odd)

	r := get(t, h, "/api/ecg?from="+dateInLoc(-30)+"&to="+dateInLoc(0))
	if r.status != 200 {
		t.Fatalf("状态码 = %d body=%s", r.status, r.body)
	}
	recs := r.arr(t, "records")
	if len(recs) != 3 {
		t.Fatalf("应 3 条记录，实际 %d（body=%s）", len(recs), r.body)
	}

	first := recs[0].(map[string]any)
	// 上游是简中原文，服务端给出统一措辞
	if first["classification"] != "窦性心律" {
		t.Fatalf("原文应原样透传，实际 %v", first["classification"])
	}
	if first["classification_label"] != "窦性心律" {
		t.Fatalf("判定标签错误: %v", first["classification_label"])
	}
	if first["classification_known"] != true {
		t.Fatalf("简中判定应算已知: %v", first)
	}
	if first["voltage_count"] != float64(3) {
		t.Fatalf("点数错误: %v", first["voltage_count"])
	}
	if first["duration_sec"] != float64(30) {
		t.Fatalf("时长错误: %v", first["duration_sec"])
	}
	// 列表不能把波形带上：那是 120KB，列表页没有画它们的地方
	if _, ok := first["voltages"]; ok {
		t.Fatalf("列表不该包含波形: %v", first)
	}

	// 英文原文与简中原文必须归一到同一个标签——这是「换手机语言不分裂」的保证
	second := recs[1].(map[string]any)
	if second["classification"] != "Sinus Rhythm" {
		t.Fatalf("英文原文应原样保留，实际 %v", second["classification"])
	}
	if second["classification_label"] != first["classification_label"] {
		t.Fatalf("同一心律的两种语言写法应给同一标签: %v vs %v",
			first["classification_label"], second["classification_label"])
	}

	// 认不出的判定回落展示原文，并标明这是上游原话而不是我们的归一结论
	third := recs[2].(map[string]any)
	if third["classification_known"] != false {
		t.Fatalf("认不出的判定不该声称已知: %v", third)
	}
	if third["classification_label"] != "室性早搏" {
		t.Fatalf("认不出时应回落原文，实际 %v", third["classification_label"])
	}
}

func TestECGListEmptyIsArray(t *testing.T) {
	h, _ := newRouter(t, "")
	r := get(t, h, "/api/ecg?from=2000-01-01&to=2000-01-31")
	if r.status != 200 {
		t.Fatalf("状态码 = %d", r.status)
	}
	if got := r.arr(t, "records"); len(got) != 0 {
		t.Fatalf("空区间应给空数组，实际 %v", got)
	}
}

func TestECGListRejectsBadDate(t *testing.T) {
	h, _ := newRouter(t, "")
	get(t, h, "/api/ecg?from=2026-13-99&to=2026-10-31").wantErr(t, 400)
}

func TestECGWaveformContract(t *testing.T) {
	h, db := newRouter(t, "")
	runID := newRun(t, db)

	rec := ecgRec(dateInLoc(0), 23, 16, "窦性心律")
	rec.Voltages = []float64{-63.284713745117188, -14.61724853515625, 0, 407.3128662109375}
	rec.VoltageCount = len(rec.Voltages)
	addECG(t, db, runID, rec)

	rows, err := db.ECGRange(context.Background(), dateInLoc(0), dateInLoc(0))
	if err != nil || len(rows) != 1 {
		t.Fatalf("读取记录失败: rows=%d err=%v", len(rows), err)
	}
	id := rows[0].ID

	r := get(t, h, "/api/ecg/waveform?id="+strconv.FormatInt(id, 10))
	if r.status != 200 {
		t.Fatalf("状态码 = %d body=%s", r.status, r.body)
	}
	vs := r.arr(t, "voltages")
	if len(vs) != 4 {
		t.Fatalf("波形点数应为 4，实际 %d", len(vs))
	}
	// 浮点必须原样往返，不能被 float32 截断
	if vs[0] != -63.284713745117188 {
		t.Fatalf("首个电压值被截断了: %v", vs[0])
	}
	if vs[3] != 407.3128662109375 {
		t.Fatalf("末个电压值被截断了: %v", vs[3])
	}
	if r.str(t, "unit") != "mcV" {
		t.Fatalf("单位应原样给出 mcV，实际 %q", r.str(t, "unit"))
	}
	if r.num(t, "duration_sec") != 4.0/512.0 {
		t.Fatalf("时长应由点数/采样率推出，实际 %v", r.fields["duration_sec"])
	}

	// 记录不存在：404 而不是空波形——两者是不同的事
	get(t, h, "/api/ecg/waveform?id=999999").wantErr(t, 404)
	// id 非法：400
	get(t, h, "/api/ecg/waveform?id=abc").wantErr(t, 400)
	get(t, h, "/api/ecg/waveform").wantErr(t, 400)
	get(t, h, "/api/ecg/waveform?id=0").wantErr(t, 400)
}

// 记录在、波形没存（上游这次没导出电压数据）时，接口给空数组而不是 404，
// 前端据此显示「这条记录没有波形数据」。
func TestECGWaveformEmptyWhenNotStored(t *testing.T) {
	h, db := newRouter(t, "")
	runID := newRun(t, db)
	addECG(t, db, runID, ecgRec(dateInLoc(0), 23, 16, "窦性心律"))

	rows, err := db.ECGRange(context.Background(), dateInLoc(0), dateInLoc(0))
	if err != nil || len(rows) != 1 {
		t.Fatalf("读取记录失败: %v", err)
	}
	r := get(t, h, "/api/ecg/waveform?id="+strconv.FormatInt(rows[0].ID, 10))
	if r.status != 200 {
		t.Fatalf("状态码 = %d body=%s", r.status, r.body)
	}
	if got := r.arr(t, "voltages"); len(got) != 0 {
		t.Fatalf("没有波形时应给空数组，实际 %v", got)
	}
}

// 心电是事件数据，绝不能混进指标接口：混进去会让天级聚合凭空多出数据。
func TestECGNotExposedAsMetric(t *testing.T) {
	h, _ := newRouter(t, "")
	m := get(t, h, "/api/metrics")
	for _, item := range m.arr(t, "metrics") {
		if def := item.(map[string]any); def["metric"] == "ecg" || def["metric"] == "ecg_records" {
			t.Fatalf("心电不该出现在指标字典里: %v", def)
		}
	}
	get(t, h, "/api/samples?metric=ecg").wantErr(t, 400)
}
