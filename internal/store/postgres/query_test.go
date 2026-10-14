package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// 本文件锁 query.go 的对外口径：错误路径、空结果的 JSON 契约、
// date/timestamptz 的 UTC 约定、以及 NULL 在 Go 侧落成什么。

// mustExec 是本包测试用的小工具：SQL 跑失败就直接让测试终止。
func mustExec(t *testing.T, db *DB, sql string, args ...any) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("执行 SQL 失败: %v\n%s", err, sql)
	}
}

// seedDailyDay 直接写 daily_metrics_day，绕开聚合：本文件只验查询侧口径。
// updatedAt 用固定 UTC 字面量，才能断言「出参一律是 UTC 的 RFC3339」。
func seedDailyDay(t *testing.T, db *DB, date, metric string, value float64, sampleCount, sourceCount int, sources []string, updatedAt string) {
	t.Helper()
	var src any
	if sources == nil {
		src = []string{}
	} else {
		src = sources
	}
	if updatedAt == "" {
		updatedAt = date + "T00:00:00Z"
	}
	mustExec(t, db, `INSERT INTO daily_metrics_day (local_date, metric, value, sample_count, source_count, sources, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::timestamptz)
		ON CONFLICT (local_date, metric) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		date, metric, value, sampleCount, sourceCount, src, updatedAt)
}

// TestDailySeriesContract 锁定天级序列的口径：
//   - 指标列表为空时不查库也必须回非 nil 空数组（前端直接 .map()）；
//   - BETWEEN 两端都是闭区间；
//   - 排序是「指标名 → 日期」，同指标内日期升序；
//   - sources 是 text[]，库里没写时是 {} 而不是 NULL。
func TestDailySeriesContract(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	got, err := db.DailySeries(ctx, nil, "2026-10-01", "2026-10-02")
	if err != nil {
		t.Fatalf("空指标列表不该查库也不该报错: %v", err)
	}
	if got == nil {
		t.Fatal("nil slice 会被编码成 JSON null")
	}
	if len(got) != 0 {
		t.Fatalf("期望空结果，实际 %d 行", len(got))
	}

	seedDailyDay(t, db, "2026-10-01", "heart_rate", 72.5, 3, 2, []string{"Apple Watch", "iPhone"}, "")
	seedDailyDay(t, db, "2026-10-02", "heart_rate", 70, 1, 1, []string{"Apple Watch"}, "")
	seedDailyDay(t, db, "2026-10-01", "step_count", 8000, 5, 1, nil, "")
	// 区间外的两天：必须被 BETWEEN 的两端裁掉
	seedDailyDay(t, db, "2026-09-30", "heart_rate", 60, 1, 1, []string{"Apple Watch"}, "")
	seedDailyDay(t, db, "2026-10-05", "heart_rate", 60, 1, 1, []string{"Apple Watch"}, "")

	rows, err := db.DailySeries(ctx, []string{"heart_rate", "step_count"}, "2026-10-01", "2026-10-02")
	if err != nil {
		t.Fatalf("DailySeries 失败: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("期望 3 行（两端闭区间、区间外两天排除），实际 %d：%v", len(rows), rows)
	}
	// ORDER BY metric, local_date：heart_rate 的两天在前
	if rows[0].Date != "2026-10-01" || rows[1].Date != "2026-10-02" || rows[2].Metric != "step_count" {
		t.Fatalf("排序不符：%v", rows)
	}
	if rows[0].Value != 72.5 || rows[0].SampleCount != 3 || rows[0].SourceCount != 2 {
		t.Fatalf("数值/样本数/来源数不符：%+v", rows[0])
	}
	if strings.Join(rows[0].Sources, ",") != "Apple Watch,iPhone" {
		t.Fatalf("sources 不符：%v", rows[0].Sources)
	}
	// 库里 sources 是默认 '{}'：扫出来必须是空数组而不是 nil，否则 JSON 是 null
	if rows[2].Sources == nil {
		t.Fatal("默认 '{}' 应扫成非 nil 空数组")
	}
	b, err := json.Marshal(rows[2].Sources)
	if err != nil || string(b) != "[]" {
		t.Fatalf("sources 应编码为 []，实际 %s (%v)", b, err)
	}

	// 只查一天：左端右端都要命中
	one, err := db.DailySeries(ctx, []string{"step_count"}, "2026-10-01", "2026-10-01")
	if err != nil || len(one) != 1 {
		t.Fatalf("单日区间应命中 1 行，实际 %d (%v)", len(one), err)
	}

	// 无数据的区间：[] 而不是 null
	empty, err := db.DailySeries(ctx, []string{"step_count"}, "2026-01-01", "2026-01-02")
	if err != nil {
		t.Fatalf("空区间不该报错: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("空区间应为非 nil 空数组，实际 %#v", empty)
	}
}

// TestParseDateRejectedByEveryQuery 锁定「日期格式错误一律报错而不是静默空结果」：
// 每个带日期参数的查询都要先过 parseDate，前端传错格式时必须能拿到 4xx 级错误。
func TestParseDateRejectedByEveryQuery(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	// 每一项都必须解析失败：Go 的 2006-01-02 版面要求定宽两位月/日，
	// 所以 2026-1-1 这种「看着像日期」的写法也必须被拒——否则前端少补一个零就静默查空。
	bad := []string{"", "2026-13-01", "2026/10/01", "2026-1-1", "20261001", "10-01-2026", "abc", "2026-10-01T00:00:00Z"}

	cases := []struct {
		name string
		call func(from, to string) error
	}{
		{"DailySeries", func(f, t2 string) error { _, e := db.DailySeries(ctx, []string{"step_count"}, f, t2); return e }},
		{"Summary", func(f, _ string) error { _, e := db.Summary(ctx, f, nil); return e }},
		{"Samples", func(f, t2 string) error { _, e := db.Samples(ctx, "step_count", f, t2, 10); return e }},
		{"SleepRange", func(f, t2 string) error { _, e := db.SleepRange(ctx, f, t2); return e }},
		{"SleepStages", func(f, _ string) error { _, e := db.SleepStages(ctx, f); return e }},
		{"SleepStagesRange", func(f, t2 string) error { _, e := db.SleepStagesRange(ctx, f, t2); return e }},
		{"WorkoutRange", func(f, t2 string) error { _, e := db.WorkoutRange(ctx, f, t2); return e }},
		{"SleepNightMetrics", func(f, t2 string) error {
			_, e := db.SleepNightMetrics(ctx, f, t2, nil)
			return e
		}},
	}
	for _, c := range cases {
		for _, s := range bad {
			if err := c.call(s, "2026-10-01"); err == nil {
				t.Fatalf("%s 接受非法起始日期 %q", c.name, s)
			} else if !strings.Contains(err.Error(), "日期格式应为 YYYY-MM-DD") {
				t.Fatalf("%s 的报错口径不对: %v", c.name, err)
			}
			if c.name == "Summary" || c.name == "SleepStages" {
				continue // 这两个只有单个日期参数
			}
			if err := c.call("2026-10-01", s); err == nil {
				t.Fatalf("%s 接受非法结束日期 %q", c.name, s)
			}
		}
	}
}

// TestSummaryContract 锁定概览卡片的口径：
//   - 前一日缺失 → PrevValue/Delta/DeltaPct 都是 nil（区分「没有昨天」与「昨天是 0」）；
//   - 前一日是 0 → DeltaPct 必须是 nil，不能除零算出 ±Inf；
//   - defs 白名单生效，defs 为空表示不过滤；
//   - updated_at 出参是 UTC 的 RFC3339，不受服务端时区影响。
func TestSummaryContract(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	const today, prev = "2026-10-13", "2026-10-12"

	seedDailyDay(t, db, today, "step_count", 10000, 5, 1, []string{"iPhone"}, "2026-10-13T02:03:04Z")
	seedDailyDay(t, db, prev, "step_count", 8000, 5, 1, []string{"iPhone"}, "2026-10-12T02:03:04Z")
	// 前一日是 0：DeltaPct 不能算出来
	seedDailyDay(t, db, today, "active_energy", 300, 2, 1, []string{"iPhone"}, "")
	seedDailyDay(t, db, prev, "active_energy", 0, 2, 1, []string{"iPhone"}, "")
	// 只有今天有：没有前一日对比
	seedDailyDay(t, db, today, "resting_heart_rate", 55, 1, 1, []string{"Apple Watch"}, "")
	// 只有昨天的：不进今天的概览
	seedDailyDay(t, db, prev, "distance", 5000, 3, 1, []string{"iPhone"}, "")

	items, err := db.Summary(ctx, today, nil)
	if err != nil {
		t.Fatalf("Summary 失败: %v", err)
	}
	byMetric := map[string]SummaryItem{}
	for _, it := range items {
		byMetric[it.Metric] = it
	}
	if len(items) != 3 {
		t.Fatalf("期望 3 项（昨天的 distance 不进今天概览），实际 %d：%v", len(items), items)
	}

	step := byMetric["step_count"]
	if step.Value == nil || *step.Value != 10000 {
		t.Fatalf("value 不符：%v", step.Value)
	}
	if step.PrevValue == nil || *step.PrevValue != 8000 {
		t.Fatalf("prev_value 不符：%v", step.PrevValue)
	}
	if step.Delta == nil || *step.Delta != 2000 {
		t.Fatalf("delta 不符：%v", step.Delta)
	}
	if step.DeltaPct == nil || *step.DeltaPct != 25 {
		t.Fatalf("delta_pct 应为 25，实际 %v", step.DeltaPct)
	}
	if step.UpdatedAt != "2026-10-13T02:03:04Z" {
		t.Fatalf("updated_at 应是 UTC 的 RFC3339，实际 %q", step.UpdatedAt)
	}

	energy := byMetric["active_energy"]
	if energy.PrevValue == nil || *energy.PrevValue != 0 {
		t.Fatalf("前一日为 0 时 prev_value 应是指向 0 的指针（区别于「没有昨天」），实际 %v", energy.PrevValue)
	}
	if energy.Delta == nil || *energy.Delta != 300 {
		t.Fatalf("delta 不符：%v", energy.Delta)
	}
	if energy.DeltaPct != nil {
		t.Fatalf("前一日为 0 时 delta_pct 必须是 nil，不能除零，实际 %v", *energy.DeltaPct)
	}

	resting := byMetric["resting_heart_rate"]
	if resting.PrevValue != nil || resting.Delta != nil || resting.DeltaPct != nil {
		t.Fatalf("无前一日数据时三个对比字段都该是 nil：%+v", resting)
	}

	// 白名单：只留登记过的指标
	filtered, err := db.Summary(ctx, today, map[string]struct{}{"step_count": {}})
	if err != nil {
		t.Fatalf("带白名单的 Summary 失败: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Metric != "step_count" {
		t.Fatalf("白名单没生效：%v", filtered)
	}

	// DisplayName/Unit 不由 store 填（口径：由 api 层用字典补），这里确认没被瞎猜
	if filtered[0].DisplayName != "" || filtered[0].Unit != "" {
		t.Fatalf("store 不该填中文名和单位，实际 %q/%q", filtered[0].DisplayName, filtered[0].Unit)
	}

	// 没有任何数据的日期：[] 而不是 null
	empty, err := db.Summary(ctx, "2020-01-01", nil)
	if err != nil {
		t.Fatalf("空日期不该报错: %v", err)
	}
	if empty == nil {
		t.Fatal("空结果不能是 nil slice")
	}
	b, _ := json.Marshal(map[string]any{"items": empty})
	if string(b) != `{"items":[]}` {
		t.Fatalf("实际 %s", b)
	}
}

// TestSamplesLimitClampAndNullBounds 锁定明细查询的口径：
//   - limit 的 0 / 负数 / 超大都一律收敛到 1000（上限保护，缺省也是 1000）；
//   - 按 start_time 倒序（排障时先看最近）；
//   - value_min / value_max 为 NULL 时 Go 侧是指针 nil，JSON 里整个字段消失。
func TestSamplesLimitClampAndNullBounds(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()

	base := time.Date(2026, 10, 13, 1, 0, 0, 0, time.UTC) // 本地 09:00 (+0800)
	steps := make([]sample.Sample, 0, 5)
	for i := 0; i < 5; i++ {
		st := base.Add(time.Duration(i) * time.Hour)
		s := sample.Sample{
			Metric: "step_count", Source: "iPhone", StartTime: st, EndTime: st.Add(30 * time.Minute),
			Value: float64(100 * (i + 1)), Unit: "count", LocalDate: "2026-10-13", TZOffsetMin: 480,
		}
		if i == 0 {
			// 有区间的一行；其余留 NULL，断言 nil 指针口径
			lo, hi := float64(90), float64(110)
			s.ValueMin, s.ValueMax = &lo, &hi
		}
		steps = append(steps, s)
	}
	// 另一天、另一指标：验 local_date 过滤与 metric 过滤
	other := time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC)
	steps = append(steps, sample.Sample{
		Metric: "heart_rate", Source: "Apple Watch", StartTime: other, EndTime: other,
		Value: 66, Unit: "bpm", LocalDate: "2026-10-11", TZOffsetMin: 480,
	})
	if _, _, _, _, err := db.WriteSamples(ctx, 7, steps, "strict"); err != nil {
		t.Fatalf("写入样本失败: %v", err)
	}

	for _, limit := range []int{0, -1, 1, 2, 5, 5000} {
		want := 5
		if limit == 1 || limit == 2 {
			want = limit
		}
		rows, err := db.Samples(ctx, "step_count", "2026-10-11", "2026-10-13", limit)
		if err != nil {
			t.Fatalf("limit=%d 查询失败: %v", limit, err)
		}
		if len(rows) != want {
			t.Fatalf("limit=%d 期望 %d 行，实际 %d", limit, want, len(rows))
		}
		if len(rows) > 1 && rows[0].StartTime <= rows[1].StartTime {
			t.Fatalf("明细必须按 start_time 倒序：%v / %v", rows[0].StartTime, rows[1].StartTime)
		}
	}

	rows, err := db.Samples(ctx, "step_count", "2026-10-13", "2026-10-13", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("local_date 过滤失效，实际 %d 行", len(rows))
	}
	// 出参时刻一律 UTC RFC3339
	if rows[0].StartTime != "2026-10-13T05:00:00Z" {
		t.Fatalf("start_time 应是 UTC 时刻，实际 %q", rows[0].StartTime)
	}
	if rows[0].EndTime != "2026-10-13T05:30:00Z" {
		t.Fatalf("end_time 应是 UTC 时刻，实际 %q", rows[0].EndTime)
	}
	if rows[0].LocalDate != "2026-10-13" {
		t.Fatalf("local_date 不符：%q", rows[0].LocalDate)
	}
	if rows[0].Source != "iPhone" || rows[0].Unit != "count" {
		t.Fatalf("source/unit 不符：%+v", rows[0])
	}
	// 有区间的那行（最早的一条 → 倒序后在最后）
	last := rows[4]
	if last.Min == nil || *last.Min != 90 || last.Max == nil || *last.Max != 110 {
		t.Fatalf("value_min/max 应原样透出，实际 %+v", last)
	}
	for _, r := range rows[:4] {
		if r.Min != nil || r.Max != nil {
			t.Fatalf("库里是 NULL，扫出来必须是指针 nil，实际 %+v", r)
		}
	}
	b, _ := json.Marshal(rows[0])
	if strings.Contains(string(b), `"min"`) || strings.Contains(string(b), `"max"`) {
		t.Fatalf("nil 区间不该出现在 JSON 里：%s", b)
	}

	// 空结果：[] 而不是 null
	empty, err := db.Samples(ctx, "step_count", "2026-10-20", "2026-10-21", 10)
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("无明细时应给非 nil 空数组，实际 %#v", empty)
	}
}

// TestWorkoutRangeContract 锁定运动记录查询的口径：
//   - total_energy / total_distance 为 NULL 时是指针 nil；
//   - energy_unit / distance_unit 为 NULL 时经 COALESCE 落成空串而不是 null；
//   - 按 start_time 倒序。
func TestWorkoutRangeContract(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	start1 := time.Date(2026, 10, 12, 23, 0, 0, 0, time.UTC)
	start2 := time.Date(2026, 10, 13, 0, 30, 0, 0, time.UTC)
	n, err := db.WriteWorkouts(ctx, 1, []sample.Workout{
		{Name: "户外跑步", Source: "Apple Watch", StartTime: start1, EndTime: start1.Add(40 * time.Minute),
			DurationMin: 40, TotalEnergy: fptr(320.5), EnergyUnit: "kcal",
			TotalDistance: fptr(5.2), DistanceUnit: "km", LocalDate: "2026-10-13", TZOffsetMin: 480},
		{Name: "力量训练", Source: "iPhone", StartTime: start2, EndTime: start2.Add(20 * time.Minute),
			DurationMin: 20, LocalDate: "2026-10-13", TZOffsetMin: 480},
	})
	if err != nil {
		t.Fatalf("写入运动失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("期望入库 2 条，实际 %d", n)
	}

	rows, err := db.WorkoutRange(ctx, "2026-10-12", "2026-10-13")
	if err != nil {
		t.Fatalf("WorkoutRange 失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("期望 2 行，实际 %d", len(rows))
	}
	// 倒序：10-13 00:30 在前
	first := rows[0]
	if first.Name != "力量训练" || first.LocalDate != "2026-10-13" {
		t.Fatalf("排序或归属日期不符：%+v", first)
	}
	if first.TotalEnergy != nil || first.EnergyUnit != "" {
		t.Fatalf("库里 NULL 的能量应是指针 nil + 空串单位，实际 %+v / %q", first.TotalEnergy, first.EnergyUnit)
	}
	if first.TotalDistance != nil || first.DistanceUnit != "" {
		t.Fatalf("NULL 距离同样要 nil + 空串，实际 %+v", first)
	}
	if first.DurationMin != 20 {
		t.Fatalf("duration_min 不符：%v", first.DurationMin)
	}
	if first.Start != "2026-10-13T00:30:00Z" || first.End != "2026-10-13T00:50:00Z" {
		t.Fatalf("起止时刻必须是 UTC RFC3339，实际 %q / %q", first.Start, first.End)
	}
	second := rows[1]
	if second.TotalEnergy == nil || *second.TotalEnergy != 320.5 || second.EnergyUnit != "kcal" {
		t.Fatalf("有值的一行要原样透出：%+v", second)
	}
	if second.Source != "Apple Watch" {
		t.Fatalf("source 不符：%q", second.Source)
	}

	empty, err := db.WorkoutRange(ctx, "2026-11-01", "2026-11-02")
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("无运动记录应为非 nil 空数组，实际 %#v", empty)
	}
}

// TestSourcesListing 锁定来源治理的口径：
//   - 空字符串来源被跳过（不能登记出一条 source=” 的脏数据）；
//   - device_class 由 ClassifySource 粗判；
//   - 重复 upsert 只更新 last_seen_at，不攒新行；
//   - 样本数是 samples 表实际行数 LEFT JOIN，登记过但一条样本没有的来源回 0。
func TestSourcesListing(t *testing.T) {
	db := openIngestDB(t)
	ctx := context.Background()

	if err := db.UpsertSources(ctx, nil); err != nil {
		t.Fatalf("空列表不该报错: %v", err)
	}
	if err := db.UpsertSources(ctx, []string{"Apple Watch", "iPhone", "", "某某健康App", "Whoop"}); err != nil {
		t.Fatalf("登记来源失败: %v", err)
	}
	// 再来一次：仍然只有 4 行
	if err := db.UpsertSources(ctx, []string{"Apple Watch"}); err != nil {
		t.Fatalf("重复登记失败: %v", err)
	}
	var cnt int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM sources`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 4 {
		t.Fatalf("期望 4 个来源（空串跳过），实际 %d", cnt)
	}

	rows, err := db.Sources(ctx)
	if err != nil {
		t.Fatalf("Sources 失败: %v", err)
	}
	if rows == nil {
		t.Fatal("nil slice 会被编码成 JSON null")
	}
	if len(rows) != 4 {
		t.Fatalf("期望 4 行，实际 %d", len(rows))
	}
	byName := map[string]SourceRow{}
	for _, r := range rows {
		byName[r.Source] = r
	}
	if byName["Apple Watch"].DeviceType != "watch" || byName["iPhone"].DeviceType != "phone" {
		t.Fatalf("设备类别判定不符：%+v / %+v", byName["Apple Watch"], byName["iPhone"])
	}
	if byName["某某健康App"].DeviceType != "unknown" || byName["Whoop"].DeviceType != "unknown" {
		t.Fatalf("认不出的来源必须是 unknown：%+v / %+v", byName["某某健康App"], byName["Whoop"])
	}
	for _, r := range rows {
		if r.Samples != 0 {
			t.Fatalf("还没有样本的来源应回 0（COALESCE）而不是别的：%+v", r)
		}
		if r.Preferred {
			t.Fatal("新登记的来源默认不是首选")
		}
		if r.FirstSeen.IsZero() || r.LastSeen.IsZero() {
			t.Fatalf("first/last seen 必须有值：%+v", r)
		}
	}

	// 有样本后按样本数排序；没样本的来源排后面
	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		{Metric: "step_count", Source: "Apple Watch", StartTime: at0800(10, 13, 9, 0), EndTime: at0800(10, 13, 9, 0),
			Value: 1, Unit: "count", LocalDate: "2026-10-13", TZOffsetMin: 480},
		{Metric: "step_count", Source: "Apple Watch", StartTime: at0800(10, 13, 10, 0), EndTime: at0800(10, 13, 10, 0),
			Value: 2, Unit: "count", LocalDate: "2026-10-13", TZOffsetMin: 480},
		{Metric: "step_count", Source: "iPhone", StartTime: at0800(10, 13, 9, 0), EndTime: at0800(10, 13, 9, 0),
			Value: 1, Unit: "count", LocalDate: "2026-10-13", TZOffsetMin: 480},
	}, "strict"); err != nil {
		t.Fatalf("写入样本失败: %v", err)
	}
	rows, err = db.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Source != "Apple Watch" || rows[0].Samples != 2 || rows[1].Samples != 1 {
		t.Fatalf("应按样本数降序，实际 %+v / %+v", rows[0], rows[1])
	}
	if rows[2].Samples != 0 || rows[3].Samples != 0 {
		t.Fatalf("无样本来源应排最后且为 0：%+v / %+v", rows[2], rows[3])
	}
}

