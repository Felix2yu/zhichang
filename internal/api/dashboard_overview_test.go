package api

import (
	"net/http"
	"testing"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// 本文件锁概览页新增三个接口的 HTTP 层契约：
// 空库的数组形状、非法入参的 400 口径、以及「今天没数据要回落并标注」这条最容易装看不见的规则。

// TestOverviewEndpointsOnEmptyDatabase 锁定空库形状——生产刚部署完就是这个样子。
// items 里每个指标都要有值（哪怕是 null），recent 必须是 []：前端直接对每个元素
// 取 recent.map()，缺键或 null 都会让整页崩成 500。
func TestOverviewEndpointsOnEmptyDatabase(t *testing.T) {
	r, _ := newRouter(t, "")

	t.Run("常态对照：每个指标都回一行，recent 是 []", func(t *testing.T) {
		got := get(t, r, "/api/baseline?metrics=step_count,heart_rate&window=30")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		items := got.arr(t, "items")
		if len(items) != 2 {
			t.Fatalf("应回两个指标各一行：%s", got.body)
		}
		for _, raw := range items {
			it, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("items 元素不是对象: %v", raw)
			}
			// 「没有数据」必须是 null + 空数组，而不是缺键：JSON 里缺键等于 undefined
			if v, exists := it["value"]; !exists || v != nil {
				t.Fatalf("空库时 value 应为 null: %v", it)
			}
			if v, exists := it["baseline"]; !exists || v != nil {
				t.Fatalf("空库时 baseline 应为 null: %v", it)
			}
			recent, ok := it["recent"].([]any)
			if !ok || len(recent) != 0 {
				t.Fatalf("recent 必须是空数组: %v", it)
			}
			if it["sample_days"] != float64(0) {
				t.Fatalf("空库时 sample_days 应为 0: %v", it)
			}
		}
	})

	t.Run("偏离提示：空库回空数组", func(t *testing.T) {
		got := get(t, r, "/api/insights?metrics=step_count,heart_rate")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		if len(got.arr(t, "items")) != 0 {
			t.Fatalf("空库不该有提示: %s", got.body)
		}
	})

	t.Run("覆盖度：rows 空数组且窗口边界闭合", func(t *testing.T) {
		got := get(t, r, "/api/coverage?days=30")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		if len(got.arr(t, "rows")) != 0 {
			t.Fatalf("空库 rows 应为空: %s", got.body)
		}
		if got.num(t, "window_days") != 30 {
			t.Fatalf("window_days 应为 30: %s", got.body)
		}
		if got.str(t, "to") != dateInLoc(0) {
			t.Fatalf("to 应为本地时区的今天: %s", got.body)
		}
		// 窗口含两端：30 天窗口的左端应是 29 天前
		if got.str(t, "from") != dateInLoc(-29) {
			t.Fatalf("from 应为 %s，实际 %s", dateInLoc(-29), got.str(t, "from"))
		}
	})
}

// TestOverviewEndpointsRejectBadInput 锁定入参校验：
// 日期格式错误必须报错而不是静默查空，指标名全不认识时必须 400（否则前端拿到一份空数据
// 会以为「用户根本没测过」，而真相是请求打错了）。
func TestOverviewEndpointsRejectBadInput(t *testing.T) {
	r, _ := newRouter(t, "")

	cases := []struct{ name, path string }{
		{"常态对照", "/api/baseline?metrics=step_count&date=2026-1-1"},
		{"偏离提示", "/api/insights?metrics=step_count&date=2026/10/01"},
		{"覆盖度", "/api/coverage?date=notadate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			get(t, r, c.path).wantErr(t, http.StatusBadRequest)
		})
	}

	t.Run("指标名全未登记", func(t *testing.T) {
		get(t, r, "/api/baseline?metrics=nosuch,alsonosuch").wantErr(t, http.StatusBadRequest)
		get(t, r, "/api/insights?metrics=nosuch").wantErr(t, http.StatusBadRequest)
	})
}

