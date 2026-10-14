package web

import (
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// 前端产物目录：只有 SPA 的资源，没有 api 目录。
func spaFS() fs.FS {
	return fstest.MapFS{
		"index.html":       {Data: []byte(`<html>SPA首页</html>`)},
		"app.js":           {Data: []byte(`console.log("app")`)},
		"site.webmanifest": {Data: []byte(`{"name":"知常"}`)},
	}
}

// 磁盘上真实存在 api/ 目录时（构建产物里塞了同名文件夹），命中文件的分支优先。
func apiDirFS() fs.FS {
	return fstest.MapFS{
		"index.html":     {Data: []byte(`<html>SPA首页</html>`)},
		"api/legacy.txt": {Data: []byte(`真实存在的文件`)},
	}
}

func get(t *testing.T, assets fs.FS, path string) (int, string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler(assets).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	res := rec.Result()
	return res.StatusCode, res.Header.Get("Content-Type"), rec.Body.String()
}

func TestHandlerServesExistingFile(t *testing.T) {
	assets := spaFS()
	code, _, body := get(t, assets, "/app.js")
	if code != 200 {
		t.Fatalf("命中文件应 200，实际 %d", code)
	}
	if body != `console.log("app")` {
		t.Fatalf("返回内容被改写: %q", body)
	}
}

func TestHandlerRootServesIndex(t *testing.T) {
	code, _, body := get(t, spaFS(), "/")
	if code != 200 {
		t.Fatalf("根路径应 200，实际 %d", code)
	}
	if !strings.Contains(body, "SPA首页") {
		t.Fatalf("根路径应返回 index.html，实际 %q", body)
	}
}

// SPA 路由：未命中的深链必须回落到 index.html，否则用户刷新子路由就 404。
// /apifoo 这类"看着像 api 前缀但不是 api/ 目录"的路径仍归 SPA。
func TestHandlerFallsBackToIndexForUnknownPath(t *testing.T) {
	cases := []string{"/dashboard", "/metrics/step_count", "/assets/missing.css", "/apifoo", "/..%2fsecret"}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			code, _, body := get(t, spaFS(), path)
			if code != 200 {
				t.Fatalf("%s 应回落首页并 200，实际 %d", path, code)
			}
			if !strings.Contains(body, "SPA首页") {
				t.Fatalf("%s 未回落到 index.html: %q", path, body)
			}
		})
	}
}

// webmanifest 的 MIME 必须显式覆盖：Go 的 mime.TypeByExtension(".webmanifest") 返回空串，
// 交给 FileServer 会被内容嗅探成 text/plain，浏览器于是静默拒绝安装 PWA。
func TestHandlerWebmanifestContentType(t *testing.T) {
	code, ct, body := get(t, spaFS(), "/site.webmanifest")
	if code != 200 {
		t.Fatalf("应 200，实际 %d", code)
	}
	if ct != "application/manifest+json; charset=utf-8" {
		t.Fatalf("Content-Type = %q，期望 application/manifest+json; charset=utf-8", ct)
	}
	if body != `{"name":"知常"}` {
		t.Fatalf("内容错误: %q", body)
	}
}

// /api/** 未命中绝不能回落成 200 + HTML：拼错端点的客户端会把 JSON 解析失败
// 当成"数据为空"，故障被伪装成正常返回。
func TestHandlerUnknownAPIPathReturnsJSON404(t *testing.T) {
	assets := spaFS()
	cases := []string{"/api", "/api/", "/api/nope", "/api/v1/metricss"}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			code, ct, body := get(t, assets, path)
			if code != 404 {
				t.Fatalf("%s 应 404，实际 %d body=%q", path, code, body)
			}
			if ct != "application/json; charset=utf-8" {
				t.Fatalf("%s Content-Type = %q", path, ct)
			}
			if body != `{"error":"接口不存在"}` {
				t.Fatalf("%s 应返回 JSON 错误体，实际 %q", path, body)
			}
		})
	}
}

// 拦截只针对"确实不存在的文件"：磁盘上真实存在的 api/ 文件仍原样交给 FileServer。
func TestHandlerExistingFileUnderAPINotIntercepted(t *testing.T) {
	assets := apiDirFS()
	code, _, body := get(t, assets, "/api/legacy.txt")
	if code != 200 {
		t.Fatalf("真实文件应 200，实际 %d", code)
	}
	if body != "真实存在的文件" {
		t.Fatalf("真实文件内容错误: %q", body)
	}
}

func TestHandlerNonGETUnknownAPIPathStill404(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(spaFS()).ServeHTTP(rec, httptest.NewRequest("POST", "/api/ingest", strings.NewReader("{}")))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "接口不存在") {
		t.Fatalf("未知 API 的 POST 也应 404 JSON，实际 %d %q", rec.Code, rec.Body.String())
	}
}
