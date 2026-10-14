package api

import (
	"context"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// 一夜睡眠：2026-05-10 本地 23:00 入睡、次日 07:00 起床，分期样本跨零点。
// 用固定历史日期而不是「昨天」，断言才不会随运行时间漂移。
const (
	stageNight = "2026-05-10" // 睡眠日（入睡时刻的本地日期）
	stageNext  = "2026-05-11" // 跨零点后的日期
)

// seedSleepNight 走真实写接口造一晚的数据：会话 + 两段分期（第二段在次日）+ 窗口内两条心率。
func seedSleepNight(t *testing.T, db *postgres.DB) {
	t.Helper()
	runID := newRun(t, db)

	sleepStart := wallAt(stageNight, 23, 0).UTC() // 2026-05-10T15:00:00Z
	sleepEnd := wallAt(stageNext, 7, 0).UTC()     // 2026-05-10T23:00:00Z
	total := 8.0
	if _, err := db.WriteSleep(context.Background(), runID, []sample.SleepSession{{
		LocalDate: stageNight, TZOffsetMin: 480, Source: "Apple Watch",
		SleepStart: &sleepStart, SleepEnd: &sleepEnd, TotalSleepHr: &total,
	}}); err != nil {
		t.Fatalf("写睡眠失败: %v", err)
	}

	addSamples(t, db, runID,
		sample.Sample{Metric: "sleep_analysis", Source: "Apple Watch",
			StartTime: wallAt(stageNight, 23, 10).UTC(), EndTime: wallAt(stageNext, 0, 30).UTC(),
			Value: 80.0 / 60, Unit: "hr", LocalDate: stageNight, TZOffsetMin: 480,
			Meta: map[string]any{"stage": "AsleepCore"}},
		// 跨零点那段：local_date 属于次日，靠时间窗才捞得回来
		sample.Sample{Metric: "sleep_analysis", Source: "Apple Watch",
			StartTime: wallAt(stageNext, 2, 0).UTC(), EndTime: wallAt(stageNext, 2, 30).UTC(),
			Value: 0.5, Unit: "hr", LocalDate: stageNext, TZOffsetMin: 480,
			Meta: map[string]any{"stage": "Awake"}},
		sample.Sample{Metric: "heart_rate", Source: "Apple Watch",
			StartTime: wallAt(stageNext, 1, 0).UTC(), Value: 66, Unit: "bpm", LocalDate: stageNext, TZOffsetMin: 480},
		sample.Sample{Metric: "heart_rate", Source: "Apple Watch",
			StartTime: wallAt(stageNext, 3, 0).UTC(), Value: 70, Unit: "bpm", LocalDate: stageNext, TZOffsetMin: 480},
	)
}

func TestSleepRangeWithRealData(t *testing.T) {
	r, db := newRouter(t, "")
	seedSleepNight(t, db)

	got := get(t, r, "/api/sleep?from="+stageNight+"&to="+stageNext)
	if got.status != http.StatusOK {
		t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
	}
	rows := got.arr(t, "rows")
	if len(rows) != 1 {
		t.Fatalf("应只有指定的一晚: %s", got.body)
	}
	row := rows[0].(map[string]any)
	if row["local_date"] != stageNight || row["source"] != "Apple Watch" {
		t.Fatalf("会话头部字段不符: %v", row)
	}
	if row["sleep_start"] != "2026-05-10T15:00:00Z" || row["sleep_end"] != "2026-05-10T23:00:00Z" {
		t.Fatalf("起止时刻应是 UTC 化的本地 23:00 / 07:00: %v", row)
	}
	if row["tz_offset_min"] != float64(480) {
		t.Fatalf("偏移要带给前端还原本地钟点: %v", row)
	}
	// awake_count / waso_hr 出自入睡窗口内的分段样本：跨零点那条也必须算进来
	if row["awake_count"] != float64(1) {
		t.Fatalf("awake_count 应为 1: %v", row)
	}
	if row["waso_hr"] != 0.5 {
		t.Fatalf("waso_hr 应为 0.5 小时: %v", row)
	}
}

func TestSleepStagesWithData(t *testing.T) {
	r, db := newRouter(t, "")
	seedSleepNight(t, db)

	t.Run("指定日期的深链", func(t *testing.T) {
		got := get(t, r, "/api/sleep/stages?date="+stageNight)
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		checkNight(t, got)
	})

	t.Run("date 留空：取分期样本 local_date 的最大值", func(t *testing.T) {
		got := get(t, r, "/api/sleep/stages")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		// 现状记录（详见交付报告，疑似 bug）：缺省日期取的是「分期样本 local_date 的最大值」，
		// 而不是入睡时刻的睡眠日。一晚里只要有一段落在次日凌晨，缺省就会指到次日：
		// 那天没有会话，粗粒度窗口（18:00→次日 18:00）又把凌晨那段挡在外面，只剩零值。
		// 断言锁住实际行为；若改成按会话日取最大，这条会红，届时期望值应为 stageNight。
		if got.str(t, "local_date") != stageNext {
			t.Fatalf("local_date = %q，期望 %q", got.str(t, "local_date"), stageNext)
		}
		if got.fields["has_session"] != false || len(got.arr(t, "segments")) != 0 {
			t.Fatalf("缺省指到次日时只能给出零值结构: %s", got.body)
		}
		// 但没有分期数据的日子也绝不能 404：前端要靠 200 + 空数组显示占位提示
		if _, ok := got.fields["totals"].(map[string]any); !ok {
			t.Fatalf("totals 必须是对象: %s", got.body)
		}
		// 缺省那晚画不出分期，却因为粗粒度窗口捞到了凌晨的心率曲线——
		// 这个自相矛盾的响应正是上面口径问题的直接表现，锁在这里等修。
		if n := len(got.arr(t, "physiology")); n != 1 {
			t.Fatalf("physiology = %d 条，期望 1 条: %s", n, got.body)
		}
	})
}

// 分段都落在入睡当日时，缺省日期就正好是那晚——这才是页面首屏要看到的东西。
func TestSleepStagesDefaultLandsOnLatestNight(t *testing.T) {
	r, db := newRouter(t, "")
	const day = "2026-04-20"
	const next = "2026-04-21"

	runID := newRun(t, db)
	sleepStart := wallAt(day, 22, 0).UTC()
	sleepEnd := wallAt(next, 6, 0).UTC()
	if _, err := db.WriteSleep(context.Background(), runID, []sample.SleepSession{{
		LocalDate: day, TZOffsetMin: 480, Source: "Apple Watch",
		SleepStart: &sleepStart, SleepEnd: &sleepEnd,
	}}); err != nil {
		t.Fatalf("写睡眠失败: %v", err)
	}
	addSamples(t, db, runID,
		sample.Sample{Metric: "sleep_analysis", Source: "Apple Watch",
			StartTime: wallAt(day, 22, 30).UTC(), EndTime: wallAt(day, 23, 30).UTC(),
			Value: 1, Unit: "hr", LocalDate: day, TZOffsetMin: 480,
			Meta: map[string]any{"stage": "AsleepCore"}},
		sample.Sample{Metric: "sleep_analysis", Source: "Apple Watch",
			StartTime: wallAt(day, 23, 30).UTC(), EndTime: wallAt(next, 1, 30).UTC(),
			Value: 2, Unit: "hr", LocalDate: day, TZOffsetMin: 480,
			Meta: map[string]any{"stage": "AsleepREM"}},
	)

	got := get(t, r, "/api/sleep/stages")
	if got.status != http.StatusOK {
		t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
	}
	if got.str(t, "local_date") != day {
		t.Fatalf("缺省应落在最近一晚 %q，实际 %q", day, got.str(t, "local_date"))
	}
	if got.fields["has_session"] != true || got.str(t, "source") != "Apple Watch" {
		t.Fatalf("会话信息缺失: %s", got.body)
	}
	segs := got.arr(t, "segments")
	if len(segs) != 2 {
		t.Fatalf("两段分期都要画出来: %s", got.body)
	}
	rem := segs[1].(map[string]any)
	// 顺序为 AsleepCore → AsleepREM；REM 必须归一到 rem 而不是笼统的 asleep
	if rem["stage"] != "rem" || rem["local_min"] != float64(1410) {
		t.Fatalf("REM 段不符: %v", rem)
	}
	totals := got.fields["totals"].(map[string]any)
	if totals["core"] != 1.0 || totals["rem"] != 2.0 {
		t.Fatalf("各阶段合计不符: %v", totals)
	}
}

func checkNight(t *testing.T, got reply) {
	t.Helper()
	if got.str(t, "local_date") != stageNight {
		t.Fatalf("local_date = %q", got.str(t, "local_date"))
	}
	if got.str(t, "source") != "Apple Watch" || got.fields["tz_offset_min"] != float64(480) {
		t.Fatalf("来源与偏移应来自会话: %s", got.body)
	}
	if got.fields["has_session"] != true {
		t.Fatalf("有会话时 has_session 必须为 true: %s", got.body)
	}
	if got.str(t, "sleep_start") != "2026-05-10T15:00:00Z" || got.str(t, "sleep_end") != "2026-05-10T23:00:00Z" {
		t.Fatalf("入睡窗口未回显: %s", got.body)
	}

	segs := got.arr(t, "segments")
	if len(segs) != 2 {
		t.Fatalf("应有两段分期（含跨零点那段）: %s", got.body)
	}
	core := segs[0].(map[string]any)
	if core["stage"] != "core" || core["raw"] != "AsleepCore" || core["local_min"] != float64(1390) {
		t.Fatalf("入睡后第一段不符（raw 要保留原文便于排查上游写法）: %v", core)
	}
	awake := segs[1].(map[string]any)
	// 跨零点的分段必须顺延到本夜轴（02:00 → 1440+120），否则时间线会把它画回前一天
	if awake["stage"] != "awake" || awake["local_min"] != float64(1560) {
		t.Fatalf("跨零点分段的本夜轴位置不符: %v", awake)
	}
	if awake["duration_min"] != 30.0 {
		t.Fatalf("分段时长应为 30 分钟: %v", awake)
	}
	totals, ok := got.fields["totals"].(map[string]any)
	if !ok {
		t.Fatalf("totals 应为对象: %s", got.body)
	}
	if math.Abs(totals["awake"].(float64)-0.5) > 1e-9 || math.Abs(totals["core"].(float64)-80.0/60) > 1e-9 {
		t.Fatalf("各阶段合计小时数不符: %v", totals)
	}

	phys, ok := got.fields["physiology"].([]any)
	if !ok || len(phys) != 1 {
		t.Fatalf("入睡窗口内的心率必须叠进时间线: %s", got.body)
	}
	hr := phys[0].(map[string]any)
	if hr["metric"] != "heart_rate" || hr["source"] != "Apple Watch" || hr["sample_count"] != float64(2) {
		t.Fatalf("生理序列不符: %v", hr)
	}
	if hr["avg"] != 68.0 || hr["min"] != 66.0 || hr["max"] != 70.0 {
		t.Fatalf("生理序列统计不符: %v", hr)
	}
	points := hr["points"].([]any)
	if len(points) != 2 {
		t.Fatalf("逐点序列应有两条: %v", points)
	}
}

func TestSleepStagesRangeWithData(t *testing.T) {
	r, db := newRouter(t, "")
	seedSleepNight(t, db)

	t.Run("区间内每晚一行", func(t *testing.T) {
		got := get(t, r, "/api/sleep/stages/range?from=2026-05-08&to=2026-05-11")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		rows := got.arr(t, "rows")
		if len(rows) != 1 {
			t.Fatalf("矩阵只应含有分期/会话的那晚: %s", got.body)
		}
		night := rows[0].(map[string]any)
		if night["local_date"] != stageNight || night["has_session"] != true {
			t.Fatalf("行头不符: %v", night)
		}
		if len(night["segments"].([]any)) != 2 {
			t.Fatalf("分段序列与单晚口径不一致: %v", night["segments"])
		}
		if night["sleep_start"] != "2026-05-10T15:00:00Z" {
			t.Fatalf("矩阵行也要带入睡时刻才能对齐日历: %v", night)
		}
	})

	// 矩阵最多画最近几十个夜晚，再多就看不出单晚了：窗口超了服务端必须截断，
	// 并把改写后的 from 回显，否则前端以为自己拿到了半年数据。
	t.Run("超过 120 天时截窗", func(t *testing.T) {
		to := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		want := to.AddDate(0, 0, -119).Format("2006-01-02")
		got := get(t, r, "/api/sleep/stages/range?from=2020-01-01&to=2026-06-01")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		if got.str(t, "from") != want {
			t.Fatalf("from 应被改写为 to-119 天 = %q，实际 %q", want, got.str(t, "from"))
		}
		if got.str(t, "to") != "2026-06-01" {
			t.Fatalf("to 不应被改动: %s", got.body)
		}
		// 截窗后区间依然覆盖到种下的那一晚：说明截的是起点而不是把数据截没了
		if len(got.arr(t, "rows")) != 1 {
			t.Fatalf("截窗后仍应拿到区间内的数据: %s", got.body)
		}
	})

	t.Run("刚好 120 天跨度不截断", func(t *testing.T) {
		// 阈值是「>120 天」才截：跨度正好 120 天时 from 必须原样回显，
		// 否则前端翻页会发现自己要的日期凭空少一天
		from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -120).Format("2006-01-02")
		got := get(t, r, "/api/sleep/stages/range?from="+from+"&to=2026-06-01")
		if got.str(t, "from") != from {
			t.Fatalf("正好 120 天窗口不应截断: %s", got.body)
		}
	})

	t.Run("缺省区间 30 天", func(t *testing.T) {
		got := get(t, r, "/api/sleep/stages/range")
		if got.str(t, "from") != dateInLoc(-29) || got.str(t, "to") != dateInLoc(0) {
			t.Fatalf("缺省区间 = %q~%q", got.str(t, "from"), got.str(t, "to"))
		}
	})
}

