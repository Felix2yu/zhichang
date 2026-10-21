package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// 本文件锁的是 MCP 给 AI 客户端的「口径」：中文指标名与单位怎么拼、默认日期区间的
// 边界落在哪一天、跨来源怎么收敛、空数据和非法指标怎么报。
// 断言刻意只挑稳定的语义片段 + 由同一套 day() 推导的日期，不整串比时间戳。

func TestListMetrics(t *testing.T) {
	db := sharedDB(t)
	reg := metric.New([]metric.Def{
		{Metric: "body_mass", DisplayNameZH: "体重", Kind: metric.KindInstant, DayAgg: metric.DayAggMean,
			CrossSourceAgg: metric.CrossAggMean, PreferredUnit: "kg", Enabled: true, SortOrder: 190},
		{Metric: "step_count", DisplayNameZH: "步数", Kind: metric.KindCumulative, DayAgg: metric.DayAggSum,
			CrossSourceAgg: metric.CrossAggMax, PreferredUnit: "count", Enabled: true, SortOrder: 10},
		{Metric: "retired_metric", DisplayNameZH: "已停用项", Kind: metric.KindInstant, DayAgg: metric.DayAggMean,
			PreferredUnit: "x", Enabled: false, SortOrder: 5},
	})
	d := DepsFrom(db, reg, testLoc, testTZName, false)

	out := text(t, func() (string, error) { return d.ListMetrics(context.Background()) })
	// 表头的「共 N 项」数的是全部登记项（含停用），而表格只列启用项：
	// 这两个数今天不相等，是刻意的还是漏了 filter，改代码前先用测试钉住现状。
	requireParts(t, out,
		"指标目录（时区 Asia/Shanghai，共 3 项）",
		"| 指标 | 中文名 | 类别 | 日内口径 | 跨来源 | 单位 |",
		"| step_count | 步数 | cumulative | sum | max | count |",
		"| body_mass | 体重 | instant | mean | mean | kg |",
	)
	forbidParts(t, out, "retired_metric", "已停用项")
	if strings.Index(out, "step_count") > strings.Index(out, "body_mass") {
		t.Errorf("应按 sort_order 升序输出（步数 10 在体重 190 之前）\n%s", out)
	}
}

func TestResolveLabelAndUnit(t *testing.T) {
	d := &Deps{Metrics: metric.New(metric.Defaults()), Location: testLoc, Timezone: testTZName}

	enabled := len(d.Metrics.Enabled())
	all, warn := d.resolve("")
	if warn != "" {
		t.Errorf("留空不应报警，实际 %q", warn)
	}
	if len(all) != enabled {
		t.Errorf("留空应展开全部启用指标，期望 %d 实际 %d", enabled, len(all))
	}
	for _, m := range all {
		if !d.known(m) {
			t.Errorf("展开出来的 %q 未登记", m)
		}
	}

	// 空白与空项要忽略，未登记的单独收集成警告而不是直接失败
	ok, warn := d.resolve(" step_count , , bogus , body_mass ")
	if strings.Join(ok, ",") != "step_count,body_mass" {
		t.Errorf("合法项解析错: %v", ok)
	}
	if warn != "未登记的指标: bogus" {
		t.Errorf("非法项警告错: %q", warn)
	}
	if ok, _ := d.resolve("bogus1,bogus2"); len(ok) != 0 {
		t.Errorf("全非法时不应有合法项: %v", ok)
	}

	if got := d.label("step_count"); got != "步数(step_count)" {
		t.Errorf("中文名+代号格式错: %q", got)
	}
	if got := d.label("no_such_metric"); got != "no_such_metric" {
		t.Errorf("未登记指标应原样返回: %q", got)
	}
	if got := d.unit("step_count"); got != "count" {
		t.Errorf("单位应取 preferred_unit: %q", got)
	}
	if got := d.unit("no_such_metric"); got != "" {
		t.Errorf("未登记指标不应编造单位: %q", got)
	}
}

func TestFmtNumAndPtr(t *testing.T) {
	if got := fmtNum(f64(2050.004), "count"); got != "2050.00 count" {
		t.Errorf("数值应保留两位并带单位: %q", got)
	}
	if got := fmtNum(f64(2.71828), ""); got != "2.72" {
		t.Errorf("无单位时不应留尾空格: %q", got)
	}
	// 聚合值为 NULL 时输出破折号，而不是 0：把「没数据」说成 0 会误导模型
	if got := fmtNum(nil, "kcal"); got != "—" {
		t.Errorf("空值应输出 —: %q", got)
	}
	if ptr(nil) != 0 || ptr(f64(2.5)) != 2.5 {
		t.Error("ptr 解引用口径错")
	}
}

func TestDailySummary(t *testing.T) {
	t.Run("含前一日对比", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d,
			daily("step_count", day(-1), 1000),
			daily("step_count", day(0), 2050),
			daily("body_mass", day(0), 70.5),
		)
		out := text(t, func() (string, error) { return d.DailySummary(context.Background(), day(0)) })
		requireParts(t, out,
			day(0)+" 概览（时区 Asia/Shanghai）",
			// 2050 vs 1000 → +105.0%，变化率必须由前一日算
			"- 步数(step_count): 2050.00 count（前一日 1000.00 count，+105.0%）",
			"- 体重(body_mass): 70.50 kg",
		)
		forbidParts(t, out, "70.50 kg（前一日")
		// 步数 sort_order 10 < 体重 190
		if strings.Index(out, "步数") > strings.Index(out, "体重") {
			t.Errorf("概览应按 sort_order 排\n%s", out)
		}
	})

	t.Run("默认查今天", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, daily("step_count", day(0), 100))
		out := text(t, func() (string, error) { return d.DailySummary(context.Background(), "") })
		requireParts(t, out, day(0)+" 概览")
	})

	t.Run("前一日为0不算变化率", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d,
			daily("mindful_minutes", day(-1), 0),
			daily("mindful_minutes", day(0), 20),
		)
		out := text(t, func() (string, error) { return d.DailySummary(context.Background(), day(0)) })
		// 除数为 0 时宁可只报前一日数值，也不能吐出 "+inf%"
		requireParts(t, out, "- 正念时长(mindful_minutes): 20.00 min（前一日 0.00 min）")
		forbidParts(t, out, "%")
	})

	t.Run("未登记指标无单位", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d,
			daily("step_count", day(0), 100),
			daily("retired_metric", day(0), 12),
		)
		out := text(t, func() (string, error) { return d.DailySummary(context.Background(), day(0)) })
		// 库里出现字典里没有的指标时：名字原样给、单位留空、按 sort_order 0 排最前
		requireParts(t, out, "- retired_metric: 12.00\n")
		if strings.Index(out, "retired_metric") > strings.Index(out, "step_count") {
			t.Errorf("未知指标应排在已知指标之前（sort_order 零值）\n%s", out)
		}
	})

	t.Run("附当晚睡眠", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, daily("step_count", day(0), 100))
		putSleep(t, d, sample.SleepSession{
			LocalDate: day(0), Source: "Apple Watch", TZOffsetMin: 480,
			SleepStart: ptrAt(at(day(0), 23, 30)), SleepEnd: ptrAt(at(nextDay(day(0)), 7, 0)),
			TotalSleepHr: f64(7.5), DeepHr: f64(1.5), CoreHr: f64(4.0), RemHr: f64(2.0),
		})
		out := text(t, func() (string, error) { return d.DailySummary(context.Background(), day(0)) })
		requireParts(t, out, "- 睡眠: 总 7.5h（深睡 1.5h / 核心 4.0h / REM 2.0h）")
	})

	t.Run("空数据", func(t *testing.T) {
		d := newDeps(t)
		out := text(t, func() (string, error) { return d.DailySummary(context.Background(), "2020-01-01") })
		if out != "2020-01-01 没有聚合数据（可能尚未同步或该日无记录）" {
			t.Errorf("空数据文案错: %q", out)
		}
	})
}

