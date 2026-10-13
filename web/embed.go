// Package web 内嵌前端（SvelteKit 静态产物），使单二进制即可提供完整站点。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:build
var buildFS embed.FS

// FS 返回前端静态文件系统的子目录视图。
func FS() (fs.FS, error) {
	return fs.Sub(buildFS, "build")
}
