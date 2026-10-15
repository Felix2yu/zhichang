package hae

import (
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// 类别型指标按子类型分键导出时根本没有 qty——实测性生活的日汇总是
// {"Protection Used":0,"Protection Not Used":0,"Unspecified":1}。
// 只按已知字段取值会把这类记录整条判成无值并静默丢掉（一年 15 条）。
func TestNormalizeCategorySubtypes(t *testing.T) {
	raw := `{"data":{"metrics":[{"name":"sexual_activity","units":"count","data":[
		{"date":"2026-01-02 00:00:00 +0800","Protection Used":0,"Protection Not Used":0,"Unspecified":1,"source":"Aphrodite"},
		{"date":"2026-01-09 00:00:00 +0800","Unspecified":3,"source":"Aphrodite"},
		{"date":"2026-01-10 00:00:00 +0800","Unspecified":0,"source":"Aphrodite"}
	]}]}}`
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	if res.Skipped != 0 {
		t.Fatalf("子类型形态不该被丢弃，skipped=%d", res.Skipped)
	}
	if len(res.Unknown) != 0 {
		t.Fatalf("sexual_activity 已登记，不该报未登记指标：%v", res.Unknown)
	}
	if len(res.Samples) != 3 {
		t.Fatalf("应有 3 条样本，实际 %d", len(res.Samples))
	}
	want := map[string]float64{"2026-01-02": 1, "2026-01-09": 3, "2026-01-10": 0}
	for _, s := range res.Samples {
		d := s.LocalDate
		if s.Value != want[d] {
			t.Fatalf("%s 的值应为各子类型求和 %v，实际 %v", d, want[d], s.Value)
		}
		if s.Unit != "count" {
			t.Fatalf("单位应为字典口径 count，实际 %q", s.Unit)
		}
	}
	// 子类型明细留在 meta 里：合并成总数之后，只有它能回答「这 3 次分别是什么」
	sub, ok := res.Samples[0].Meta["subtypes"].(map[string]float64)
	if !ok || sub["Unspecified"] != 1 {
		t.Fatalf("子类型明细未随样本落库: %+v", res.Samples[0].Meta)
	}
}

// 已知字段不能被当成子类型再算一遍：反射出来的键清单一旦与字段漂移，
// 一个 {qty:5} 的点会变成 qty + subtypes 两份，值翻倍且不报错。
func TestNormalizeKnownKeysAreNotSubtypes(t *testing.T) {
	raw := `{"data":{"metrics":[{"name":"step_count","units":"count","data":[` +
		`{"date":"2026-01-02 00:00:00 +0800","qty":5,"Min":1,"Avg":3,"Max":9,"source":"iPhone"}]}]}}`
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	s := res.Samples[0]
	if s.Value != 5 {
		t.Fatalf("步数应取 qty=5，实际 %v", s.Value)
	}
	if _, leaked := s.Meta["subtypes"]; leaked {
		t.Fatalf("已知字段被当成未知子类型收走了: %+v", s.Meta)
	}
	// 非数字的未知键（备注、状态文本）不参与求和，也不进子类型
	raw2 := `{"data":{"metrics":[{"name":"step_count","units":"count","data":[` +
		`{"date":"2026-01-02 00:00:00 +0800","qty":5,"note":"手动补录","flag":{"a":1},"source":"iPhone"}]}]}}`
	res2 := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw2)
	if _, leaked := res2.Samples[0].Meta["subtypes"]; leaked {
		t.Fatalf("字符串/对象形态的未知键不该被当成计数: %+v", res2.Samples[0].Meta)
	}
}