// TestBaselineEndpointFallsBackAndMarksStale 锁定概览页最容易装看不见的一条规则：
// 早上还没同步时，接口回落到最后有数据的那天并把 stale 置真。
// 前端据此标注「显示的是 X 月 X 日」——不标的话，用户会把三天前的数据当成今天的。
func TestBaselineEndpointFallsBackAndMarksStale(t *testing.T) {
	r, db := newRouter(t, "")
	runID := newRun(t, db)

	today := dateInLoc(0)
	// 前五天每天一批步数，今天故意没有
	var dates []string
	for i := 5; i >= 1; i-- {
		d := dateInLoc(-i)
		dates = append(dates, d)
		addSamples(t, db, runID, stepSample(d, 8, 0, 1000+float64(i)*100, "iPhone"))
	}
	recompute(t, db, dates, []string{"step_count"})

	t.Run("今天无数据时回落到最后有数据的一天", func(t *testing.T) {
		got := get(t, r, "/api/baseline?metrics=step_count&date="+today+"&window=30")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		items := got.arr(t, "items")
		if len(items) != 1 {
			t.Fatalf("应回一行: %s", got.body)
		}
		it := items[0].(map[string]any)
		if it["date"] != dateInLoc(-1) {
			t.Fatalf("对比日应回落到 %s，实际 %v", dateInLoc(-1), it["date"])
		}
		if it["stale"] != true {
			t.Fatalf("回落后 stale 必须为 true：%v", it)
		}
		if it["value"] == nil {
			t.Fatalf("回落日的值必须给出: %v", it)
		}
		// 只有 4 天样本（5 天里剔除对比日），刚好够算常态
		if it["sample_days"] != float64(4) {
			t.Fatalf("窗口应是对比日之前那 4 天，实际 %v", it["sample_days"])
		}
	})

	t.Run("当天有数据时不回落", func(t *testing.T) {
		addSamples(t, db, runID, stepSample(today, 8, 0, 9000, "iPhone"))
		recompute(t, db, []string{today}, []string{"step_count"})

		got := get(t, r, "/api/baseline?metrics=step_count&date="+today+"&window=30")
		it := got.arr(t, "items")[0].(map[string]any)
		if it["date"] != today || it["stale"] != false {
			t.Fatalf("当天有数据就不该回落: %v", it)
		}
		if it["value"] != float64(9000) {
			t.Fatalf("值应为 9000: %v", it)
		}
		// 5 天常态：1100..1500 的中位数 1300
		if it["baseline"] != float64(1300) {
			t.Fatalf("常态应是前 5 天的中位数 1300，实际 %v", it["baseline"])
		}
	})

	t.Run("窗口按传入的天数生效", func(t *testing.T) {
		got := get(t, r, "/api/baseline?metrics=step_count&date="+today+"&window=7")
		it := got.arr(t, "items")[0].(map[string]any)
		if it["window_days"] != float64(7) {
			t.Fatalf("window_days 应回显 7，实际 %v", it["window_days"])
		}
	})
}

// TestInsightsEndpointReportsDeviation 打通端到端：
// 一段稳定的序列最后一天突跳，接口要把它挑出来，且带上方向与实际偏离幅度。
func TestInsightsEndpointReportsDeviation(t *testing.T) {
	r, db := newRouter(t, "")
	runID := newRun(t, db)

	// 前 9 天在 60 附近抖动，最后一天抬到 75
	base := []float64{60, 61, 59, 60, 61, 59, 60, 61, 60}
	var dates []string
	for i, v := range base {
		d := dateInLoc(-(len(base) - i))
		dates = append(dates, d)
		addSamples(t, db, runID, hrSample(d, 8, 0, v))
	}
	today := dateInLoc(0)
	dates = append(dates, today)
	addSamples(t, db, runID, hrSample(today, 8, 0, 75))
	recompute(t, db, dates, []string{"heart_rate"})

	got := get(t, r, "/api/insights?metrics=heart_rate&date="+today)
	if got.status != http.StatusOK {
		t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
	}
	items := got.arr(t, "items")
	if len(items) != 1 {
		t.Fatalf("应挑出 1 条偏离，实际 %d：%s", len(items), got.body)
	}
	it := items[0].(map[string]any)
	if it["metric"] != "heart_rate" {
		t.Fatalf("指标名不对: %v", it)
	}
	if it["direction"] != "high" {
		t.Fatalf("应判偏高: %v", it)
	}
	if it["date"] != today {
		t.Fatalf("日期应为今天: %v", it)
	}
	if pct, _ := it["deviation_pct"].(float64); pct < 20 {
		t.Fatalf("偏离幅度应在 20%% 以上，实际 %v", pct)
	}
}

// TestCoverageEndpointCountsActiveDays 锁定覆盖度：它回答「这张卡为什么是空的」，
// 天数算错会让界面说出「明天更新」这种话。
func TestCoverageEndpointCountsActiveDays(t *testing.T) {
	r, db := newRouter(t, "")
	runID := newRun(t, db)

	d1, d2 := dateInLoc(-1), dateInLoc(0)
	addSamples(t, db, runID, stepSample(d1, 8, 0, 8000, "iPhone"))
	addSamples(t, db, runID, stepSample(d2, 8, 0, 9000, "iPhone"))
	recompute(t, db, []string{d1, d2}, []string{"step_count"})

	got := get(t, r, "/api/coverage?days=30")
	rows := got.arr(t, "rows")
	if len(rows) != 1 {
		t.Fatalf("应只有 step_count 有数据: %s", got.body)
	}
	it := rows[0].(map[string]any)
	if it["metric"] != "step_count" {
		t.Fatalf("指标不对: %v", it)
	}
	if it["days"] != float64(2) {
		t.Fatalf("应有 2 天数据，实际 %v", it["days"])
	}
	if it["last_date"] != d2 {
		t.Fatalf("最后更新日应为今天: %v", it)
	}
	if it["days_since"] != float64(0) {
		t.Fatalf("距今天数应为 0，实际 %v", it["days_since"])
	}
}

// hrSample 造一条心率样本（天级口径是 mean，一条样本即当天均值）。
func hrSample(date string, hour, minute int, v float64) sample.Sample {
	at := wallAt(date, hour, minute).UTC()
	return sample.Sample{
		Metric: "heart_rate", Source: "Apple Watch", StartTime: at, EndTime: at,
		Value: v, Unit: "bpm", LocalDate: date, TZOffsetMin: 480,
	}
}
