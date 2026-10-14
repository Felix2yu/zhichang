package hae

import (
	"sort"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// 别名收敛的回归：同一概念的上游旧名必须落进字典的规范 metric。
//
// 这条链路上错误是静默的——名字没收敛时样本照样写进 samples，接口照样返回 200，
// 只有打开界面才会发现卡片是空的（因为卡片的 metric 在字典里、而数据在另一个名字下）。
// 所以必须用断言把「旧名 → 规范名」钉死，而不是靠人工看一眼导出文件。
func TestNormalizeConvergesAliasNames(t *testing.T) {
	reg := metric.New(metric.Defaults())
	res := mustNormalize(t, reg, time.UTC, `{"data":{"metrics":[
		{"name":"blood_oxygen_saturation","units":"%","data":[{"date":"2026-10-01 08:00:00 +0800","qty":95.68,"source":"Apple Watch"}]},
		{"name":"apple_exercise_time","units":"min","data":[{"date":"2026-10-01 08:00:00 +0800","qty":40,"source":"Apple Watch"}]},
		{"name":"apple_stand_time","units":"min","data":[{"date":"2026-10-01 08:00:00 +0800","qty":120,"source":"Apple Watch"}]},
		{"name":"apple_stand_hour","units":"count","data":[{"date":"2026-10-01 08:00:00 +0800","qty":12,"source":"Apple Watch"}]},
		{"name":"distance_walking_running","units":"km","data":[{"date":"2026-10-01 08:00:00 +0800","qty":5.2,"source":"Apple Watch"}]},
		{"name":"weight_&_body_mass","units":"kg","data":[{"date":"2026-10-01 08:00:00 +0800","qty":68.5,"source":"Apple Watch"}]},
		{"name":"weight_body_mass","units":"kg","data":[{"date":"2026-10-02 08:00:00 +0800","qty":68.3,"source":"Apple Watch"}]},
		{"name":"apple_sleeping_wrist_temperature","units":"degC","data":[{"date":"2026-10-01 08:00:00 +0800","qty":35.3,"source":"Apple Watch"}]}
	]}}`)

	if len(res.Unknown) != 0 {
		t.Fatalf("别名指标不该被报成未登记指标（否则会被当成新指标）：%v", res.Unknown)
	}
	if res.Skipped != 0 {
		t.Fatalf("样本不该被丢弃，实际 skipped=%d", res.Skipped)
	}

	got := map[string][]float64{}
	for _, s := range res.Samples {
		got[s.Metric] = append(got[s.Metric], s.Value)
	}
	want := map[string]float64{
		"oxygen_saturation":          95.68,
		"exercise_time":              40,
		"stand_time":                 120,
		"stand_hour":                 12,
		"walking_running_distance":   5.2,
		"sleeping_wrist_temperature": 35.3,
	}
	for name, v := range want {
		vals, ok := got[name]
		if !ok {
			t.Fatalf("规范名 %q 下没有样本，实际落库的名字：%v", name, keysOf(got))
		}
		if len(vals) != 1 {
			t.Fatalf("规范名 %q 应有 1 条样本，实际 %d 条", name, len(vals))
		}
		almost(t, name, vals[0], v, 1e-9)
	}
	// 体重两种写法（weight_&_body_mass / weight_body_mass）都收敛到 body_mass：
	// 两条都要在，不能互相覆盖。
	if vals := got["body_mass"]; len(vals) != 2 || vals[0] != 68.5 || vals[1] != 68.3 {
		t.Fatalf("两种体重写法都应落进 body_mass（68.5 与 68.3），实际 %v", vals)
	}
	// 旧名不得残留成独立 metric
	for _, alias := range []string{
		"blood_oxygen_saturation", "apple_exercise_time", "apple_stand_time",
		"apple_stand_hour", "distance_walking_running", "weight_&_body_mass",
		"weight_body_mass", "apple_sleeping_wrist_temperature",
	} {
		if _, ok := got[alias]; ok {
			t.Fatalf("旧名 %q 不该作为独立 metric 落库", alias)
		}
	}
}

// 别名表的每一项都要能被字典认识——收录漏了会让数据继续落到孤儿名字上。
func TestNormalizeEveryAliasResolvesToRegistryEntry(t *testing.T) {
	reg := metric.New(metric.Defaults())
	for alias, canonical := range metric.Aliases() {
		if !reg.IsKnown(metric.Canonical(alias)) {
			t.Errorf("别名 %q 收敛到 %q，字典里没有这个指标", alias, canonical)
		}
	}
}

// 真的用真实导出文件里的旧名形态跑一遍：上面那些是手写样例，
// 这一条防止「样例写得比真实数据好看」。
func TestNormalizeRealExportAliasNamesUseCanonicalMetric(t *testing.T) {
	reg := metric.New(metric.Defaults())
	res := mustNormalize(t, reg, time.UTC, `{"data":{"metrics":[
		{"name":"blood_oxygen_saturation","units":"%","data":[
			{"date":"2026-10-01 00:00:00 +0800","qty":95.68181818181819,"source":"子翼的Apple\u00a0Watch"}]}
	]}}`)
	if len(res.Samples) != 1 {
		t.Fatalf("应有 1 条样本，实际 %d", len(res.Samples))
	}
	s := res.Samples[0]
	if s.Metric != "oxygen_saturation" {
		t.Fatalf("血氧必须落到 oxygen_saturation，实际 %q", s.Metric)
	}
	// 来源名里的 NBSP 要归一成普通空格：真实导出里所有 Watch 点位都是 NBSP，
	// 不归一的话同一台设备会在 sources 页显示成两条「看起来一样」的记录。
	if s.Source != "子翼的Apple Watch" {
		t.Fatalf("来源名的不可见空白应归一，实际 %q", s.Source)
	}
	almost(t, "血氧值", s.Value, 95.68181818181819, 1e-9)
}

func keysOf(m map[string][]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