func TestQuery(t *testing.T) {
	t.Run("默认区间是含今天的30天", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d,
			daily("step_count", day(-29), 100),
			daily("step_count", day(-30), 999),
		)
		out := text(t, func() (string, error) { return d.Query(context.Background(), "step_count", "", "") })
		requireParts(t, out,
			// 第 30 天前（-29）在区间内，-30 必须在外面
			"\n步数(step_count)（"+day(-29)+" ~ "+day(0)+"，1 天，单位 count）",
			"  "+day(-29)+"  100.00  (来源 1: Apple Watch)",
		)
		forbidParts(t, out, day(-30), "999.00")
	})

	t.Run("留空即全部启用指标", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, daily("step_count", day(0), 100))
		out := text(t, func() (string, error) { return d.Query(context.Background(), "", day(0), day(0)) })
		requireParts(t, out, "步数(step_count)")
		// 没数据的指标不该各出一段
		forbidParts(t, out, "体重(body_mass)", "没有数据")
	})

	t.Run("跨来源收敛", func(t *testing.T) {
		d := newDeps(t)
		// 累计量：iPhone 2050、Watch 2000 → 跨来源取 max = 2050（消除双计）
		putDaily(t, d,
			row{metric: "step_count", source: "iPhone", at: at(day(0), 8, 0), value: 1200},
			row{metric: "step_count", source: "iPhone", at: at(day(0), 9, 0), value: 850},
			row{metric: "step_count", source: "Apple Watch", at: at(day(0), 8, 0), value: 1200},
			row{metric: "step_count", source: "Apple Watch", at: at(day(0), 9, 0), value: 800},
			// 瞬时量：日内 mean + 跨来源按样本数加权 → (60+62+80)/3 = 67.33
			// 若是「各来源均值再平均」会得到 70.50，那正是本条要挡住的写法。
			row{metric: "heart_rate", source: "Apple Watch", at: at(day(0), 8, 0), value: 60},
			row{metric: "heart_rate", source: "Apple Watch", at: at(day(0), 9, 0), value: 62},
			row{metric: "heart_rate", source: "iPhone", at: at(day(0), 8, 0), value: 80},
		)
		out := text(t, func() (string, error) { return d.Query(context.Background(), "step_count,heart_rate", day(0), day(0)) })
		requireParts(t, out, "  "+day(0)+"  2050.00  (来源 2:", "  "+day(0)+"  67.33  (来源 2:")
		forbidParts(t, out, "70.50", "4050.00")
	})

	t.Run("按请求顺序输出", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, daily("step_count", day(0), 100), daily("body_mass", day(0), 70))
		out := text(t, func() (string, error) { return d.Query(context.Background(), "body_mass,step_count", day(0), day(0)) })
		// 请求里先写谁就先出谁，不受字典 sort_order 影响
		if strings.Index(out, "体重") > strings.Index(out, "步数") {
			t.Errorf("应保留请求顺序\n%s", out)
		}
	})

	t.Run("非法指标只警告不失败", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, daily("step_count", day(0), 100))
		out := text(t, func() (string, error) {
			return d.Query(context.Background(), "step_count,bogus", day(0), day(0))
		})
		if !strings.HasPrefix(out, "未登记的指标: bogus\n") {
			t.Errorf("警告必须是首行且仍继续输出\n%q", out)
		}
		requireParts(t, out, "100.00")
	})

	t.Run("全是非法指标则报错", func(t *testing.T) {
		d := newDeps(t)
		msg := fail(t, func() (string, error) { return d.Query(context.Background(), "bogus", "", "") })
		requireParts(t, msg, "没有可用指标", "未登记的指标: bogus")
	})

	t.Run("区间无数据", func(t *testing.T) {
		d := newDeps(t)
		out := text(t, func() (string, error) { return d.Query(context.Background(), "step_count", "2020-01-01", "2020-01-02") })
		if out != "2020-01-01 ~ 2020-01-02 没有数据" {
			t.Errorf("无数据文案错: %q", out)
		}
	})

	t.Run("非法日期报错", func(t *testing.T) {
		d := newDeps(t)
		msg := fail(t, func() (string, error) { return d.Query(context.Background(), "step_count", "2026-13-45", "") })
		if !strings.Contains(msg, "日期格式应为 YYYY-MM-DD") {
			t.Errorf("应把仓储层的日期错误透出来: %q", msg)
		}
	})
}

