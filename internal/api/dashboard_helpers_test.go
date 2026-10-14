package api

import (
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// splitList 是 metrics=/list 参数的唯一解析入口：前端会传 "a, b,"，
// 空串和纯空白必须回 nil（而不是 []string{""}），否则「留空=全部指标」的判定会失效。
func TestSplitList(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"空串", "", nil},
		{"纯空白", "   ", nil},
		{"只有逗号", ",,", nil},
		{"单个", "step_count", []string{"step_count"}},
		{"带空格", " step_count , heart_rate ", []string{"step_count", "heart_rate"}},
		{"多余逗号", "step_count,,heart_rate,", []string{"step_count", "heart_rate"}},
		{"中文值", "步数,心率", []string{"步数", "心率"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitList(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("得到 %v，期望 %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("第 %d 项 = %q，期望 %q", i, got[i], c.want)
				}
			}
		})
	}
}

// 缺省区间是页面首次进来的视野：概览/趋势看 30 天、明细看 7 天。
// 起止都按传入时区算，不能跟着服务器时区跑。
func TestDefaultRange(t *testing.T) {
	d := &Dashboard{loc: homeLoc}

	cases := []struct {
		name string
		from string
		to   string
		days int
	}{
		{"缺省 30 天", "", "", 30},
		{"缺省 7 天", "", "", 7},
		{"只缺 from", "", "2026-05-10", 30},
		{"只缺 to", "2026-05-01", "", 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from, to := d.defaultRange(c.from, c.to, c.days)
			if c.from == "" {
				want := time.Now().In(homeLoc).AddDate(0, 0, -c.days+1).Format("2006-01-02")
				if from != want {
					t.Fatalf("from = %q，期望 %q", from, want)
				}
			} else if from != c.from {
				t.Fatalf("显式 from 不该被改写: %q", from)
			}
			if c.to == "" {
				if want := time.Now().In(homeLoc).Format("2006-01-02"); to != want {
					t.Fatalf("to = %q，期望 %q", to, want)
				}
			} else if to != c.to {
				t.Fatalf("显式 to 不该被改写: %q", to)
			}
			if c.from == "" && c.to == "" && from > to {
				t.Fatalf("缺省区间反了: %q > %q", from, to)
			}
		})
	}

	t.Run("显式区间原样返回", func(t *testing.T) {
		from, to := d.defaultRange("2026-01-01", "2026-01-31", 30)
		if from != "2026-01-01" || to != "2026-01-31" {
			t.Fatalf("得到 %q~%q", from, to)
		}
	})

	// 跨零点的深夜：loc 用 +0800 时「今天」可能与服务器 UTC 的今天不同
	t.Run("按本地时区取今天", func(t *testing.T) {
		utc := &Dashboard{loc: time.UTC}
		_, toUTC := utc.defaultRange("", "", 30)
		_, toLocal := d.defaultRange("", "", 30)
		if toLocal != time.Now().In(homeLoc).Format("2006-01-02") {
			t.Fatalf("toLocal = %q", toLocal)
		}
		if toUTC != time.Now().UTC().Format("2006-01-02") {
			t.Fatalf("toUTC = %q", toUTC)
		}
	})
}

// metrics 留空 = 字典里全部「启用」指标：用户下线的指标不该被顺手画出来；
// 显式传的名单里，未登记的名字要被丢掉而不是报错（前端可能带着旧字典的键）。
func TestResolveMetrics(t *testing.T) {
	reg := metric.New([]metric.Def{
		{Metric: "step_count", Enabled: true},
		{Metric: "heart_rate", Enabled: true},
		{Metric: "legacy_chart", Enabled: false},
	})
	d := &Dashboard{metric: reg}

	if got := d.resolveMetrics(""); len(got) != 2 {
		t.Fatalf("留空应展开为 2 个启用指标，得到 %v", got)
	}
	for _, m := range d.resolveMetrics("") {
		if m == "legacy_chart" {
			t.Fatal("禁用指标不应被展开")
		}
	}
	if got := d.resolveMetrics("step_count, nosuch, heart_rate"); len(got) != 2 ||
		got[0] != "step_count" || got[1] != "heart_rate" {
		t.Fatalf("显式名单应保留顺序并丢掉未登记项: %v", got)
	}
	if got := d.resolveMetrics("nosuch,alsonosuch"); len(got) != 0 {
		t.Fatalf("全是不明指标时应回空切片（HTTP 层据此报 400）: %v", got)
	}
	// 空白串等价于留空：必须由 splitList 先判空，否则会当成「用户点名了空指标」
	if got := d.resolveMetrics(" , "); len(got) != 2 {
		t.Fatalf("空白名单应展开为全部启用指标: %v", got)
	}
}

// groupByMetric 给每个请求的指标都留一个键：前端直接 series[metric].map()，
// 缺键就是 undefined，比空数组更容易崩。
func TestGroupByMetric(t *testing.T) {
	pts := []postgres.DailyPoint{
		{Metric: "step_count", Date: "2026-05-09", Value: 5000},
		{Metric: "step_count", Date: "2026-05-10", Value: 8000},
		{Metric: "heart_rate", Date: "2026-05-10", Value: 72},
	}
	got := groupByMetric(pts, []string{"step_count", "heart_rate", "body_mass"})

	if len(got) != 3 {
		t.Fatalf("每个请求指标都要有键: %v", got)
	}
	if len(got["step_count"]) != 2 || got["step_count"][0].Date != "2026-05-09" {
		t.Fatalf("同一指标的点应聚在一起并保持顺序: %v", got["step_count"])
	}
	if v, ok := got["body_mass"]; !ok || v == nil || len(v) != 0 {
		t.Fatalf("无数据指标必须是空切片而不是 nil（否则 JSON 出 null）: %#v", v)
	}

	empty := groupByMetric(nil, nil)
	if len(empty) != 0 {
		t.Fatalf("无请求指标时 series 应为空对象: %v", empty)
	}
}
