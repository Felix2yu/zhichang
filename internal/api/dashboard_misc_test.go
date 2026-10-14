package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

func TestWorkoutsWithRealData(t *testing.T) {
	r, db := newRouter(t, "")
	runID := newRun(t, db)
	start := wallAt(stageNight, 7, 0).UTC()
	end := wallAt(stageNight, 8, 0).UTC()
	energy, distance := 320.0, 8.2
	if _, err := db.WriteWorkouts(context.Background(), runID, []sample.Workout{{
		Name: "户外跑步", Source: "Apple Watch", StartTime: start, EndTime: end, DurationMin: 60,
		TotalEnergy: &energy, EnergyUnit: "kcal", TotalDistance: &distance, DistanceUnit: "km",
		LocalDate: stageNight, TZOffsetMin: 480,
	}}); err != nil {
		t.Fatalf("写运动失败: %v", err)
	}

	t.Run("指定区间", func(t *testing.T) {
		got := get(t, r, "/api/workouts?from=2026-05-09&to=2026-05-11")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		rows := got.arr(t, "rows")
		if len(rows) != 1 {
			t.Fatalf("应只有一条运动: %s", got.body)
		}
		row := rows[0].(map[string]any)
		if row["name"] != "户外跑步" || row["duration_min"] != 60.0 {
			t.Fatalf("运动字段不符: %v", row)
		}
		if row["total_energy"] != 320.0 || row["energy_unit"] != "kcal" || row["total_distance"] != 8.2 {
			t.Fatalf("能量与距离要带单位，否则前端只能瞎猜: %v", row)
		}
		if row["start"] != start.Format(time.RFC3339) {
			t.Fatalf("start = %q", row["start"])
		}
	})

	t.Run("缺省区间 30 天", func(t *testing.T) {
		got := get(t, r, "/api/workouts")
		if got.str(t, "from") != dateInLoc(-29) || got.str(t, "to") != dateInLoc(0) {
			t.Fatalf("缺省区间 = %q~%q", got.str(t, "from"), got.str(t, "to"))
		}
		if len(got.arr(t, "rows")) != 0 {
			t.Fatalf("历史数据不该落进缺省的最近 30 天: %s", got.body)
		}
	})
}

func TestSourcesWithRealData(t *testing.T) {
	r, db := newRouter(t, "")
	if err := db.UpsertSources(context.Background(), []string{"Apple Watch", "iPhone", ""}); err != nil {
		t.Fatalf("登记来源失败: %v", err)
	}
	addSamples(t, db, newRun(t, db), stepSample(stageNight, 9, 0, 100, "Apple Watch"))

	got := get(t, r, "/api/sources")
	if got.status != http.StatusOK {
		t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
	}
	rows := got.arr(t, "sources")
	if len(rows) != 2 {
		t.Fatalf("空来源名不应入库: %s", got.body)
	}
	// 按样本数降序：手表有样本，排在 iPhone 前面
	first := rows[0].(map[string]any)
	if first["source"] != "Apple Watch" || first["samples"] != float64(1) {
		t.Fatalf("来源样本统计不符: %v", first)
	}
	if first["device_class"] != "watch" {
		t.Fatalf("设备类别应由来源名判定: %v", first)
	}
	if _, ok := first["first_seen"].(string); !ok {
		t.Fatalf("首次见到时刻必须可读: %v", first)
	}
}

func TestSyncStatusWithData(t *testing.T) {
	r, db := newRouter(t, "")
	runID := newRun(t, db)
	addSamples(t, db, runID, stepSample(stageNight, 9, 0, 8000, "iPhone"))
	if err := db.FinishRun(context.Background(), runID, postgres.IngestStats{
		Received: 1, Inserted: 1, AffectedFrom: stageNight, AffectedTo: stageNight,
	}, nil); err != nil {
		t.Fatalf("收尾摄入失败: %v", err)
	}
	if err := db.UpdateWatermark(context.Background(), "api-test", map[string]postgres.Watermark{
		"step_count": {Time: wallAt(stageNight, 9, 0), LocalDate: stageNight},
	}); err != nil {
		t.Fatalf("写水位失败: %v", err)
	}

	got := get(t, r, "/api/sync/status?days=abc")
	if got.status != http.StatusOK {
		t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
	}
	// home_tz 来自构造时传入的 tzName：服务器跑在 UTC 时页面仍要知道用户住在哪个时区
	if got.str(t, "home_timezone") != homeTZ {
		t.Fatalf("home_timezone = %q，期望 %q", got.str(t, "home_timezone"), homeTZ)
	}
	runs := got.arr(t, "recent_runs")
	if len(runs) != 1 {
		t.Fatalf("recent_runs 应有那次摄入: %s", got.body)
	}
	run := runs[0].(map[string]any)
	if run["status"] != "ok" || run["transport"] != "test" || run["inserted"] != float64(1) {
		t.Fatalf("run 摘要不符: %v", run)
	}
	last, ok := got.fields["last_run"].(map[string]any)
	if !ok || last["id"] != run["id"] {
		t.Fatalf("last_run 应是最近一次: %s", got.body)
	}
	wm := got.arr(t, "watermarks")
	if len(wm) != 1 {
		t.Fatalf("水位应有 1 条: %s", got.body)
	}
	w := wm[0].(map[string]any)
	if w["last_local_date"] != stageNight || w["metric"] != "step_count" {
		t.Fatalf("水位不符: %v", w)
	}

	// 空聚合表时「缺口天数」正好等于回看区间长度，条数直接暴露 days 用了哪个值；
	// 断言条数而不是日期本身，因为缺口区间跟着库里的 CURRENT_DATE 走。
	t.Run("days 非法或 <=0 时回落 30 天", func(t *testing.T) {
		cases := []struct {
			query string
			want  int
		}{
			{"days=abc", 31}, // 非数字 → 30 天 → 含首尾 31 天
			{"days=-5", 31},
			{"days=0", 31},
			{"days=5", 6},
			{"", 31},
		}
		for _, c := range cases {
			t.Run(c.query, func(t *testing.T) {
				target := "/api/sync/status"
				if c.query != "" {
					target += "?" + c.query
				}
				got := get(t, r, target)
				if n := len(got.arr(t, "missing_days")); n != c.want {
					t.Fatalf("%s → 缺口 %d 天，期望 %d", c.query, n, c.want)
				}
			})
		}
	})
}