func TestCompare(t *testing.T) {
	rows := append(append(append([]row{},
		rowsFor("step_count", -15, -15, 700)...), // 两个区间之外，用来暴露边界算错
		rowsFor("body_mass", -6, 0, 80)...),
		rowsFor("mindful_minutes", -13, -7, 0)...)
	rows = append(rows, rowsFor("step_count", -13, -7, 50)...)
	rows = append(rows, rowsFor("step_count", -6, 0, 100)...)
	rows = append(rows, rowsFor("mindful_minutes", -6, 0, 100)...)

	t.Run("默认本周对上周", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, rows...)
		out := text(t, func() (string, error) {
			return d.Compare(context.Background(), "step_count,body_mass,mindful_minutes", "", "", "", "")
		})
		requireParts(t, out,
			// A = 今天往前 6 天到今天，B = 今天往前 13 天到 7 天前
			"区间 A "+day(-6)+"~"+day(0)+" 与区间 B "+day(-13)+"~"+day(-7)+" 对比（日均）",
			"| 指标 | A 日均 | B 日均 | 变化 | 变化率 |",
			"| 步数(step_count) | 100.00 | 50.00 | +50.00 | +100.0% |",
		)
		// 区间外那天的 700 绝不能进日均
		forbidParts(t, out, "700")
		// 一侧完全没数据的指标不出现，而不是拿 0 去比
		forbidParts(t, out, "| 体重(body_mass) |")
		// B 侧日均为 0 时变化率不除零，给 +0.0%
		requireParts(t, out, "| 正念时长(mindful_minutes) | 100.00 | 0.00 | +100.00 | +0.0% |")
	})

	t.Run("显式区间", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, rows...)
		out := text(t, func() (string, error) {
			return d.Compare(context.Background(), "step_count", day(-15), day(-15), day(0), day(0))
		})
		requireParts(t, out, "区间 A "+day(-15)+"~"+day(-15)+" 与区间 B "+day(0)+"~"+day(0))
		// A 只有 700、B 只有 100：换方向后变化率应为负
		requireParts(t, out, "| 步数(step_count) | 700.00 | 100.00 | +600.00 | +600.0% |")
	})

	t.Run("非法指标只警告", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, rows...)
		out := text(t, func() (string, error) {
			return d.Compare(context.Background(), "step_count,bogus", day(-6), day(0), day(-13), day(-7))
		})
		if !strings.HasPrefix(out, "未登记的指标: bogus\n") {
			t.Errorf("警告应是首行: %q", out)
		}
	})

	t.Run("无可用指标报错", func(t *testing.T) {
		d := newDeps(t)
		msg := fail(t, func() (string, error) { return d.Compare(context.Background(), "bogus", "", "", "", "") })
		requireParts(t, msg, "没有可用指标", "未登记的指标: bogus")
	})

	t.Run("区间日期非法报错", func(t *testing.T) {
		d := newDeps(t)
		msg := fail(t, func() (string, error) {
			return d.Compare(context.Background(), "step_count", "bad", "worse", "", "")
		})
		requireParts(t, msg, "日期格式应为 YYYY-MM-DD")
	})

	t.Run("对照区间日期非法报错", func(t *testing.T) {
		d := newDeps(t)
		msg := fail(t, func() (string, error) {
			return d.Compare(context.Background(), "step_count", day(-6), day(0), "bad", "worse")
		})
		requireParts(t, msg, "日期格式应为 YYYY-MM-DD")
	})
}

func TestLatest(t *testing.T) {
	t.Run("60天窗口两端", func(t *testing.T) {
		d := newDeps(t)
		// 只有 -60 这天：仍在窗口内（含端点），必须报出数值
		putDaily(t, d, daily("step_count", day(-60), 10), daily("body_mass", day(-61), 70))
		out := text(t, func() (string, error) { return d.Latest(context.Background(), "step_count,body_mass") })
		requireParts(t, out,
			"最新值（按本地日期）",
			"- 步数(step_count): 10.00 count ("+day(-60)+"，来源 Apple Watch)",
			// 只有 -61 有数据的指标必须落在窗口外
			"- 体重(body_mass): 近 60 天无数据",
		)
		forbidParts(t, out, day(-61))
	})

	t.Run("取最新一天而非最后一条", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d,
			daily("step_count", day(-20), 10),
			daily("step_count", day(-5), 20),
			daily("step_count", day(-10), 15),
		)
		out := text(t, func() (string, error) { return d.Latest(context.Background(), "step_count") })
		requireParts(t, out, "- 步数(step_count): 20.00 count ("+day(-5)+"，来源 Apple Watch)")
		forbidParts(t, out, "15.00", "10.00")
	})

	t.Run("非法指标先警告", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, daily("step_count", day(0), 10))
		out := text(t, func() (string, error) { return d.Latest(context.Background(), "step_count,bogus") })
		if !strings.HasPrefix(out, "未登记的指标: bogus\n") {
			t.Errorf("警告应是首行: %q", out)
		}
	})

	t.Run("全非法报错", func(t *testing.T) {
		d := newDeps(t)
		msg := fail(t, func() (string, error) { return d.Latest(context.Background(), "bogus") })
		requireParts(t, msg, "没有可用指标")
	})
}

func TestSleep(t *testing.T) {
	night := func(date, source string, total float64) sample.SleepSession {
		return sample.SleepSession{
			LocalDate: date, Source: source, TZOffsetMin: 480,
			SleepStart: ptrAt(at(date, 23, 30)), SleepEnd: ptrAt(at(nextDay(date), 7, 0)),
			TotalSleepHr: f64(total), DeepHr: f64(1.5), CoreHr: f64(4.0), RemHr: f64(2.0), InBedHr: f64(8.0),
		}
	}

	t.Run("默认30天且跨零点心率能关联", func(t *testing.T) {
		d := newDeps(t)
		putSleep(t, d, night(day(-29), "Apple Watch", 7.5))
		putSleep(t, d, night(day(-30), "Apple Watch", 9.0))
		// 心率样本落在次晨 03:12，local_date 是「次日」；
		// 只有按入睡时间窗关联才捞得回来，按日期取会整段漏掉。
		putSamples(t, d,
			row{metric: "heart_rate", source: "Apple Watch", at: at(nextDay(day(-29)), 3, 12),
				value: 70, vmin: f64(58), unit: "count/min"},
		)
		out := text(t, func() (string, error) { return d.Sleep(context.Background(), "", "") })
		requireParts(t, out,
			"睡眠 "+day(-29)+" ~ "+day(0)+"（1 天）",
			"| "+day(-29)+" | 7.5 | 1.5 | 4.0 | 2.0 | 8.0 | 58 bpm | Apple Watch |",
			"日均总睡眠 7.5 小时",
			"用 health_sleep_detail 传睡眠日查询",
		)
		forbidParts(t, out, day(-30))
	})

	t.Run("无夜间心率给破折号", func(t *testing.T) {
		d := newDeps(t)
		putSleep(t, d, night(day(-1), "Apple Watch", 7.5))
		putSleep(t, d, night(day(-1), "iPhone", 6.0))
		out := text(t, func() (string, error) { return d.Sleep(context.Background(), day(-1), day(0)) })
		requireParts(t, out, "| "+day(-1)+" | 6.0 | 1.5 | 4.0 | 2.0 | 8.0 | — | iPhone |")
		// 同一晚两个来源会各出一行：表头写「N 天」其实数的是会话条数，
		// 日均也是按条数除的（(7.5+6.0)/2=6.75→6.8），双来源的那晚会把一天算成两天。
		requireParts(t, out, "（2 天）", "日均总睡眠 6.8 小时")
	})

	t.Run("空数据", func(t *testing.T) {
		d := newDeps(t)
		out := text(t, func() (string, error) { return d.Sleep(context.Background(), "2020-01-01", "2020-01-02") })
		if out != "2020-01-01 ~ 2020-01-02 没有睡眠记录" {
			t.Errorf("空数据文案错: %q", out)
		}
	})

	t.Run("非法日期", func(t *testing.T) {
		d := newDeps(t)
		msg := fail(t, func() (string, error) { return d.Sleep(context.Background(), "昨天", "") })
		requireParts(t, msg, "日期格式应为 YYYY-MM-DD")
	})
}

