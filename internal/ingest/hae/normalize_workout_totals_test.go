package hae

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// basalEnergy 在 2026 版导出里是逐分钟数组（真实导出 111 条运动全部是数组），
// 而结构体按 {qty,units} 对象解析：Quantity 的容错逻辑对数组返回「不认识」，
// 于是 workouts.meta 里的 basal_energy 一次也没落过库——代码看着在存，实际全丢，
// 而且界面上看不出任何矛盾（那一栏本来就是空的）。
//
// 数组按累计量口径求和；旧版的对象形态必须照常工作。
func TestNormalizeWorkoutBasalEnergyAcceptsPerMinuteArray(t *testing.T) {
	raw := `{"data":{"workouts":[{
		"name":"室内 骑行","start":"2026-05-03 07:00:00 +0800","end":"2026-05-03 07:20:00 +0800",
		"duration":1200,"source":"示例的Apple Watch",
		"basalEnergy":[
			{"date":"2026-05-03 07:01:00 +0800","qty":3,"units":"kJ","source":"示例的Apple Watch"},
			{"date":"2026-05-03 07:02:00 +0800","qty":6,"units":"kJ","source":"示例的Apple Watch"},
			{"date":"2026-05-03 07:03:00 +0800","units":"kJ","source":"示例的Apple Watch"}]
	},{
		"name":"户外 步行","start":"2026-05-04 07:00:00 +0800","end":"2026-05-04 07:10:00 +0800",
		"duration":600,"source":"示例的iPhone","basalEnergy":{"qty":20,"units":"kJ"}
	}]}}`

	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	if len(res.Workouts) != 2 {
		t.Fatalf("应有 2 条运动: %d skipped=%d", len(res.Workouts), res.Skipped)
	}

	// meta 是「只用于核对、不参与聚合」的一层，所以值与上游单位一起原样存（9 kJ），
	// 不在这里偷偷换成 kcal——换成字典单位反而会让它与同排的 avg_speed 口径不一致。
	arr, obj := res.Workouts[0], res.Workouts[1]
	if got := arr.Meta["basal_energy"]; got != 9.0 {
		t.Fatalf("数组形态应求和入库（3+6=9），实际 %v（meta=%v）", got, arr.Meta)
	}
	if arr.Meta["basal_energy_unit"] != "kJ" {
		t.Fatalf("基础能量的上游单位应随值一起留存，实际 %v", arr.Meta["basal_energy_unit"])
	}
	if got := obj.Meta["basal_energy"]; got != 20.0 {
		t.Fatalf("旧版对象形态被改坏了: %v", got)
	}
}

// 逐点形态要留在 Points 里而不是只留一个和：序列表要按分钟还原曲线，
// 丢掉逐点等于把「这次运动里能量什么时候花的」这个问题永久关掉。
// 缺 qty 的点不计数（求和与逐点都跳过它），但也不许让整条运动失败。
func TestNormalizeWorkoutTotalsKeepsPerPointSeries(t *testing.T) {
	p := mustParseTotals(t, `[{"date":"2026-05-03 07:01:00 +0800","qty":3,"units":"kJ"},
		{"date":"2026-05-03 07:02:00 +0800","qty":6,"units":"kJ"},
		{"date":"2026-05-03 07:03:00 +0800","units":"kJ"}]`)
	if !p.Qty.Set || p.Qty.Value != 9 {
		t.Fatalf("总量应为 9，实际 %+v", p.Qty)
	}
	if len(p.Points) != 2 {
		t.Fatalf("应只留下有数值的 2 个点，实际 %d 个", len(p.Points))
	}
	if p.Units != "kJ" {
		t.Fatalf("单位应取第一个带单位的点，实际 %q", p.Units)
	}
}

// 空的、纯文本的、布尔的形态都不能让解析失败：一个字段换形态，代价必须只是少一个字段。
func TestNormalizeWorkoutTotalsToleratesOtherShapes(t *testing.T) {
	for _, in := range []string{`[]`, `{}`, `null`, `"abc"`, `true`, `12.5`, `[{"qty":"x"}]`} {
		p := mustParseTotals(t, in)
		if in == `12.5` && (!p.Qty.Set || p.Qty.Value != 12.5) {
			t.Fatalf("裸数值应照旧取到 Qty: %+v", p)
		}
	}
}

func mustParseTotals(t *testing.T, jsonValue string) Totals {
	t.Helper()
	var got Totals
	if err := json.Unmarshal([]byte(jsonValue), &got); err != nil {
		t.Fatalf("Totals 解析 %s 失败: %v", jsonValue, err)
	}
	return got
}

// 平均心率这类速率字段**不给**求和的能力：数组换成和会得出一个没有意义的 bpm
// （一年 3267 个逐点心率加起来是六位数）。这条是 Totals 只挂在累计量上的理由，
// 用测试钉住，免得以后有人「顺手」把别的字段也换成 Totals。
func TestQuantityStillIgnoresArrays(t *testing.T) {
	raw := `{"data":{"workouts":[{
		"name":"户外 步行","start":"2026-05-03 07:00:00 +0800","end":"2026-05-03 07:10:00 +0800",
		"duration":600,"source":"示例的Apple Watch",
		"avgHeartRate":[{"qty":90,"units":"bpm"},{"qty":100,"units":"bpm"}]
	}]}}`
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	if len(res.Workouts) != 1 {
		t.Fatalf("负载形态不该让运动丢失: %d", len(res.Workouts))
	}
	if res.Workouts[0].AvgHR != nil {
		t.Fatalf("速率字段不该按数组求和，实际 %v", *res.Workouts[0].AvgHR)
	}
}
