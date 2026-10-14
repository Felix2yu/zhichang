package metric

import (
	"testing"
)

func sampleDefs() []Def {
	return []Def{
		def("step_count", "步数", KindCumulative, DayAggSum, CrossAggMax, "count", "qty", "bar", 10),
		def("heart_rate", "心率", KindInstant, DayAggMean, CrossAggMean, "bpm", "Avg", "line", 110),
		{Metric: "archived_metric", DisplayNameZH: "已下线", Kind: KindInstant, DayAgg: DayAggMean,
			CrossSourceAgg: CrossAggMean, Unit: "x", ValueField: "qty", Chart: "line", Enabled: false, SortOrder: 999},
	}
}

func TestRegistry(t *testing.T) {
	r := New(sampleDefs())

	if got := len(r.All()); got != 3 {
		t.Fatalf("All 应返回全部 3 条，实际 %d", got)
	}
	// Enabled 过滤掉字典里手工置 false 的项，而不是全部。
	if got := len(r.Enabled()); got != 2 {
		t.Fatalf("Enabled 应只含启用项，实际 %d 条", got)
	}
	for _, d := range r.Enabled() {
		if !d.Enabled {
			t.Fatalf("Enabled() 混入未启用指标: %s", d.Metric)
		}
	}

	cases := []struct {
		name    string
		wantOK  bool
		wantZH  string
		wantSet bool // 未知指标 MustGet 返回零值而不是 panic
	}{
		{"step_count", true, "步数", true},
		{"heart_rate", true, "心率", true},
		{"archived_metric", true, "已下线", true},
		{"unknown_metric", false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := r.Get(tc.name)
			if ok != tc.wantOK {
				t.Fatalf("Get(%q) ok = %v，期望 %v", tc.name, ok, tc.wantOK)
			}
			if ok && d.DisplayNameZH != tc.wantZH {
				t.Fatalf("Get(%q) 中文名 = %q，期望 %q", tc.name, d.DisplayNameZH, tc.wantZH)
			}
			if r.IsKnown(tc.name) != tc.wantOK {
				t.Fatalf("IsKnown(%q) = %v，期望 %v", tc.name, r.IsKnown(tc.name), tc.wantOK)
			}
			// MustGet 对未知指标返回零值：调用点用它做展示，绝不能 panic 打挂请求。
			m := r.MustGet(tc.name)
			if tc.wantSet {
				if m.Metric != tc.name {
					t.Fatalf("MustGet(%q) = %+v", tc.name, m)
				}
			} else if m != (Def{}) {
				t.Fatalf("MustGet 未知指标应返回零值，实际 %+v", m)
			}
		})
	}
}

// 同名指标后写入者胜出：字典以 metric 为主键，seed/构建顺序变化不该让查询结果不确定。
func TestRegistryLastDefWins(t *testing.T) {
	r := New([]Def{
		def("step_count", "旧名", KindCumulative, DayAggSum, CrossAggMax, "count", "qty", "bar", 10),
		def("step_count", "步数", KindCumulative, DayAggSum, CrossAggMax, "count", "qty", "bar", 11),
	})
	d := r.MustGet("step_count")
	if d.DisplayNameZH != "步数" || d.SortOrder != 11 {
		t.Fatalf("同名应后者胜出，实际 %+v", d)
	}
	if len(r.All()) != 1 {
		t.Fatalf("同名不应重复计入，实际 %d 条", len(r.All()))
	}
}

func TestRegistryEmpty(t *testing.T) {
	r := New(nil)
	if len(r.All()) != 0 || len(r.Enabled()) != 0 {
		t.Fatal("空字典应返回空切片")
	}
	if r.IsKnown("step_count") {
		t.Fatal("空字典不应认识任何指标")
	}
	if _, ok := r.Get("step_count"); ok {
		t.Fatal("空字典 Get 应返回 false")
	}
}

