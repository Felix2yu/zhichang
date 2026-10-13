// Package web 提供前端静态资源的 HTTP 服务（含单页应用 fallback）。
package web

import (
	"io/fs"
	"net/http"
	"strings"
)

// Handler 返回静态文件处理器：命中文件直接返回，否则回落到 index.html。
//
// 两处必须显式处理：
//   - Go 的 mime.TypeByExtension(".webmanifest") 返回空串，交给 http.FileServer 会被
//     内容嗅探成 text/plain，浏览器因此拒绝 manifest（PWA 装不上且没有任何报错）。
//   - /api/** 不能被单页应用回落吞掉——调用方已在 chi 上先注册 /api 路由，
//     这里只对「确实不存在的文件」回落，命中文件的分支原样交给 FileServer。
func Handler(assets fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if f, err := assets.Open(name); err == nil {
			_ = f.Close()
			if strings.HasSuffix(name, ".webmanifest") {
				w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		// /api/** 不得回落成首页：否则拼错端点的客户端会拿到 200 + HTML，
		// 把 JSON 解析错误伪装成"数据为空"
		if name == "api" || strings.HasPrefix(name, "api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"接口不存在"}`))
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