func TestSleepDetail(t *testing.T) {
	// 一晚完整数据：上床 23:10、入睡 23:30、起床 07:00（次日）
	asleep := func(date string) sample.SleepSession {
		return sample.SleepSession{
			LocalDate: date, Source: "Apple Watch", TZOffsetMin: 480,
			SleepStart: ptrAt(at(date, 23, 30)), SleepEnd: ptrAt(at(nextDay(date), 7, 0)),
			InBedStart: ptrAt(at(date, 23, 10)), InBedEnd: ptrAt(at(nextDay(date), 7, 10)),
			TotalSleepHr: f64(7.5), DeepHr: f64(1.5), CoreHr: f64(4.0), RemHr: f64(2.0),
			InBedHr: f64(8.0), AwakeHr: f64(0.75),
		}
	}
	fullNight := func(t *testing.T, d *Deps, date string) {
		t.Helper()
		putSleep(t, d, asleep(date))
		putSamples(t, d,
			seg(date, 23*60+10, 20, "inBed", "Apple Watch"),
			seg(date, 23*60+30, 40, "AsleepDeep", "Apple Watch"),
			seg(date, 24*60+10, 120, "AsleepCore", "Apple Watch"),
			seg(date, 24*60+130, 15, "AsleepAwake", "Apple Watch"),
			seg(date, 24*60+145, 45, "AsleepREM", "Apple Watch"),
			seg(date, 24*60+190, 15, "AsleepAwake", "Apple Watch"),
			seg(date, 24*60+205, 155, "AsleepCore", "Apple Watch"),
			// 夜间心率：最低值看 value_min，平均看 value
			row{metric: "heart_rate", source: "Apple Watch", at: atMinute(date, 24*60+192), value: 70, vmin: f64(58), unit: "count/min"},
			row{metric: "heart_rate", source: "Apple Watch", at: atMinute(date, 24*60+160), value: 65, vmin: f64(60), unit: "count/min"},
			row{metric: "heart_rate", source: "Apple Watch", at: atMinute(date, 24*60+305), value: 66, vmin: f64(62), unit: "count/min"},
			row{metric: "respiratory_rate", source: "Apple Watch", at: atMinute(date, 24*60+200), value: 16, unit: "breaths/min"},
		)
	}

	t.Run("完整一晚", func(t *testing.T) {
		d := newDeps(t)
		date := day(-1)
		fullNight(t, d, date)
		out := text(t, func() (string, error) { return d.SleepDetail(context.Background(), date) })
		requireParts(t, out,
			"睡眠详情 "+date,
			"- 入睡 23:30 → 起床 07:00（tz +480 分钟）",
			"- 时长：总睡眠 7.5h · 深睡 1.5h · 核心 4.0h · REM 2.0h · 在床 8.0h",
			// 潜伏期 = 入睡 23:30 - 上床 23:10
			"- 入睡潜伏期 20 分钟",
			// WASO 只算入睡之后的两段清醒（0.25+0.25 小时），不含 awake_hr 的 0.75
			"- 入睡后清醒 30 分钟 · 2 次（不含入睡前那段）",
			"- 分期时间线（7 段，来源 Apple Watch）",
			"  23:10–23:30 在床（20 分）",
			"  23:30–00:10 深睡（40 分）",
			"  02:10–02:25 清醒（15 分）",
			"  02:25–03:10 REM（45 分）",
			// count/min 这个原始单位对读文本的人来说没用，必须换成 bpm
			"心率 最低 58 bpm · 平均 67 bpm · 3 点 · 来源 Apple Watch，03:12 出现",
			"；呼吸频率 最低 16 breaths/min",
		)
		forbidParts(t, out, "整晚清醒", "count/min", "- 时长：总睡眠 6")
	})

	t.Run("只有上床离床的一晚", func(t *testing.T) {
		d := newDeps(t)
		date := day(-1)
		putSleep(t, d, sample.SleepSession{
			LocalDate: date, Source: "iPhone", TZOffsetMin: 480,
			InBedStart: ptrAt(at(date, 22, 0)), InBedEnd: ptrAt(at(nextDay(date), 7, 0)),
			TotalSleepHr: f64(8.5), AwakeHr: f64(0.75),
		})
		out := text(t, func() (string, error) { return d.SleepDetail(context.Background(), date) })
		requireParts(t, out,
			"- 这一晚没有入睡/起床时刻记录",
			// 没有分段就无法拆出入睡前/后的清醒，只能报整晚清醒
			"- 整晚清醒 45 分钟（导出未含分段，无法区分入睡前/后）",
			"- 分期时间线：无（导出端需在 sleep_analysis 里带上分段）",
			"- 夜间生理指标：入睡窗口内没有样本",
		)
		forbidParts(t, out, "入睡后清醒")
	})

	t.Run("什么都没有的一晚", func(t *testing.T) {
		d := newDeps(t)
		out := text(t, func() (string, error) { return d.SleepDetail(context.Background(), "2020-01-01") })
		requireParts(t, out,
			"睡眠详情 2020-01-01",
			"- 这一晚没有入睡/起床时刻记录",
			"- 分期时间线：无",
			"- 夜间生理指标：入睡窗口内没有样本",
		)
		forbidParts(t, out, "- 时长：")
	})

	t.Run("默认查今天", func(t *testing.T) {
		d := newDeps(t)
		out := text(t, func() (string, error) { return d.SleepDetail(context.Background(), "") })
		requireParts(t, out, "睡眠详情 "+day(0))
	})

	t.Run("时长取最长会话且分期来源按覆盖率择优", func(t *testing.T) {
		d := newDeps(t)
		date := day(-1)
		putSleep(t, d, asleep(date))
		putSleep(t, d, sample.SleepSession{
			LocalDate: date, Source: "iPhone", TZOffsetMin: 480,
			SleepStart: ptrAt(at(date, 23, 30)), SleepEnd: ptrAt(at(nextDay(date), 5, 30)),
			TotalSleepHr: f64(6.0),
		})
		// 只有 iPhone 出了分期：会话来源是 Apple Watch，但该来源没有分段，
		// 时间线必须退到「覆盖率最高的来源」而不是死守会话来源。
		putSamples(t, d,
			seg(date, 23*60+30, 40, "AsleepDeep", "iPhone"),
			seg(date, 24*60+10, 120, "AsleepCore", "iPhone"),
		)
		out := text(t, func() (string, error) { return d.SleepDetail(context.Background(), date) })
		requireParts(t, out,
			"- 时长：总睡眠 7.5h",
			"- 分期时间线（2 段，来源 iPhone）",
		)
		// 有分段就一律用分段口径（本晚没有清醒段 → 0 分钟），不再退回整晚 awake_hr
		requireParts(t, out, "- 入睡后清醒 0 分钟 · 0 次（不含入睡前那段）")
		forbidParts(t, out, "整晚清醒")
	})

	t.Run("会话来源有分期时优先它", func(t *testing.T) {
		d := newDeps(t)
		date := day(-1)
		putSleep(t, d, asleep(date))
		// iPhone 的分期更长，但会话来源是 Apple Watch：仍以会话来源为准
		putSamples(t, d,
			seg(date, 23*60+30, 30, "AsleepDeep", "Apple Watch"),
			seg(date, 24*60+10, 300, "AsleepCore", "iPhone"),
		)
		out := text(t, func() (string, error) { return d.SleepDetail(context.Background(), date) })
		requireParts(t, out, "- 分期时间线（1 段，来源 Apple Watch）", "  23:30–00:00 深睡（30 分）")
		// iPhone 那段 300 分钟覆盖率更高，但会话来源有分期就必须用它
		forbidParts(t, out, "来源 iPhone")
	})

	t.Run("认不出的阶段归到其他", func(t *testing.T) {
		d := newDeps(t)
		date := day(-1)
		putSleep(t, d, asleep(date))
		// 归一化认不出的写法（napper）落到 other → 中文「其他」，
		// 不能凭空猜它是深睡还是 REM。
		putSamples(t, d, seg(date, 23*60+30, 40, "Napper", "Apple Watch"))
		out := text(t, func() (string, error) { return d.SleepDetail(context.Background(), date) })
		requireParts(t, out, "  23:30–00:10 其他（40 分）")
	})

	t.Run("非法日期", func(t *testing.T) {
		d := newDeps(t)
		msg := fail(t, func() (string, error) { return d.SleepDetail(context.Background(), "2020-13-01") })
		requireParts(t, msg, "日期格式应为 YYYY-MM-DD")
	})
}

