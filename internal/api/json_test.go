package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 这一层所有响应都出自 WriteJSON/WriteError：头、状态码、错误结构三件事
// 一旦被改坏，前端要么解析失败要么错误提示全空。
func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]any{"名称": "步数 <10000>"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q，缺 charset 浏览器会按本地编码猜", ct)
	}
	body := rec.Body.String()
	// SetEscapeHTML(false)：< > & 原样输出，否则错误文案里的 < 会变成 <
	// 被前端当文本渲染出来一排转义符
	if strings.Contains(body, `\<`) || !strings.Contains(body, `"名称":"步数 <10000>"`) {
		t.Fatalf("响应体不符: %s", body)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(body), &back); err != nil {
		t.Fatalf("写出的必须就是合法 JSON: %v", err)
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusBadRequest, "指标缺失或未登记")

	var back struct {
		Error  string `json:"error"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &back); err != nil {
		t.Fatalf("错误响应解析失败: %v (%s)", err, rec.Body.String())
	}
	if back.Error != "指标缺失或未登记" {
		t.Fatalf("error 文案丢失: %s", rec.Body.String())
	}
	// status 必须与 HTTP 状态码一致：前端拿它做分支，不一致就会走错兜底逻辑
	if rec.Code != http.StatusBadRequest || back.Status != rec.Code {
		t.Fatalf("status = %d，HTTP = %d", back.Status, rec.Code)
	}
}

// DecodeJSON 关掉了未知字段：payload 拼错字段名要当场报错，
// 而不是静默丢掉一个数字。
func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Metric string  `json:"metric"`
		Target float64 `json:"target"`
	}

	t.Run("正常解析", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/goals", strings.NewReader(`{"metric":"step_count","target":8000}`))
		var p payload
		if err := DecodeJSON(req, &p); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if p.Metric != "step_count" || p.Target != 8000 {
			t.Fatalf("字段丢失: %+v", p)
		}
	})

	t.Run("未知字段被拒", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/goals", strings.NewReader(`{"metric":"step_count","value":8000}`))
		var p payload
		if err := DecodeJSON(req, &p); err == nil {
			t.Fatal("拼错字段名必须报错，否则目标会被当成 0 写进去")
		}
	})

	t.Run("非法 JSON 被拒", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/goals", strings.NewReader(`{"metric":`))
		var p payload
		if err := DecodeJSON(req, &p); err == nil {
			t.Fatal("截断的 JSON 必须报错")
		}
	})
}