// TestLatestSleepStageDateNoData 锁定「完全没有分期数据」时回空串而不是报错：
// 空库首启时页面要能正常渲染，不该因为一句 ErrNoRows 就 500。
func TestLatestSleepStageDateNoData(t *testing.T) {
	db := openSleepDB(t)
	ctx := context.Background()

	got, err := db.LatestSleepStageDate(ctx)
	if err != nil {
		t.Fatalf("无数据不该报错: %v", err)
	}
	if got != "" {
		t.Fatalf("无分期数据应回空串，实际 %q", got)
	}

	// 有分期但没有 stage 字段的样本不算数（口径：meta->>'stage' 非空）
	if _, _, _, _, err := db.WriteSamples(ctx, 1, []sample.Sample{
		{Metric: "sleep_analysis", Source: "Apple Watch", StartTime: at0800(10, 1, 3, 0), EndTime: at0800(10, 1, 3, 30),
			Value: 0.5, Unit: "hr", LocalDate: "2026-10-01", TZOffsetMin: 480},
		{Metric: "sleep_analysis", Source: "Apple Watch", StartTime: at0800(10, 2, 3, 0), EndTime: at0800(10, 2, 3, 30),
			Value: 0.5, Unit: "hr", LocalDate: "2026-10-02", TZOffsetMin: 480, Meta: map[string]any{"stage": "AsleepCore"}},
	}, "strict"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	got, err = db.LatestSleepStageDate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026-10-02" {
		t.Fatalf("应取带 stage 的最新一天 2026-10-02，实际 %q", got)
	}
}

// TestNormalizeSleepStage 锁死阶段归一化的优先级：
// 新版 HealthKit 的 AsleepCore 同时含 asleep 与 core，必须先判具体阶段。
func TestNormalizeSleepStage(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"", "other"},
		{"   ", "other"},
		{"AsleepCore", "core"},
		{"AsleepDeep", "deep"},
		{"AsleepREM", "rem"},
		{"Asleep", "asleep"},
		{"Awake", "awake"},
		{"InBed", "inbed"},
		{"In Bed", "inbed"}, // 带空格的「在床」写法
		{"核心睡眠", "core"},
		{"深度睡眠", "deep"},
		{"快速眼动", "other"}, // 中文 REM 的写法当前认不出来：只认 "rem"。锁住现状，别偷偷改口径
		{"清醒", "awake"},
		{"在床", "inbed"},
		{"睡眠", "asleep"},
		{"WHOOP_STRAIN", "other"},
		{"  AWAKE  ", "awake"}, // 大小写与空白都要归一
	}
	for _, c := range cases {
		if got := normalizeSleepStage(c.raw); got != c.want {
			t.Fatalf("normalizeSleepStage(%q) = %q，期望 %q", c.raw, got, c.want)
		}
	}
}
