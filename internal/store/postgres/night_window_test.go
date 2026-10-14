package postgres

import (
	"testing"
	"time"
)

// 纯函数单测：夜间窗口与分段装配。
// 这两个函数是 SleepStages 与 SleepStagesRange 共用的核心口径，
// 不碰数据库，所以任何机器上都能跑（集成测试要 ZHICHANG_TEST_DSN）。

// segAt 以「10-13 本地 00:00」为基准偏移 absMin 分钟造一段分期；
// 负数表示 10-12 夜里，正好覆盖跨零点的情形。
func segAt(absMin int, durMin int, src, stage string) rawSeg {
	st := at0800(10, 13, 0, 0).Add(time.Duration(absMin) * time.Minute)
	return rawSeg{
		Start: st, End: st.Add(time.Duration(durMin) * time.Minute),
		Source: src, TZ: 480, Raw: stage,
	}
}

func TestNightWindow(t *testing.T) {
	bed := at0800(10, 12, 23, 30)
	wake := at0800(10, 13, 7, 0)

	// 有会话：起点取「18:00 与入睡时刻里更早的那个」——23:30 入睡仍从 18:00 起画，
	// 午睡（13:00 入睡）才会把起点提前；起床 07:00 跨零点 → 420+1440=1860
	lo, hi := nightWindow(&bed, &wake, 480)
	if lo != 1080 || hi != 1860 {
		t.Fatalf("窗口应为 1080~1860，实际 %d~%d", lo, hi)
	}
	nap := at0800(10, 12, 13, 0)
	if lo, _ = nightWindow(&nap, &wake, 480); lo != 780 {
		t.Fatalf("13:00 入睡应把起点提前到 780，实际 %d", lo)
	}

	// 无会话：退化成「入睡日 18:00 → 次日 18:00」
	lo, hi = nightWindow(nil, nil, 0)
	if lo != 1080 || hi != 2520 {
		t.Fatalf("无会话窗口应为 1080~2520，实际 %d~%d", lo, hi)
	}
}

func TestAssembleNightSegments(t *testing.T) {
	// 窗口：23:30 入睡 → 07:00 起床
	const lo, hi = 1410, 1860
	nightDay := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

	raw := []rawSeg{
		segAt(-10, 30, "Apple Watch", "AsleepCore"), // 10-12 23:50 → 本夜轴 1430
		segAt(180, 60, "Apple Watch", "AsleepDeep"), // 10-13 03:00 → 1620（跨零点顺延）
		segAt(450, 30, "Apple Watch", "Awake"),      // 07:30 起床后：排除
		segAt(1200, 30, "Apple Watch", "AsleepREM"), // 次日晚 20:00 的另一觉：排除
		segAt(120, 5, "iPhone", "Awake"),            // 窗口内但覆盖率低的来源：被择优排除
		segAt(60, 0, "Apple Watch", "AsleepCore"),   // 零时长：排除
	}

	segs, totals, src := assembleNightSegments(raw, lo, hi, "Apple Watch", nightDay)
	if src != "Apple Watch" {
		t.Fatalf("会话来源应优先，实际 %q", src)
	}
	if len(segs) != 2 {
		t.Fatalf("期望 2 段（窗口外/零时长/非择优来源都排除），实际 %d：%v", len(segs), segs)
	}
	if segs[0].LocalMin != 1430 || segs[0].Stage != "core" {
		t.Fatalf("第一段应为 core@1430，实际 %s@%d", segs[0].Stage, segs[0].LocalMin)
	}
	if segs[1].LocalMin != 1620 || segs[1].Stage != "deep" {
		t.Fatalf("凌晨 03:00 应落在 1620，实际 %s@%d", segs[1].Stage, segs[1].LocalMin)
	}
	if totals["core"] < 0.49 || totals["core"] > 0.51 {
		t.Fatalf("core 合计应为 0.5h，实际 %v", totals["core"])
	}
	if totals["deep"] < 0.99 || totals["deep"] > 1.01 {
		t.Fatalf("deep 合计应为 1h，实际 %v", totals["deep"])
	}
	if _, ok := totals["awake"]; ok {
		t.Fatal("iPhone 那一段不该进入合计")
	}

	// 首选来源不在候选里：退回覆盖率最高的来源
	if _, _, src2 := assembleNightSegments(raw, lo, hi, "Whoop", nightDay); src2 != "Apple Watch" {
		t.Fatalf("应按覆盖率择优，实际 %q", src2)
	}

	// 宽窗口（无会话或入睡晚于 18:00 锚点时的真实窗口 [1080, 1860]）：
	// 次日 20:00 的墙钟是 1200，正好落进这个窗口——只按墙钟试两个候选值就会把
	// 次日那觉当成当晚 20:00 收进来。必须靠样本真实落在的第几天排除它。
	wideSegs, _, _ := assembleNightSegments(raw, 1080, 1860, "Apple Watch", nightDay)
	for _, s := range wideSegs {
		if s.Raw == "AsleepREM" {
			t.Fatalf("次日晚 20:00 的 REM 不该出现在当晚时间线上：%+v", s)
		}
	}
	if len(wideSegs) != 2 { // 仍只有 core@1430 与 deep@1620
		t.Fatalf("宽窗口下应有 2 段，实际 %d：%+v", len(wideSegs), wideSegs)
	}

	// 空输入也必须给非 nil 的 slice 与 map（JSON 契约：[] 而不是 null）
	e, et, es := assembleNightSegments(nil, lo, hi, "Apple Watch", nightDay)
	if e == nil || et == nil {
		t.Fatalf("空结果不能是 nil：%v / %v", e, et)
	}
	if es != "" {
		t.Fatalf("没有任何来源时不该凭空选出来源，实际 %q", es)
	}
}