func TestSleepDelete(t *testing.T) {
	// 两晚完整数据：会话 + 分期（含跨零点那段），外加一个无关指标验证不误伤。
	seed := func(t *testing.T) (d *Deps, old, recent string) {
		t.Helper()
		d = newDeps(t)
		old, recent = day(-3), day(-1)
		putSleep(t, d, *sleepNight(old), *sleepNight(recent))
		putSamples(t, d,
			seg(old, 23*60+30, 40, "AsleepDeep", "Apple Watch"),
			seg(old, 24*60+10, 120, "AsleepCore", "Apple Watch"),
			seg(recent, 23*60+30, 40, "AsleepDeep", "Apple Watch"),
			seg(recent, 24*60+10, 120, "AsleepCore", "Apple Watch"),
			daily("step_count", old, 100),
			daily("step_count", recent, 200),
		)
		if err := postgres.NewRollup(d.DB, d.Metrics).Recompute(context.Background(),
			[]string{old, recent}, []string{postgres.SleepMetric, "step_count"}); err != nil {
			t.Fatalf("播种天级聚合失败: %v", err)
		}
		return d, old, recent
	}
	count := func(t *testing.T, d *Deps, q string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := d.DB.Pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
			t.Fatalf("查询条数失败: %v", err)
		}
		return n
	}
	dayValue := func(t *testing.T, d *Deps, metric, date string) (float64, bool) {
		t.Helper()
		var v float64
		err := d.DB.Pool.QueryRow(context.Background(),
			`SELECT value FROM daily_metrics_day WHERE metric=$1 AND local_date=$2`, metric, date).Scan(&v)
		if err != nil {
			return 0, false
		}
		return v, true
	}

	t.Run("预演不删数据", func(t *testing.T) {
		d, _, recent := seed(t)
		out := text(t, func() (string, error) { return d.SleepDelete(context.Background(), recent, recent, false) })
		requireParts(t, out,
			"睡眠记录清理预演（"+recent+" ~ "+recent+"）",
			"- 睡眠会话 1 条",
			// 跨零点那段 local_date 是次日，也必须在预演里算进去
			"- 分期样本 2 条",
			"confirm=true",
		)
		if n := count(t, d, `SELECT count(*) FROM sleep_sessions WHERE local_date=$1`, recent); n != 1 {
			t.Errorf("预演后会话应原样保留，实际 %d", n)
		}
		if n := count(t, d, `SELECT count(*) FROM samples WHERE metric='sleep_analysis'`); n != 4 {
			t.Errorf("预演后分期样本应原样保留，实际 %d", n)
		}
	})

	t.Run("确认后删会话与分期并重算天级值", func(t *testing.T) {
		d, old, recent := seed(t)
		// 排一条待重算任务：删除后应一并清掉，不能让它把已删的日期又算一遍。
		if _, err := d.DB.Pool.Exec(context.Background(),
			`INSERT INTO rollup_queue (local_date, metric) VALUES ($1,$2)`, recent, postgres.SleepMetric); err != nil {
			t.Fatal(err)
		}

		out := text(t, func() (string, error) { return d.SleepDelete(context.Background(), recent, recent, true) })
		requireParts(t, out,
			"睡眠记录已清空（"+recent+" ~ "+recent+"）",
			"删除睡眠会话 1 条、分期样本 2 条",
			"现在可以重新导入睡眠数据了",
		)

		if n := count(t, d, `SELECT count(*) FROM sleep_sessions WHERE local_date=$1`, recent); n != 0 {
			t.Errorf("目标日期会话应删净，实际 %d", n)
		}
		if n := count(t, d, `SELECT count(*) FROM sleep_sessions WHERE local_date=$1`, old); n != 1 {
			t.Errorf("范围外的晚上不应被动，实际 %d", n)
		}
		if n := count(t, d, `SELECT count(*) FROM samples WHERE metric='sleep_analysis'`); n != 2 {
			t.Errorf("应只剩范围外那晚的两段分期，实际 %d", n)
		}
		// 目标那晚 00:10 的分段 local_date 落到了 nextDay(recent)，按日期删会漏，
		// 必须靠入睡时间窗一起删掉。
		if n := count(t, d, `SELECT count(*) FROM samples WHERE metric='sleep_analysis' AND local_date=$1`, nextDay(recent)); n != 0 {
			t.Errorf("跨零点分期应随会话一起删掉，实际还剩 %d", n)
		}

		if _, ok := dayValue(t, d, postgres.SleepMetric, recent); ok {
			t.Error("目标日期的天级睡眠值应删掉")
		}
		if v, ok := dayValue(t, d, postgres.SleepMetric, old); !ok || v != 7.5 {
			t.Errorf("范围外的天级睡眠值应保留并重算，实际 %v/%v", v, ok)
		}
		if v, ok := dayValue(t, d, "step_count", recent); !ok || v != 200 {
			t.Errorf("无关指标的天级值不应被清理，实际 %v/%v", v, ok)
		}
		if n := count(t, d, `SELECT count(*) FROM rollup_queue WHERE metric=$1`, postgres.SleepMetric); n != 0 {
			t.Errorf("删除日期的待重算任务应清掉，实际 %d", n)
		}

		sleepOut := text(t, func() (string, error) { return d.Sleep(context.Background(), recent, recent) })
		requireParts(t, sleepOut, recent+" ~ "+recent+" 没有睡眠记录")
		oldOut := text(t, func() (string, error) { return d.Sleep(context.Background(), old, old) })
		requireParts(t, oldOut, "| "+old+" | 7.5 |")
	})

	t.Run("相邻夜晚的后半夜不被误删", func(t *testing.T) {
		d := newDeps(t)
		prev, target := day(-2), day(-1)
		putSleep(t, d, *sleepNight(prev), *sleepNight(target))
		putSamples(t, d,
			seg(prev, 23*60+30, 40, "AsleepDeep", "Apple Watch"),
			seg(prev, 24*60+10, 120, "AsleepCore", "Apple Watch"),
			seg(target, 23*60+30, 40, "AsleepDeep", "Apple Watch"),
			seg(target, 24*60+10, 120, "AsleepCore", "Apple Watch"),
		)

		preview := text(t, func() (string, error) { return d.SleepDelete(context.Background(), target, target, false) })
		requireParts(t, preview, "- 睡眠会话 1 条", "- 分期样本 2 条")

		text(t, func() (string, error) { return d.SleepDelete(context.Background(), target, target, true) })
		// 前一晚 00:10 的分段 local_date 正好落在被删区间里，但它属于要保留的
		// 前一晚（时间窗内），不能按日期当成孤儿段清掉。
		if n := count(t, d, `SELECT count(*) FROM samples WHERE metric='sleep_analysis'`); n != 2 {
			t.Errorf("前一晚的分期应完整保留，实际剩 %d", n)
		}
		if n := count(t, d, `SELECT count(*) FROM samples WHERE metric='sleep_analysis' AND local_date=$1`, target); n != 1 {
			t.Errorf("前一晚跨零点的分段不应被误删，实际剩 %d", n)
		}
	})

	t.Run("留空即全部历史", func(t *testing.T) {
		d, old, recent := seed(t)
		// 多留一天只有分段、没有会话的数据。
		putSamples(t, d, seg(day(-5), 23*60+30, 40, "AsleepDeep", "Apple Watch"))

		preview := text(t, func() (string, error) { return d.SleepDelete(context.Background(), "", "", false) })
		requireParts(t, preview, "（全部历史）", "- 睡眠会话 2 条", "- 分期样本 5 条")

		text(t, func() (string, error) { return d.SleepDelete(context.Background(), "", "", true) })
		if n := count(t, d, `SELECT count(*) FROM sleep_sessions`); n != 0 {
			t.Errorf("全部历史应删掉所有会话，实际剩 %d", n)
		}
		if n := count(t, d, `SELECT count(*) FROM samples WHERE metric='sleep_analysis'`); n != 0 {
			t.Errorf("全部历史应删掉所有分期，实际剩 %d", n)
		}
		if n := count(t, d, `SELECT count(*) FROM daily_metrics_day WHERE metric=$1`, postgres.SleepMetric); n != 0 {
			t.Errorf("全部历史应清掉天级睡眠值，实际剩 %d", n)
		}
		// 其他指标不受影响：两个旧日期的步数都应留着
		for _, date := range []string{old, recent} {
			if _, ok := dayValue(t, d, "step_count", date); !ok {
				t.Errorf("%s 的步数天级值不应被删除", date)
			}
		}
	})

	t.Run("只剩天级值也能清掉", func(t *testing.T) {
		d := newDeps(t)
		stale := day(-5)
		if _, err := d.DB.Pool.Exec(context.Background(),
			`INSERT INTO daily_metrics_day (local_date, metric, value) VALUES ($1,$2,7)`,
			stale, postgres.SleepMetric); err != nil {
			t.Fatal(err)
		}
		preview := text(t, func() (string, error) { return d.SleepDelete(context.Background(), stale, stale, false) })
		requireParts(t, preview, "没有睡眠会话或分期样本，但有 1 天的天级睡眠值残留")

		text(t, func() (string, error) { return d.SleepDelete(context.Background(), stale, stale, true) })
		if n := count(t, d, `SELECT count(*) FROM daily_metrics_day WHERE metric=$1 AND local_date=$2`, postgres.SleepMetric, stale); n != 0 {
			t.Errorf("残留天级值应清掉，实际剩 %d", n)
		}
	})

	t.Run("空库与非法区间", func(t *testing.T) {
		d := newDeps(t)
		out := text(t, func() (string, error) { return d.SleepDelete(context.Background(), day(-1), day(-1), false) })
		requireParts(t, out, "范围内没有睡眠记录")
		out = text(t, func() (string, error) { return d.SleepDelete(context.Background(), day(-1), day(-1), true) })
		requireParts(t, out, "未做任何修改")

		msg := fail(t, func() (string, error) { return d.SleepDelete(context.Background(), day(0), day(-1), false) })
		requireParts(t, msg, "结束日期早于起始日期")
	})
}

