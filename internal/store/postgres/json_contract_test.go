package postgres

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEmptyRowsEncodeAsArray 锁定 JSON 契约：空结果集必须编码为 []，不能是 null。
//
// 背景：nil slice 会被 encoding/json 编成 null。前端拿到 null 后直接
// .map() / 展开 / .at() 会抛 TypeError，SvelteKit 错误边界接管后整页变成
// 500 Internal Error。生产空库必然触发，本地有数据时反而看不到。
//
// 这些用例刻意构造「非 nil 的空 slice」，与 store 层各查询的返回写法一致；
// TestNilSliceEncodesAsNull 是对照组，确保本测试仍能区分 nil 与空 slice。
func TestEmptyRowsEncodeAsArray(t *testing.T) {
	cases := []struct {
		name string
		got  any
		want string
	}{
		{"SleepRange", map[string]any{"rows": []SleepRow{}}, `{"rows":[]}`},
		{"WorkoutRange", map[string]any{"rows": []WorkoutRow{}}, `{"rows":[]}`},
		{"SampleRow", map[string]any{"rows": []SampleRow{}}, `{"rows":[]}`},
		{"SourceRow", map[string]any{"sources": []SourceRow{}}, `{"sources":[]}`},
		{"DailyPoint", map[string]any{"series": map[string][]DailyPoint{"heart_rate": {}}}, `{"series":{"heart_rate":[]}}`},
		{"SyncStatus", SyncStatus{
			RecentRuns:  []RunBrief{},
			Watermarks:  []WatermarkRow{},
			MissingDays: []string{},
		}, `{"recent_runs":[],"watermarks":[],"missing_days":[]}`},
		// 睡眠分期时间线：segments / totals / physiology 在无数据时也必须是 [] / {}
		{"SleepStages", SleepStages{
			Segments:   []SleepStageSeg{},
			Totals:     map[string]float64{},
			Physiology: []NightSeries{},
		}, `{"local_date":"","source":"","tz_offset_min":0,"has_session":false,"segments":[],"totals":{},"physiology":[]}`},
		{"NightMetricRow", map[string]any{"rows": []NightMetricRow{}}, `{"rows":[]}`},
		{"SleepStagesRange", map[string]any{"rows": []NightStages{}}, `{"rows":[]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.got)
			if err != nil {
				t.Fatalf("编码失败: %v", err)
			}
			if strings.Contains(string(b), "null") {
				t.Fatalf("响应含 null，前端会崩：%s", b)
			}
			if string(b) != c.want {
				t.Fatalf("实际 %s，期望 %s", b, c.want)
			}
		})
	}
}

// TestNilSliceEncodesAsNull 是对照组：若哪天有人把某处改回 var out []Xxx，
// 这里必须仍然编成 null，说明上面的断言确实能区分两者。
func TestNilSliceEncodesAsNull(t *testing.T) {
	var nilRows []SleepRow
	b, err := json.Marshal(map[string]any{"rows": nilRows})
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	if string(b) != `{"rows":null}` {
		t.Fatalf("对照组失效，nil slice 应编成 null，实际 %s", b)
	}
}

// emptyRows 是一条没有任何结果的 pgx.Rows 替身。
type emptyRows struct{}

func (emptyRows) Next() bool             { return false }
func (emptyRows) Scan(dest ...any) error { return nil }
func (emptyRows) Err() error             { return nil }
func (emptyRows) Close()                 {}

// TestCollectDailyEmptyIsNotNil 直接驱动真实的 collectDaily：
// 空结果集时返回值必须是「非 nil 的空 slice」，否则前端拿到 JSON null 就会崩。
func TestCollectDailyEmptyIsNotNil(t *testing.T) {
	out, err := collectDaily(emptyRows{})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if out == nil {
		t.Fatal("空结果集返回了 nil slice，会被编码成 JSON null")
	}
	if len(out) != 0 {
		t.Fatalf("期望空结果，实际 %d 行", len(out))
	}
	b, err := json.Marshal(map[string]any{"rows": out})
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	if string(b) != `{"rows":[]}` {
		t.Fatalf("实际 %s，期望 {\"rows\":[]}", b)
	}
}
