package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// 空库是生产刚部署完的必然状态：这一组用例锁住「只读接口一律回 []，不能回 null」。
// 前端按数组直接 .map() / 展开，拿到 null 会整页 500。
func TestEmptyDatabaseReturnsArrays(t *testing.T) {
	r, _ := newRouter(t, "")

	cases := []struct {
		name string
		path string
		keys []string
	}{
		{"概览", "/api/summary", []string{"items"}},
		{"明细", "/api/samples?metric=step_count", []string{"rows"}},
		{"睡眠", "/api/sleep", []string{"rows"}},
		{"睡眠分期矩阵", "/api/sleep/stages/range", []string{"rows"}},
		{"夜间生理指标", "/api/sleep/night-metrics", []string{"rows"}},
		{"运动", "/api/workouts", []string{"rows"}},
		{"来源", "/api/sources", []string{"sources"}},
		{"目标", "/api/goals", []string{"goals"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := get(t, r, c.path)
			if got.status != http.StatusOK {
				t.Fatalf("状态码 = %d，期望 200（body=%s）", got.status, got.body)
			}
			for _, k := range c.keys {
				if len(got.arr(t, k)) != 0 {
					t.Fatalf("空库时 %q 应为空数组: %s", k, got.body)
				}
			}
		})
	}

	t.Run("天级序列：series 每个指标都挂空数组", func(t *testing.T) {
		got := get(t, r, "/api/daily?metrics=step_count,heart_rate")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		series, ok := got.fields["series"].(map[string]any)
		if !ok {
			t.Fatalf("series 不是对象: %s", got.body)
		}
		// 无数据的指标也必须出现在 series 里：前端按指标名取键，缺键就崩
		for _, m := range []string{"step_count", "heart_rate"} {
			v, ok := series[m]
			if !ok {
				t.Fatalf("series 缺少指标 %q: %s", m, got.body)
			}
			if arr, ok := v.([]any); !ok || len(arr) != 0 {
				t.Fatalf("series[%q] 应为 []: %v", m, v)
			}
		}
	})

	t.Run("同步状态：三个列表字段都不能是 null", func(t *testing.T) {
		got := get(t, r, "/api/sync/status")
		for _, k := range []string{"recent_runs", "watermarks", "missing_days"} {
			// missing_days 在非空库里本来就有内容（缺口区间跟着 CURRENT_DATE 走），
			// 所以这里只锁「必须是数组」而不是「必须是空」
			if _, ok := got.fields[k].([]any); !ok {
				t.Fatalf("%q 必须是数组而不是 null: %s", k, got.body)
			}
		}
		if len(got.arr(t, "recent_runs")) != 0 || len(got.arr(t, "watermarks")) != 0 {
			t.Fatalf("空库时运行与水位应为空: %s", got.body)
		}
	})

	t.Run("单晚分期：没有分期数据时回 200 零值而不是 404", func(t *testing.T) {
		got := get(t, r, "/api/sleep/stages")
		if got.status != http.StatusOK {
			t.Fatalf("没有分期数据也必须 200（前端据此显示占位提示），实际 %d", got.status)
		}
		if got.str(t, "local_date") != "" || got.fields["has_session"] != false {
			t.Fatalf("零值结构不符: %s", got.body)
		}
		if len(got.arr(t, "segments")) != 0 || len(got.arr(t, "physiology")) != 0 {
			t.Fatalf("segments/physiology 应为空数组: %s", got.body)
		}
		if totals, ok := got.fields["totals"].(map[string]any); !ok || len(totals) != 0 {
			t.Fatalf("totals 应为空对象而不是 null: %s", got.body)
		}
	})
}

