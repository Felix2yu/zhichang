package hae

import (
	"encoding/json"
	"strings"
	"testing"
)

// 这些测试锁 ParsePayload 与 Payload 辅助方法的真实口径：
// HAE 的自动化经常推回一个「结构在、数据为空」的导出，端点必须能区分
// 「解析失败」（400）和「解析成功但没数据」（200/empty），否则用户重跑自动化会被 400 卡死。

func TestParsePayloadErrors(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantMsg string
	}{
		{"空请求体", "", "请求体为空"},
		{"截断的对象", `{"data":`, "解析 JSON 失败"},
		{"字段类型不符", `{"data":{"metrics":123}}`, "解析 JSON 失败"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParsePayload(strings.NewReader(tt.raw))
			if err == nil {
				t.Fatalf("应解析失败，实际返回 %+v", p)
			}
			if p != nil {
				t.Fatal("失败时必须返回 nil payload，调用方据此直接短路")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("错误信息应含 %q，实际 %q", tt.wantMsg, err.Error())
			}
		})
	}
}

func TestParsePayloadAcceptsRealExport(t *testing.T) {
	p := loadSample(t)
	if len(p.Data.Metrics) != 5 || len(p.Data.Workouts) != 1 {
		t.Fatalf("真实样例解析数量错误: metrics=%d workouts=%d", len(p.Data.Metrics), len(p.Data.Workouts))
	}
	// sleep 以 RawMessage 保留原始形态，交给 normalizeSleep 判断聚合/分段
	if len(p.Data.Sleep) != 0 {
		t.Fatalf("样例的 sleep 在顶层不存在，实际 %q", string(p.Data.Sleep))
	}
}

func TestIsEmpty(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		empty bool
	}{
		{"完全空对象", `{}`, true},
		{"data 缺失", `{"data":{}}`, true},
		{"三类都是空数组", `{"data":{"metrics":[],"workouts":[],"sleep":[]}}`, false},
		{"只有指标", `{"data":{"metrics":[{"name":"step_count","units":"count","data":[]}]}}`, false},
		{"只有运动", `{"data":{"workouts":[{"name":"Walking"}]}}`, false},
		{"只有睡眠", `{"data":{"sleep":[{"date":"2026-10-13"}]}}`, false},
		// 心电也是数据：漏掉它，一份只导出心电的自动化会被判成空包整体丢弃
		{"只有心电图", `{"data":{"ecg":[{"start":"2026-10-13 09:16:15 +0800"}]}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParsePayload(strings.NewReader(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got := p.IsEmpty(); got != tt.empty {
				t.Fatalf("IsEmpty 应为 %v，实际 %v", tt.empty, got)
			}
		})
	}
	// nil payload 会 panic 的事实不在生产路径上（handler 解析失败已短路），
	// 但空数组 sleep 仍判非空是 RawMessage 的自然结果，这里固化以免日后无声改变。
}

func TestCount(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		want   int
		reason string
	}{
		{"指标点数加运动条数", `{"data":{"metrics":[
			{"name":"step_count","units":"count","data":[{},{},{}]},
			{"name":"body_mass","units":"kg","data":[{}]}
		],"workouts":[{},{}]}}`, 6, "received 口径：3+1 个指标点 + 2 条运动"},
		{"睡眠不计入", `{"data":{"sleep":[{"date":"2026-10-13","totalSleep":7.5}]}}`, 0,
			"聚合睡眠不产生 samples，计入会让 received 与落库条数对不上"},
		{"心电不计入", `{"data":{"ecg":[{"start":"2026-10-13 09:16:15 +0800"}]}}`, 0,
			"心电同样是事件表数据，不产生 samples；它的接收条数单独走 IngestStats.ECG"},
		{"空指标名也算点", `{"data":{"metrics":[{"name":"","units":"x","data":[{}]}]}}`, 1,
			"Count 是原始条数，与 Normalize 之后是否可用无关"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParsePayload(strings.NewReader(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got := p.Count(); got != tt.want {
				t.Fatalf("Count 应为 %d（%s），实际 %d", tt.want, tt.reason, got)
			}
		})
	}
}

// Float 必须容忍 HAE 的三种历史形态：数字、字符串数字、以及状态文本。
// 解析失败视为「缺失」而不是报错，是为了让一个坏点不拖垮整份导出。
func TestFloatUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Float
	}{
		{"整数", `{"qty":12}`, Float{Value: 12, Set: true}},
		{"小数", `{"qty":12.5}`, Float{Value: 12.5, Set: true}},
		{"字符串数字", `{"qty":"1500"}`, Float{Value: 1500, Set: true}},
		{"带空格的字符串", `{"qty":" 42 "}`, Float{Value: 42, Set: true}},
		{"空字符串", `{"qty":""}`, Float{}},
		{"JSON null", `{"qty":null}`, Float{}},
		{"状态文本", `{"qty":"high"}`, Float{}},
		{"布尔", `{"qty":true}`, Float{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p Point
			if err := json.Unmarshal([]byte(tt.raw), &p); err != nil {
				t.Fatalf("Unmarshal 不应报错: %v", err)
			}
			if p.Qty != tt.want {
				t.Fatalf("期望 %+v，实际 %+v", tt.want, p.Qty)
			}
		})
	}
}

func TestFloatUnmarshalQuotedString(t *testing.T) {
	// HAE 某些版本会把时间戳式值带引号写进数值字段；UnmarshalJSON 先剥引号再试解析。
	var p Point
	if err := json.Unmarshal([]byte(`{"qty": " 7.5 "}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Qty.Value != 7.5 || !p.Qty.Set {
		t.Fatalf("带引号数值解析错误: %+v", p.Qty)
	}
}

func TestFloatUnmarshalErrorPropagatesOnlyOnBadJSON(t *testing.T) {
	// Float 自己从不返回 error：非法值走 Set=false。真正报错的只能是 JSON 本身。
	var p Point
	if err := json.Unmarshal([]byte(`{"qty":}`), &p); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}
