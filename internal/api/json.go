// Package api 提供 HTTP 层的公共响应工具。
package api

import (
	"encoding/json"
	"net/http"
)

// WriteJSON 输出 JSON 响应。
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// WriteError 输出统一结构的错误响应。
func WriteError(w http.ResponseWriter, code int, msg string) {
	WriteJSON(w, code, map[string]any{"error": msg, "status": code})
}

// DecodeJSON 解析 JSON 请求体。
func DecodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