// 参数格式与未登记指标必须在 HTTP 层就挡下：400 是「请求有问题」，
// 500 才是服务端故障，两者不能混——混了运维就分不清该改前端还是查后端。
func TestBadParamsAndUnknownMetrics(t *testing.T) {
	r, _ := newRouter(t, "")
	cases := []struct {
		name string
		path string
		want int
	}{
		{"概览：月份越界", "/api/summary?date=2026-13-01", http.StatusBadRequest},
		{"概览：用了斜杠格式", "/api/summary?date=2026/10/01", http.StatusBadRequest},
		{"概览：完全不是日期", "/api/summary?date=today", http.StatusBadRequest},
		{"天级：from 非法", "/api/daily?from=abc&metrics=step_count", http.StatusBadRequest},
		{"天级：to 日期不存在", "/api/daily?to=2026-02-30&metrics=step_count", http.StatusBadRequest},
		{"天级：指标全是不明的", "/api/daily?metrics=nosuchmetric", http.StatusBadRequest},
		{"明细：指标未登记", "/api/samples?metric=nosuchmetric", http.StatusBadRequest},
		{"明细：漏了 metric", "/api/samples", http.StatusBadRequest},
		{"明细：from 非法（存储层报错）", "/api/samples?metric=step_count&from=abc", http.StatusInternalServerError},
		{"单晚分期：date 非法", "/api/sleep/stages?date=abc", http.StatusBadRequest},
		{"分期矩阵：from 非法", "/api/sleep/stages/range?from=abc", http.StatusBadRequest},
		{"睡眠：to 非法", "/api/sleep?to=abc", http.StatusInternalServerError},
		{"运动：from 非法", "/api/workouts?from=abc", http.StatusInternalServerError},
		{"夜间生理：from 非法", "/api/sleep/night-metrics?from=abc", http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			get(t, r, c.path).wantErr(t, c.want)
		})
	}

	t.Run("混入不明指标时只丢不明项而不是整体报错", func(t *testing.T) {
		got := get(t, r, "/api/daily?metrics=step_count,nosuchmetric")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		metrics, ok := got.fields["metrics"].([]any)
		if !ok || len(metrics) != 1 || metrics[0] != "step_count" {
			t.Fatalf("metrics 应只剩已登记项: %s", got.body)
		}
	})
}

// 上下文取消 = 存储层必然报错。用它来逐条打中「查询失败」分支，
// 比伪造 SQL 错误更接近真实故障。
func TestStoreErrorsMapToHandlerStatus(t *testing.T) {
	r, _ := newRouter(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name string
		path string
		want int
	}{
		{"概览", "/api/summary", http.StatusBadRequest},
		{"天级", "/api/daily?metrics=step_count", http.StatusBadRequest},
		{"明细", "/api/samples?metric=step_count", http.StatusInternalServerError},
		{"睡眠", "/api/sleep", http.StatusInternalServerError},
		{"单晚分期（先找最近一晚）", "/api/sleep/stages", http.StatusInternalServerError},
		{"单晚分期（深链指定日期）", "/api/sleep/stages?date=2026-05-10", http.StatusBadRequest},
		{"分期矩阵", "/api/sleep/stages/range", http.StatusBadRequest},
		{"夜间生理", "/api/sleep/night-metrics", http.StatusInternalServerError},
		{"运动", "/api/workouts", http.StatusInternalServerError},
		{"来源", "/api/sources", http.StatusInternalServerError},
		{"目标列表", "/api/goals", http.StatusInternalServerError},
		{"同步状态", "/api/sync/status", http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			getWith(t, r, c.path, ctx).wantErr(t, c.want)
		})
	}
}

func TestListMetrics(t *testing.T) {
	r, _ := newRouter(t, "")
	got := get(t, r, "/api/metrics")
	if got.status != http.StatusOK {
		t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
	}
	// 页面所有本地日期都靠这个字段回显，缺了就只能拿浏览器时区猜
	if got.str(t, "timezone") != homeTZ {
		t.Fatalf("timezone = %q，期望 %q", got.str(t, "timezone"), homeTZ)
	}
	metrics := got.arr(t, "metrics")
	if len(metrics) == 0 {
		t.Fatal("字典为空，页面将没有任何指标可画")
	}
	var step map[string]any
	for _, raw := range metrics {
		m, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("metrics 元素不是对象: %v", raw)
		}
		if m["metric"] == "step_count" {
			step = m
		}
	}
	if step == nil {
		t.Fatal("字典里应有 step_count")
	}
	if step["display_name"] != "步数" || step["display_name_zh"] != "步数" {
		t.Fatalf("中文名应从字典带出: %v", step)
	}
	if step["preferred_unit"] != "count" || step["kind"] != "cumulative" {
		t.Fatalf("口径字段应原样暴露给前端: %v", step)
	}
}

