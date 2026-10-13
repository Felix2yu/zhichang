// Package web 提供前端静态资源的 HTTP 服务（含单页应用 fallback）。
package web

import (
	"io/fs"
	"net/http"
	"strings"
)

// Handler 返回静态文件处理器：命中文件直接返回，否则回落到 index.html。
func Handler(assets fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if f, err := assets.Open(name); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