func TestWorkouts(t *testing.T) {
	t.Run("列表与空字段", func(t *testing.T) {
		d := newDeps(t)
		putWorkouts(t, d,
			sample.Workout{
				Name: "户外跑步", Source: "Apple Watch", LocalDate: day(-1), TZOffsetMin: 480,
				StartTime: at(day(-1), 7, 0), EndTime: at(day(-1), 7, 32), DurationMin: 32.4,
				TotalEnergy: f64(299.6), EnergyUnit: "kcal", TotalDistance: f64(5.234), DistanceUnit: "km",
			},
			sample.Workout{
				Name: "室内骑行", Source: "iPhone", LocalDate: day(0), TZOffsetMin: 480,
				StartTime: at(day(0), 18, 0), EndTime: at(day(0), 18, 45), DurationMin: 45,
			},
		)
		out := text(t, func() (string, error) { return d.Workouts(context.Background(), day(-1), day(0)) })
		requireParts(t, out,
			"运动 "+day(-1)+" ~ "+day(0)+"（2 次）",
			"| 日期 | 项目 | 时长(min) | 消耗 | 距离 | 来源 |",
			"| "+day(-1)+" | 户外跑步 | 32 | 300 kcal | 5.23 km | Apple Watch |",
			// 没给消耗/距离的运动必须是破折号，不能写成 0
			"| "+day(0)+" | 室内骑行 | 45 | — | — | iPhone |",
		)
		// 按起始时刻倒序：今天的排最前
		if strings.Index(out, "室内骑行") > strings.Index(out, "户外跑步") {
			t.Errorf("运动应按时间倒序\n%s", out)
		}
	})

	t.Run("默认30天窗口", func(t *testing.T) {
		d := newDeps(t)
		putWorkouts(t, d,
			sample.Workout{Name: "步行", Source: "iPhone", LocalDate: day(-29), TZOffsetMin: 480,
				StartTime: at(day(-29), 9, 0), EndTime: at(day(-29), 9, 30), DurationMin: 30},
			sample.Workout{Name: "远足", Source: "iPhone", LocalDate: day(-30), TZOffsetMin: 480,
				StartTime: at(day(-30), 9, 0), EndTime: at(day(-30), 12, 0), DurationMin: 180},
		)
		out := text(t, func() (string, error) { return d.Workouts(context.Background(), "", "") })
		requireParts(t, out, "运动 "+day(-29)+" ~ "+day(0)+"（1 次）", "步行")
		forbidParts(t, out, "远足", day(-30))
	})

	t.Run("空数据", func(t *testing.T) {
		d := newDeps(t)
		out := text(t, func() (string, error) { return d.Workouts(context.Background(), "2020-01-01", "2020-01-02") })
		if out != "2020-01-01 ~ 2020-01-02 没有运动记录" {
			t.Errorf("空数据文案错: %q", out)
		}
	})

	t.Run("非法日期", func(t *testing.T) {
		d := newDeps(t)
		msg := fail(t, func() (string, error) { return d.Workouts(context.Background(), "", "not-a-date") })
		requireParts(t, msg, "日期格式应为 YYYY-MM-DD")
	})
}