func TestSummaryWithRealData(t *testing.T) {
	today, yesterday := dateInLoc(0), dateInLoc(-1)
	r, db := newRouter(t, "")

	addSamples(t, db, newRun(t, db),
		stepSample(yesterday, 9, 0, 5000, "iPhone"),
		stepSample(today, 9, 0, 8000, "iPhone"),
		stepSample(today, 18, 0, 2000, "iPhone"),
	)
	// 字典里没有的指标：中文名必须回落到 metric 键本身，不能空着。
	// 只能直接塞表——写接口都会先过字典，造不出这种脏数据。
	if _, err := db.Pool.Exec(context.Background(),
		`INSERT INTO daily_metrics_day (local_date, metric, value, sample_count, source_count)
		 VALUES ($1, 'mystery_metric', 1, 1, 1)`, today); err != nil {
		t.Fatalf("塞入未登记指标失败: %v", err)
	}
	recompute(t, db, []string{today, yesterday}, []string{"step_count"})

	t.Run("缺省取今天并带前日对比", func(t *testing.T) {
		got := get(t, r, "/api/summary")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		if got.str(t, "date") != today || got.str(t, "today") != today {
			t.Fatalf("date/today = %q/%q，期望 %q", got.str(t, "date"), got.str(t, "today"), today)
		}
		if got.str(t, "timezone") != homeTZ {
			t.Fatalf("timezone = %q", got.str(t, "timezone"))
		}
		item := firstItemByMetric(t, got.arr(t, "items"), "step_count")
		if item["value"] != float64(10000) {
			t.Fatalf("今日步数应为 10000，实际 %v", item["value"])
		}
		if item["prev_value"] != float64(5000) || item["delta"] != float64(5000) || item["delta_pct"] != float64(100) {
			t.Fatalf("前日对比口径不符: %v", item)
		}
		if item["display_name"] != "步数" || item["unit"] != "count" {
			t.Fatalf("中文名与单位应从字典补全: %v", item)
		}
		if s, ok := item["updated_at"].(string); !ok || s == "" {
			t.Fatalf("updated_at 应可读: %v", item)
		}
		// 现状记录：/api/summary 对字典里没有的指标不回退，display_name 与 unit 都是空串
		// （/api/goals 同场景会回落成指标名，两处口径不一致）。断言锁住实际行为，
		// 哪天补上回退逻辑这条会红，届时应该改成期望指标名。
		other := firstItemByMetric(t, got.arr(t, "items"), "mystery_metric")
		if other["display_name"] != "" || other["unit"] != "" {
			t.Fatalf("未登记指标当前不回退中文名: %v", other)
		}
	})

	t.Run("metrics 过滤只在指定时生效", func(t *testing.T) {
		got := get(t, r, "/api/summary?metrics=step_count")
		items := got.arr(t, "items")
		if len(items) != 1 {
			t.Fatalf("过滤后只应剩 step_count: %s", got.body)
		}
		firstItemByMetric(t, items, "step_count")
	})

	t.Run("带空格的列表等价于不带", func(t *testing.T) {
		got := get(t, r, "/api/summary?metrics=%20step_count%20,")
		items := got.arr(t, "items")
		if len(items) != 1 {
			t.Fatalf("splitList 应裁掉空白与多余逗号: %s", got.body)
		}
	})

	t.Run("指定历史日期时无前一日数据则不给差值", func(t *testing.T) {
		got := get(t, r, "/api/summary?date="+yesterday)
		if got.str(t, "date") != yesterday {
			t.Fatalf("date 应回显请求值: %s", got.body)
		}
		item := firstItemByMetric(t, got.arr(t, "items"), "step_count")
		if item["value"] != float64(5000) {
			t.Fatalf("昨日步数应为 5000: %v", item)
		}
		if item["prev_value"] != nil || item["delta"] != nil || item["delta_pct"] != nil {
			t.Fatalf("没有前日数据时差值应为 null: %v", item)
		}
	})
}

func firstItemByMetric(t *testing.T, items []any, metricName string) map[string]any {
	t.Helper()
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("items 元素不是对象: %v", raw)
		}
		if item["metric"] == metricName {
			return item
		}
	}
	t.Fatalf("items 中没有 %q: %v", metricName, items)
	return nil
}

func TestDailyWithRealData(t *testing.T) {
	today, yesterday := dateInLoc(0), dateInLoc(-1)
	r, db := newRouter(t, "")
	addSamples(t, db, newRun(t, db),
		stepSample(yesterday, 9, 0, 5000, "iPhone"),
		stepSample(today, 9, 0, 8000, "iPhone"),
	)
	recompute(t, db, []string{today, yesterday}, []string{"step_count", "heart_rate"})

	t.Run("指定区间：按指标分组的天级点", func(t *testing.T) {
		got := get(t, r, "/api/daily?metrics=step_count&from="+yesterday+"&to="+today)
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		if got.str(t, "from") != yesterday || got.str(t, "to") != today {
			t.Fatalf("区间未回显: %s", got.body)
		}
		if got.str(t, "timezone") != homeTZ {
			t.Fatalf("timezone = %q", got.str(t, "timezone"))
		}
		series := got.fields["series"].(map[string]any)
		points, ok := series["step_count"].([]any)
		if !ok || len(points) != 2 {
			t.Fatalf("step_count 应有两天: %s", got.body)
		}
		first := points[0].(map[string]any)
		if first["date"] != yesterday || first["value"] != float64(5000) {
			t.Fatalf("天级点内容不符: %v", first)
		}
		if first["sample_count"] != float64(1) {
			t.Fatalf("sample_count 应为 1: %v", first)
		}
		// 没请求的指标不该被顺手带出来（多一次序列化，前端也会画出多余的线）
		if _, leaked := series["heart_rate"]; leaked {
			t.Fatalf("series 不应包含未请求的指标: %s", got.body)
		}
	})

	t.Run("缺省区间是最近 30 天", func(t *testing.T) {
		got := get(t, r, "/api/daily?metrics=step_count")
		if got.str(t, "from") != dateInLoc(-29) || got.str(t, "to") != today {
			t.Fatalf("缺省区间 = %q~%q，期望最近 30 天", got.str(t, "from"), got.str(t, "to"))
		}
	})

	t.Run("metrics 留空即全部启用指标", func(t *testing.T) {
		got := get(t, r, "/api/daily")
		metrics := got.arr(t, "metrics")
		if len(metrics) == 0 {
			t.Fatal("留空时应展开为字典里全部启用指标")
		}
		series := got.fields["series"].(map[string]any)
		if len(series) != len(metrics) {
			t.Fatalf("series 键数 %d 与 metrics 数 %d 不一致", len(series), len(metrics))
		}
		for _, m := range metrics {
			name := m.(string)
			if _, ok := series[name].([]any); !ok {
				t.Fatalf("series 缺少指标 %q 或其为 null", name)
			}
		}
	})
}