func TestSleepNightMetricsWithData(t *testing.T) {
	r, db := newRouter(t, "")
	seedSleepNight(t, db)

	t.Run("按入睡窗口聚合", func(t *testing.T) {
		got := get(t, r, "/api/sleep/night-metrics?from=2026-05-08&to=2026-05-11")
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		var hr map[string]any
		for _, raw := range got.arr(t, "rows") {
			if raw.(map[string]any)["metric"] == "heart_rate" {
				hr = raw.(map[string]any)
			}
		}
		if hr == nil {
			t.Fatalf("窗口内有心率样本就该出行: %s", got.body)
		}
		if hr["local_date"] != stageNight {
			t.Fatalf("local_date 应是睡眠日而不是样本自带的次日: %v", hr)
		}
		if hr["avg"] != 68.0 || hr["min"] != 66.0 || hr["max"] != 70.0 {
			t.Fatalf("夜间心率统计不符: %v", hr)
		}
		if hr["source"] != "Apple Watch" || hr["sample_count"] != float64(2) {
			t.Fatalf("一晚一指标只认一个来源: %v", hr)
		}
		if hr["window_start"] != "2026-05-10T15:00:00Z" || hr["window_end"] != "2026-05-10T23:00:00Z" {
			t.Fatalf("窗口应回显入睡时刻: %v", hr)
		}
		if hr["window_min"] != 480.0 {
			t.Fatalf("窗口时长应为 8 小时: %v", hr)
		}
	})

	// 白名单外的指标一律忽略：这一页只画睡着之后的生理量
	t.Run("非白名单指标回空数组而不是报错", func(t *testing.T) {
		got := get(t, r, "/api/sleep/night-metrics?from=2026-05-08&to=2026-05-11&metrics=step_count")
		if got.status != http.StatusOK || len(got.arr(t, "rows")) != 0 {
			t.Fatalf("应为 200 + 空数组: %d %s", got.status, got.body)
		}
	})

	t.Run("缺省区间 30 天", func(t *testing.T) {
		got := get(t, r, "/api/sleep/night-metrics")
		if got.str(t, "from") != dateInLoc(-29) {
			t.Fatalf("from = %q，期望最近 30 天", got.str(t, "from"))
		}
	})
}
