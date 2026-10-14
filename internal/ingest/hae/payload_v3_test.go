package hae

import (
	"encoding/json"
	"os"
	"testing"
)

// 2026 版 HAE 导出（下称 v3）相对 v2 改了三处字段形态，而这三处正好是
// 「一个字段拖垮整份导出」的典型：
//
//   - workouts[].source   字符串  → 对象 {name, identifier}
//   - workouts[].duration 分钟    → 秒
//   - 运动距离字段         totalDistance → distance
//
// 按 v2 的 Go 结构体去解析 v3，encoding/json 会抛 UnmarshalTypeError，
// ParsePayload 直接报错、端点回 400，34 个指标一条也入不了库。
// 这组用例把「v3 必须能解析、字段必须落到正确位置、v2 形态不能因此坏掉」钉死。
func loadV3(t *testing.T) *Payload {
	t.Helper()
	f, err := os.Open("../../../testdata/hae_export_v3.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := ParsePayload(f)
	if err != nil {
		t.Fatalf("v3 导出必须能解析（source 为对象不应导致整包失败）: %v", err)
	}
	return p
}

func TestParsePayloadAcceptsNewExportShape(t *testing.T) {
	p := loadV3(t)
	if len(p.Data.Metrics) != 6 || len(p.Data.Workouts) != 2 {
		t.Fatalf("v3 样例解析数量错误: metrics=%d workouts=%d", len(p.Data.Metrics), len(p.Data.Workouts))
	}

	// 对象形态的来源：取 name，不带 identifier 的壳。
	// 样例文件里的名字带 NBSP（真实导出就是这样），出口处统一收成普通空格。
	w := p.Data.Workouts[0]
	if got := string(w.Source); got != "子翼的Apple Watch" {
		t.Fatalf("对象来源应取 name 字段并归一空白，实际 %q", got)
	}
	if !w.Duration.Set || w.Duration.Value != 890.4655394554138 {
		t.Fatalf("新版 duration（秒）解析错误: %+v", w.Duration)
	}
	if !w.Distance.Qty.Set || w.Distance.Qty.Value != 0.9632032549719515 || w.Distance.Units != "km" {
		t.Fatalf("distance 字段解析错误: %+v", w.Distance)
	}
	if w.TotalDistance.Qty.Set {
		t.Fatalf("v3 里没有 totalDistance，不该解析出值: %+v", w.TotalDistance)
	}
	if w.TotalEnergy.Units != "kJ" {
		t.Fatalf("总能耗单位错误: %+v", w.TotalEnergy)
	}

	// 同一份导出里混着旧形态：两版必须都能解析，迁移期不会因为混排而丢数据
	legacy := p.Data.Workouts[1]
	if string(legacy.Source) != "Apple Watch" {
		t.Fatalf("字符串来源仍须正常解析，实际 %q", string(legacy.Source))
	}
	if !legacy.TotalDistance.Qty.Set || legacy.TotalDistance.Qty.Value != 2.4 {
		t.Fatalf("旧版 totalDistance 仍须解析，实际 %+v", legacy.TotalDistance)
	}
	if legacy.Distance.Qty.Set {
		t.Fatalf("旧版没有 distance，不该解析出值: %+v", legacy.Distance)
	}
}

// SourceName 的容错口径：认不出的形态按空串处理，绝不返回错误。
// 一旦这里返回 error，encoding/json 会让整份 payload 解析失败——代价是不对称的。
func TestSourceNameUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"字符串形态（旧版）", `"source":"Apple Watch"`, "Apple Watch"},
		{"对象形态（新版）", `"source":{"name":"子翼的Apple Watch","identifier":"com.apple.health.x"}`, "子翼的Apple Watch"},
		// 真实导出用的是 NBSP：出口必须收成普通空格，否则同一台设备会分裂成两个来源
		{"字符串里的 NBSP", `"source":"子翼的Apple\u00a0Watch"`, "子翼的Apple Watch"},
		{"对象 name 里的 NBSP", `"source":{"name":"子翼的Apple\u00a0Watch"}`, "子翼的Apple Watch"},
		{"多来源拼串归一管空格", `"source":"子翼的Apple\u00a0Watch|子翼的iPhone"`, "子翼的Apple Watch | 子翼的iPhone"},
		{"对象缺少 name", `"source":{"identifier":"x"}`, ""},
		{"空字符串", `"source":""`, ""},
		{"JSON null", `"source":null`, ""},
		{"数字", `"source":123`, ""},
		{"数组", `"source":["a","b"]`, ""},
		{"布尔", `"source":true`, ""},
		{"字段缺失", `"other":1`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w Workout
			if err := json.Unmarshal([]byte(`{`+tt.raw+`}`), &w); err != nil {
				t.Fatalf("来源形态异常不得导致解析失败: %v", err)
			}
			if string(w.Source) != tt.want {
				t.Fatalf("来源应为 %q，实际 %q", tt.want, string(w.Source))
			}
		})
	}
}

func TestPointSourceAcceptsObject(t *testing.T) {
	// 指标点目前是字符串来源，但新版把运动来源改成对象之后，同一份代码里两种位置都可能变。
	// 点位上的来源同样走容错类型，避免同类故障在指标侧重演。
	var p Point
	if err := json.Unmarshal([]byte(`{"source":{"name":"子翼的Apple Watch"}}`), &p); err != nil {
		t.Fatal(err)
	}
	if string(p.Source) != "子翼的Apple Watch" {
		t.Fatalf("点位来源应取 name，实际 %q", string(p.Source))
	}
}

// Quantity 的容错口径：对象形态正常取值，裸数值取数不取单位，
// 数组/布尔等无法解释的形态整体忽略——都不允许报错。
func TestQuantityUnmarshalTolerant(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantSet   bool
		wantQty   float64
		wantUnits string
	}{
		{"对象形态", `{"qty":316.9891202958506,"units":"kJ"}`, true, 316.9891202958506, "kJ"},
		{"字符串数值", `{"qty":"120","units":"kcal"}`, true, 120, "kcal"},
		{"只有数值没有单位", `{"qty":5}`, true, 5, ""},
		{"裸数值", `11.5`, true, 11.5, ""},
		{"数值字符串", `"11.5"`, true, 11.5, ""},
		{"数组（逐点距离）", `[{"qty":1,"units":"km"}]`, false, 0, ""},
		{"JSON null", `null`, false, 0, ""},
		{"布尔", `true`, false, 0, ""},
		{"状态文本", `{"qty":"high","units":"x"}`, false, 0, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var q Quantity
			if err := json.Unmarshal([]byte(tt.raw), &q); err != nil {
				t.Fatalf("任何数量形态都不该导致解析失败: %v", err)
			}
			if q.Qty.Set != tt.wantSet {
				t.Fatalf("是否取到值应为 %v，实际 %v（%+v）", tt.wantSet, q.Qty.Set, q.Qty)
			}
			if tt.wantSet && q.Qty.Value != tt.wantQty {
				t.Fatalf("数值应为 %v，实际 %v", tt.wantQty, q.Qty.Value)
			}
			if q.Units != tt.wantUnits {
				t.Fatalf("单位应为 %q，实际 %q", tt.wantUnits, q.Units)
			}
		})
	}
}

func TestQuantityInvalidJSONStillErrors(t *testing.T) {
	// 容错只针对「形态不认识」，非法 JSON 该报错还得报错，否则解析层会静默吞掉损坏的负载。
	var q Quantity
	if err := json.Unmarshal([]byte(`{"qty":}`), &q); err == nil {
		t.Fatal("损坏的 JSON 必须报错")
	}
}