func TestCoverage(t *testing.T) {
	dbCurrentDate := func(t *testing.T, d *Deps) string {
		t.Helper()
		var s string
		if err := d.DB.Pool.QueryRow(context.Background(), `SELECT CURRENT_DATE::text`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	t.Run("尚无同步记录", func(t *testing.T) {
		d := newDeps(t)
		cur := dbCurrentDate(t, d)
		out := text(t, func() (string, error) { return d.Coverage(context.Background(), 0) })
		requireParts(t, out, "最近同步: 尚无记录",
			// days<=0 一律回 30；空表时缺口从 CURRENT_DATE-30 一路数到今天，共 31 个日期
			"近 30 天缺失 31 天: ",
			", "+cur+"\n", // 今天排在列表末尾
		)
		forbidParts(t, out, "同步水位", "无缺失日")
	})

	t.Run("最近同步与水位", func(t *testing.T) {
		d := newDeps(t)
		ctx := context.Background()
		runID, dup, err := d.DB.BeginRun(ctx, postgres.RunMeta{Transport: "rest", AutomationID: "auto-1", SessionID: "sess-c"})
		if err != nil || dup {
			t.Fatalf("登记摄入失败: %v dup=%v", err, dup)
		}
		if err := d.DB.FinishRun(ctx, runID, postgres.IngestStats{Received: 8, Inserted: 7, Duplicates: 1}, nil); err != nil {
			t.Fatal(err)
		}
		if err := d.DB.UpdateWatermark(ctx, "auto-1", map[string]postgres.Watermark{
			"step_count": {Time: at(day(0), 8, 0), LocalDate: day(0)},
		}); err != nil {
			t.Fatal(err)
		}
		putDaily(t, d, daily("step_count", dbCurrentDate(t, d), 100))

		out := text(t, func() (string, error) { return d.Coverage(context.Background(), 7) })
		requireParts(t, out,
			"（rest，收到 8 / 入库 7 / 重复 1，状态 ok）",
			"近 7 天无缺失日",
			"同步水位（automation × metric）",
			"- auto-1 / step_count: "+day(0)+" (",
		)
	})

	t.Run("缺失日明细", func(t *testing.T) {
		d := newDeps(t)
		cur := dbCurrentDate(t, d)
		// 缺口是按 step_count 有没有聚合行判的
		putDaily(t, d, daily("active_energy", cur, 10))
		out := text(t, func() (string, error) { return d.Coverage(context.Background(), 30) })
		requireParts(t, out, "近 30 天缺失 1 天: "+cur)
		forbidParts(t, out, "无缺失日")
	})

	t.Run("非法日期以外的错误路径", func(t *testing.T) {
		d := newDeps(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		msg := fail(t, func() (string, error) { return d.Coverage(ctx, 30) })
		requireParts(t, msg, "context")
	})
}

func TestCorrelation(t *testing.T) {
	// putPair 把两个指标在同一天集合上各写一条样本。
	putPair := func(t *testing.T, d *Deps, days []int, va, vb func(int) float64) {
		t.Helper()
		var rows []row
		for _, i := range days {
			rows = append(rows, daily("step_count", day(i), va(i)), daily("active_energy", day(i), vb(i)))
		}
		putDaily(t, d, rows...)
	}

	t.Run("完全正相关", func(t *testing.T) {
		d := newDeps(t)
		putPair(t, d, []int{-3, -2, -1},
			func(i int) float64 { return float64(-i) },
			func(i int) float64 { return 2 * float64(-i) })
		out := text(t, func() (string, error) {
			return d.Correlation(context.Background(), "step_count", "active_energy", day(-3), day(-1))
		})
		requireParts(t, out,
			"步数(step_count) 与 活动能量(active_energy)（"+day(-3)+"~"+day(-1)+"，3 天对齐）皮尔逊相关系数 r = 1.000",
			"参考：|r|>0.5 视为中等以上相关",
		)
	})

	t.Run("完全负相关", func(t *testing.T) {
		d := newDeps(t)
		putPair(t, d, []int{-3, -2, -1},
			func(i int) float64 { return float64(-i) },
			func(i int) float64 { return 4 - float64(-i) })
		out := text(t, func() (string, error) {
			return d.Correlation(context.Background(), "step_count", "active_energy", day(-3), day(-1))
		})
		requireParts(t, out, "r = -1.000")
	})

	t.Run("只算两边都有数据的日期", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d,
			daily("step_count", day(-4), 4), daily("step_count", day(-3), 3),
			daily("step_count", day(-2), 2), daily("step_count", day(-1), 1),
			daily("active_energy", day(-3), 3), daily("active_energy", day(-2), 2),
			daily("active_energy", day(-1), 1),
		)
		out := text(t, func() (string, error) {
			return d.Correlation(context.Background(), "step_count", "active_energy", day(-4), day(-1))
		})
		requireParts(t, out, "，3 天对齐）皮尔逊相关系数 r = 1.000")
	})

	t.Run("默认90天窗口", func(t *testing.T) {
		d := newDeps(t)
		putPair(t, d, []int{-89, -88, -87, -90},
			func(i int) float64 { return float64(-i) },
			func(i int) float64 { return 2 * float64(-i) })
		out := text(t, func() (string, error) {
			return d.Correlation(context.Background(), "step_count", "active_energy", "", "")
		})
		// -90 必须落在窗口外：一旦被算进来就是 4 天对齐
		requireParts(t, out, "（"+day(-89)+"~"+day(0)+"，3 天对齐）")
	})

	t.Run("对齐天数不足", func(t *testing.T) {
		d := newDeps(t)
		putPair(t, d, []int{-2, -1},
			func(i int) float64 { return float64(-i) },
			func(i int) float64 { return float64(-i) })
		out := text(t, func() (string, error) {
			return d.Correlation(context.Background(), "step_count", "active_energy", day(-2), day(-1))
		})
		if out != "可对齐的日期不足（2 天），无法计算相关性" {
			t.Errorf("样本不足文案错: %q", out)
		}
	})

	t.Run("方差为0", func(t *testing.T) {
		d := newDeps(t)
		putPair(t, d, []int{-3, -2, -1},
			func(i int) float64 { return float64(-i) },
			func(int) float64 { return 5 })
		out := text(t, func() (string, error) {
			return d.Correlation(context.Background(), "step_count", "active_energy", day(-3), day(-1))
		})
		requireParts(t, out, "步数(step_count) 与 活动能量(active_energy) 在 "+day(-3)+"~"+day(-1)+" 内方差为 0，无法计算相关性")
	})

	t.Run("未登记指标报错", func(t *testing.T) {
		d := newDeps(t)
		msg := fail(t, func() (string, error) { return d.Correlation(context.Background(), "bogus", "step_count", "", "") })
		if msg != "指标未登记: bogus / step_count" {
			t.Errorf("报错文案错: %q", msg)
		}
	})

	t.Run("两侧都未登记", func(t *testing.T) {
		d := newDeps(t)
		msg := fail(t, func() (string, error) { return d.Correlation(context.Background(), "b1", "b2", "", "") })
		requireParts(t, msg, "指标未登记: b1 / b2")
	})
}

// TestDBErrorPaths 用取消过的 context 真的把仓储层打到出错，
// 而不是靠参数校验伪造一个「错误」。
func TestDBErrorPaths(t *testing.T) {
	d := newDeps(t)
	putDaily(t, d, daily("step_count", day(0), 100))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name string
		call func(context.Context) (string, error)
	}{
		{"DailySummary", func(c context.Context) (string, error) { return d.DailySummary(c, day(0)) }},
		{"Query", func(c context.Context) (string, error) { return d.Query(c, "step_count", day(0), day(0)) }},
		{"Compare", func(c context.Context) (string, error) { return d.Compare(c, "step_count", "", "", "", "") }},
		{"Latest", func(c context.Context) (string, error) { return d.Latest(c, "step_count") }},
		{"Sleep", func(c context.Context) (string, error) { return d.Sleep(c, day(-29), day(0)) }},
		{"SleepDetail", func(c context.Context) (string, error) { return d.SleepDetail(c, day(0)) }},
		{"SleepDelete", func(c context.Context) (string, error) { return d.SleepDelete(c, "", "", true) }},
		{"Workouts", func(c context.Context) (string, error) { return d.Workouts(c, day(-29), day(0)) }},
		{"Correlation", func(c context.Context) (string, error) {
			return d.Correlation(c, "step_count", "step_count", day(-29), day(0))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := fail(t, func() (string, error) { return tc.call(ctx) })
			if !strings.Contains(msg, "context") {
				t.Errorf("应透出数据库层的上下文取消错误，实际 %q", msg)
			}
		})
	}
}

