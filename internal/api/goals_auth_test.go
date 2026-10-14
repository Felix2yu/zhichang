package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// requireSettings 是这个服务唯一的写入口，鉴权分支必须逐条锁住：
// 未配置令牌时宁可 503 也不敞开，比对必须走常量时间比较。
func TestRequireSettings(t *testing.T) {
	cases := []struct {
		name  string
		token string // 服务端配置的令牌
		give  string // 请求头里的 Bearer 值
		want  int
	}{
		{"未配置令牌即只读", "", "whatever", http.StatusServiceUnavailable},
		{"空请求头", "s3cr3t", "", http.StatusUnauthorized},
		{"令牌错误", "s3cr3t", "wrong", http.StatusUnauthorized},
		{"令牌正确", "s3cr3t", "s3cr3t", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &Dashboard{settingsToken: c.token}
			h := d.requireSettings(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodPut, "/api/goals", nil)
			if c.give != "" {
				req.Header.Set("Authorization", "Bearer "+c.give)
			}
			rec := httptest.NewRecorder()
			h(rec, req)
			if rec.Code != c.want {
				t.Fatalf("期望 %d，实际 %d（body=%s）", c.want, rec.Code, rec.Body.String())
			}
		})
	}
}
