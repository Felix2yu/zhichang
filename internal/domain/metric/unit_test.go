package metric

import (
	"math"
	"testing"
)

// 单位换算的唯一职责：把 payload 自带单位对齐到字典声明单位。
// 这里锁三件事——该换算的必须换算、不该换算的绝不能动、未知单位必须原样保留。
func TestConvertUnit(t *testing.T) {
	tests := []struct {
		name      string
		value     float64
		from      string
		to        string
		wantValue float64
		wantUnit  string
		reason    string
	}{
		{
			name: "活动能量 kJ 换成 kcal", value: 1090.86, from: "kJ", to: "kcal",
			wantValue: 1090.86 / 4.184, wantUnit: "kcal",
			reason: "HAE 新版本给 kJ，字典声明 kcal；不换算会让「1091 kcal」虚高 4.18 倍",
		},
		{
			name: "基础能量 kJ 换成 kcal", value: 7885.5496910674565, from: "kJ", to: "kcal",
			wantValue: 7885.5496910674565 / 4.184, wantUnit: "kcal",
			reason: "同上，基础能量走的是同一条导入路径",
		},
		{
			name: "kcal 换回 kJ", value: 260, from: "kcal", to: "kJ",
			wantValue: 260 * 4.184, wantUnit: "kJ",
			reason: "反向因子必须存在，否则字典改口后换算会静默失效",
		},
		{
			name: "能量单位一致时原样", value: 260, from: "kcal", to: "kcal",
			wantValue: 260, wantUnit: "kcal",
		},
		{
			name: "英里换成公里", value: 3, from: "mi", to: "km",
			wantValue: 3 * 1.609344, wantUnit: "km",
		},
		{
			name: "米换成公里", value: 1800, from: "m", to: "km",
			wantValue: 1.8, wantUnit: "km",
		},
		{
			name: "磅换成公斤", value: 150, from: "lb", to: "kg",
			wantValue: 150 * 0.45359237, wantUnit: "kg",
		},
		{
			name: "大小写与空白差异只统一写法不换算", value: 93.5, from: " ML ", to: "mL",
			wantValue: 93.5, wantUnit: "mL",
			reason: "单位同义，值不能动；只把写法对齐到字典口径",
		},
		{
			name: "ml 与 mL 视为同一单位", value: 1700, from: "ml", to: "mL",
			wantValue: 1700, wantUnit: "mL",
		},
		{
			name: "count/min 与 bpm 不是换算关系", value: 78.5, from: "count/min", to: "bpm",
			wantValue: 78.5, wantUnit: "count/min",
			reason: "数值本就相同，只是标签差异；擅自改单位会篡改上游原文",
		},
		{
			name: "输入量的两种写法不是换算关系", value: 32.38, from: "ml/(kg·min)", to: "mL/min·kg",
			wantValue: 32.38, wantUnit: "ml/(kg·min)",
		},
		{
			name: "呼吸频率两种写法不是换算关系", value: 12.6, from: "count/min", to: " breaths/min",
			wantValue: 12.6, wantUnit: "count/min",
		},
		{
			name: "未知单位不猜系数", value: 12, from: "u", to: "mg/dL",
			wantValue: 12, wantUnit: "u",
			reason: "最小字典与自定义指标都走这条路，猜错比不换算更糟",
		},
		{
			name: "目标单位为空时不动", value: 12, from: "kJ", to: "",
			wantValue: 12, wantUnit: "kJ",
			reason: "未登记指标的 def 是零值，Unit 为空；此时必须原样保留",
		},
		{
			name: "源单位为空时不动", value: 12, from: "", to: "kcal",
			wantValue: 12, wantUnit: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, gotUnit := ConvertUnit(tt.value, tt.from, tt.to)
			if math.Abs(gotValue-tt.wantValue) > 1e-9 {
				t.Fatalf("换算值应为 %v，实际 %v（%s）", tt.wantValue, gotValue, tt.reason)
			}
			if gotUnit != tt.wantUnit {
				t.Fatalf("单位应为 %q，实际 %q（%s）", tt.wantUnit, gotUnit, tt.reason)
			}
		})
	}
}

func TestConvertUnitIsReversible(t *testing.T) {
	// 正反换算必须回到原点：表里一侧改了另一侧忘改，会让「字典改口」变成静默数据损坏。
	for _, pair := range [][2]string{{"kJ", "kcal"}, {"m", "km"}, {"mi", "km"}, {"lb", "kg"}} {
		back, _ := ConvertUnit(1234.5, pair[0], pair[1])
		round, _ := ConvertUnit(back, pair[1], pair[0])
		if math.Abs(round-1234.5) > 1e-6 {
			t.Fatalf("%s → %s → %s 往返失真: %v", pair[0], pair[1], pair[0], round)
		}
	}
}