// 字典与数据库之间唯一的隐式契约：migrations 里 metric_defs 的 CHECK 约束。
// 写错要等到 seed 时才炸，所以在这里先钉住。
func TestDefaultsAggValuesMatchMigrationCheckConstraints(t *testing.T) {
	kinds := map[Kind]bool{KindCumulative: true, KindInstant: true, KindInterval: true, KindCategorical: true}
	dayAggs := map[DayAgg]bool{DayAggSum: true, DayAggMean: true, DayAggMax: true, DayAggMin: true, DayAggLast: true, DayAggCount: true}
	crossAggs := map[CrossAgg]bool{CrossAggMax: true, CrossAggMean: true, CrossAggSum: true, CrossAggLatest: true}

	for _, d := range Defaults() {
		if !kinds[d.Kind] {
			t.Errorf("%s: kind %q 不在 CHECK 允许集合内", d.Metric, d.Kind)
		}
		if !dayAggs[d.DayAgg] {
			t.Errorf("%s: day_agg %q 不在 CHECK 允许集合内", d.Metric, d.DayAgg)
		}
		if !crossAggs[d.CrossSourceAgg] {
			t.Errorf("%s: cross_source_agg %q 不在 CHECK 允许集合内", d.Metric, d.CrossSourceAgg)
		}
	}
}

func TestDefaults(t *testing.T) {
	all := Defaults()
	if len(all) == 0 {
		t.Fatal("内置字典不应为空")
	}

	seen := map[string]bool{}
	dupDisplay := map[string][]string{}
	for _, d := range all {
		if !d.Enabled {
			t.Errorf("%s: 内置指标 Enabled 必须为 true（否则前端图表整块消失）", d.Metric)
		}
		if d.Metric == "" || d.DisplayNameZH == "" || d.Unit == "" || d.ValueField == "" || d.Chart == "" {
			t.Errorf("内置指标存在空字段: %+v", d)
		}
		// def() 的不变式：preferred_unit 与 unit 相同，单位换算从同值起步。
		if d.PreferredUnit != d.Unit {
			t.Errorf("%s: PreferredUnit %q != Unit %q", d.Metric, d.PreferredUnit, d.Unit)
		}
		if seen[d.Metric] {
			t.Errorf("%s: metric 主键重复", d.Metric)
		}
		seen[d.Metric] = true
		dupDisplay[d.DisplayNameZH] = append(dupDisplay[d.DisplayNameZH], d.Metric)
	}

	// 中文名重复是刻意的：HAE 对同一概念有两种导出名，两者都要能落进同一口径。
	pairs := map[string]string{
		"体重":      "weight_&_body_mass",
		"步行+跑步距离": "distance_walking_running",
	}
	for zh, alias := range pairs {
		names := dupDisplay[zh]
		if len(names) != 2 {
			t.Fatalf("中文名 %q 应有 2 个指标（原始名 + HAE 别名），实际 %v", zh, names)
		}
		found := false
		for _, n := range names {
			if n == alias {
				found = true
			}
		}
		if !found {
			t.Fatalf("中文名 %q 的别名 %q 缺失，实际 %v", zh, alias, names)
		}
	}
	// 除上述有意重复外，其余中文名唯一。
	for zh, names := range dupDisplay {
		if len(names) > 2 {
			t.Errorf("中文名 %q 被 %d 个指标共用: %v", zh, len(names), names)
		}
		if len(names) == 2 {
			if _, ok := pairs[zh]; !ok {
				t.Errorf("未预期的重复中文名 %q: %v", zh, names)
			}
		}
	}
}

// Defaults() 每次返回新切片：调用方排序或就地修改不能污染字典。
func TestDefaultsReturnsFreshSlice(t *testing.T) {
	first := Defaults()
	first[0].DisplayNameZH = "被改坏了"
	first[0].Enabled = false
	second := Defaults()
	if second[0].DisplayNameZH != "步数" || !second[0].Enabled {
		t.Fatalf("Defaults 被外部修改污染: %+v", second[0])
	}
}
