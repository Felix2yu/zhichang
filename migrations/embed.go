// Package migrations 内嵌 SQL 迁移文件，保证单二进制部署时无需外部文件。
package migrations

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *.sql
var sqlFS embed.FS

// Files 返回按文件名排序的迁移（version, sql）。
func Files() ([]File, error) {
	entries, err := fs.ReadDir(sqlFS, ".")
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := sqlFS.ReadFile(e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, File{Version: e.Name(), SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// File 是一个迁移文件。
type File struct {
	Version string
	SQL     string
}