func TestSamplesWithRealData(t *testing.T) {
	today, yesterday := dateInLoc(0), dateInLoc(-1)
	r, db := newRouter(t, "")
	addSamples(t, db, newRun(t, db),
		sample.Sample{Metric: "heart_rate", Source: "Apple Watch", StartTime: wallAt(today, 8, 0).UTC(),
			EndTime: wallAt(today, 8, 5).UTC(), Value: 72,
			Unit: "bpm", LocalDate: today, TZOffsetMin: 480},
		sample.Sample{Metric: "heart_rate", Source: "Apple Watch", StartTime: wallAt(today, 9, 0).UTC(),
			Value: 76, ValueMin: ptr(60), ValueMax: ptr(90),
			Unit: "bpm", LocalDate: today, TZOffsetMin: 480},
		sample.Sample{Metric: "heart_rate", Source: "iPhone", StartTime: wallAt(yesterday, 9, 0).UTC(),
			Value: 70, Unit: "bpm", LocalDate: yesterday, TZOffsetMin: 480},
	)

	t.Run("limit 生效且按时间倒序", func(t *testing.T) {
		got := get(t, r, "/api/samples?metric=heart_rate&from="+today+"&to="+today+"&limit=1")
		if got.str(t, "metric") != "heart_rate" {
			t.Fatalf("metric 未回显: %s", got.body)
		}
		rows := got.arr(t, "rows")
		if len(rows) != 1 {
			t.Fatalf("limit=1 只应回 1 行: %s", got.body)
		}
		row := rows[0].(map[string]any)
		if row["value"] != float64(76) || row["local_date"] != today {
			t.Fatalf("应取最近一条: %v", row)
		}
		if row["min"] != float64(60) || row["max"] != float64(90) {
			t.Fatalf("有区间量的样本要带 min/max: %v", row)
		}
		if row["start_time"] != wallAt(today, 9, 0).UTC().Format(time.RFC3339) {
			t.Fatalf("start_time 必须是 UTC RFC3339: %v", row["start_time"])
		}
	})

	t.Run("缺省区间是最近 7 天", func(t *testing.T) {
		got := get(t, r, "/api/samples?metric=heart_rate")
		if got.str(t, "from") != dateInLoc(-6) || got.str(t, "to") != today {
			t.Fatalf("缺省区间 = %q~%q，期望最近 7 天", got.str(t, "from"), got.str(t, "to"))
		}
		rows := got.arr(t, "rows")
		if len(rows) != 3 {
			t.Fatalf("7 天内应有 3 条: %s", got.body)
		}
		// end_time 原样透传（前端算持续时长靠它），没有区间量的样本不带 min/max
		for _, raw := range rows {
			row := raw.(map[string]any)
			if row["value"] == float64(72) {
				if row["end_time"] != wallAt(today, 8, 5).UTC().Format(time.RFC3339) {
					t.Fatalf("end_time 不符: %v", row)
				}
				if _, has := row["min"]; has {
					t.Fatalf("无区间量时不应出现 min 键: %v", row)
				}
			}
		}
	})

	t.Run("limit 非法时回落到上限而不是报错", func(t *testing.T) {
		got := get(t, r, "/api/samples?metric=heart_rate&limit=0")
		if got.status != http.StatusOK || len(got.arr(t, "rows")) != 3 {
			t.Fatalf("limit=0 应视为不限: %s", got.body)
		}
		got = get(t, r, "/api/samples?metric=heart_rate&limit=abc")
		if got.status != http.StatusOK || len(got.arr(t, "rows")) != 3 {
			t.Fatalf("limit 非数字应视为不限: %s", got.body)
		}
	})
}

func ptr(v float64) *float64 { return &v }