func TestDepsFrom(t *testing.T) {
	db := sharedDB(t)
	reg := metric.New(metric.Defaults())
	d := DepsFrom(db, reg, testLoc, testTZName, false)
	if d.DB != db || d.Metrics != reg || d.Location != testLoc || d.Timezone != testTZName {
		t.Fatal("DepsFrom 没把依赖原样装进去")
	}
	if got := d.today(); !strings.HasPrefix(got, time.Now().In(testLoc).Format("2006")) {
		t.Errorf("today 应是本地日期: %q", got)
	}
	if d.shift(-1) == d.today() {
		t.Error("shift 必须按天位移")
	}
}

func TestMetricLabel(t *testing.T) {
	d := &Deps{Metrics: metric.New([]metric.Def{{Metric: "step_count", DisplayNameZH: "步数", Enabled: true}})}
	if got := metricLabel(d, "step_count"); got != "步数" {
		t.Errorf("应只给中文名（睡眠详情里代号没有阅读价值）: %q", got)
	}
	if got := metricLabel(d, "nope"); got != "nope" {
		t.Errorf("未登记指标应原样返回: %q", got)
	}
	// 登记了但没中文名，也不能输出空串
	blank := &Deps{Metrics: metric.New([]metric.Def{{Metric: "x_metric", DisplayNameZH: ""}})}
	if got := metricLabel(blank, "x_metric"); got != "x_metric" {
		t.Errorf("缺中文名时应回落到代号: %q", got)
	}
}
