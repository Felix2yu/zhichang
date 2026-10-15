package gpx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFileName(t *testing.T) {
	tests := []struct {
		path     string
		wantName string
		wantWall string
		ok       bool
	}{
		{"户外 步行-Route-20261014_091230.gpx", "户外 步行", "20261014091230", true},
		{"/tmp/户外 骑行-Route-20260101_083000.gpx", "户外 骑行", "20260101083000", true},
		{"徒步-Route-20260310_190000.gpx", "徒步", "20260310190000", true},
		{"route-20261014.gpx", "", "", false},              // 没有 -Route- 段
		{"户外 步行-Route-2026-10-14.gpx", "", "", false},      // 钟点格式不对
		{"户外 步行-Route-20261345_190000.gpx", "", "", false}, // 月份非法
		{"户外 步行-Route-20261014_091230.csv", "", "", false},
	}
	for _, tt := range tests {
		name, wall, ok := ParseFileName(tt.path)
		if ok != tt.ok || name != tt.wantName || wall != tt.wantWall {
			t.Fatalf("%s: got (%q,%q,%v)，want (%q,%q,%v)", tt.path, name, wall, ok, tt.wantName, tt.wantWall, tt.ok)
		}
	}
}

// write 把内容写成 HAE 命名的 GPX 文件并返回路径。
func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "户外 步行-Route-20261014_091230.gpx")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const gpxHead = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg>`

const gpxTail = `</trkseg></trk></gpx>`

func TestParseFileReadsPointsAndRelativeTime(t *testing.T) {
	// 第三个点比前一个晚 7 秒：时轴必须是逐点相对秒数，不能按固定 1Hz 推。
	path := write(t, gpxHead+
		`<trkpt lat="30.25410123456789" lon="120.15320123456789"><ele>3.5</ele><time>2026-10-14T01:12:39Z</time><extensions><speed>0.6</speed></extensions></trkpt>`+
		`<trkpt lat="30.25409643210987" lon="120.15319918765432"><ele>4.0</ele><time>2026-10-14T01:12:40Z</time></trkpt>`+
		`<trkpt lat="30.25409312345678" lon="120.15319462345678"><ele>2.0</ele><time>2026-10-14T01:12:47Z</time></trkpt>`+
		gpxTail)

	r, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Count() != 3 || r.WorkoutName != "户外 步行" || r.Wall != "20261014091230" {
		t.Fatalf("点数或文件名归属错误: %+v", r)
	}
	if want := "2026-10-14T01:12:39Z"; !r.StartTime.Equal(mustTime(want)) {
		t.Fatalf("起点应为第一个点的时间，实际 %s", r.StartTime)
	}
	if want := "2026-10-14T01:12:47Z"; !r.EndTime.Equal(mustTime(want)) {
		t.Fatalf("终点应为最后一个点的时间，实际 %s", r.EndTime)
	}
	for i, want := range []int32{0, 1, 8} {
		if r.AtSec[i] != want {
			t.Fatalf("第 %d 点的相对秒数应为 %d，实际 %d（按索引猜时轴会把后面的点全部挪错）", i, want, r.AtSec[i])
		}
	}
	if r.Lats[0] != 30.25410123456789 || r.Lons[2] != 120.15319462345678 {
		t.Fatalf("经纬度解析错误: %v %v", r.Lats, r.Lons)
	}
	if r.Eles[1] != 4.0 {
		t.Fatalf("高程解析错误: %v", r.Eles)
	}
}

// (0,0) 不是「赤道与本初子午线的交点」，是上游没给坐标。
// 留着会把 bbox 拉到半个地球外，折线也画错，所以这个点不入库。
func TestParseFileDropsZeroCoordinatePoints(t *testing.T) {
	path := write(t, gpxHead+
		`<trkpt lat="0" lon="0"><ele>0</ele><time>2026-10-14T01:12:39Z</time></trkpt>`+
		`<trkpt lat="30.25" lon="120.15"><ele>1</ele><time>2026-10-14T01:12:40Z</time></trkpt>`+
		gpxTail)
	r, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Count() != 1 || r.Lats[0] != 30.25 {
		t.Fatalf("应只剩有效的那一个点，实际 %+v", r.Lats)
	}
	if !r.StartTime.Equal(mustTime("2026-10-14T01:12:40Z")) {
		t.Fatalf("被丢弃的点不该参与时轴原点，实际 %s", r.StartTime)
	}
}

// 任一点缺时间 → 整条轨迹不入库。时轴按 start + at_sec[i] 推，
// 中间少一个时间会让后面所有点错位，画出来是一条时间轴错的轨迹。
func TestParseFileRejectsRouteWithMissingTime(t *testing.T) {
	path := write(t, gpxHead+
		`<trkpt lat="30.25" lon="120.15"><ele>1</ele><time>2026-10-14T01:12:39Z</time></trkpt>`+
		`<trkpt lat="30.26" lon="120.16"><ele>1</ele></trkpt>`+
		gpxTail)
	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "时间戳") {
		t.Fatalf("缺点时间应整条拒绝，实际 %v", err)
	}
}

func TestParseFileRejectsEmptyAndBadName(t *testing.T) {
	if _, err := ParseFile(write(t, gpxHead+gpxTail)); !errors.Is(err, ErrNoPoints) {
		t.Fatalf("空轨迹应报 ErrNoPoints，实际 %v", err)
	}
	p := filepath.Join(t.TempDir(), "随手拍.gpx")
	if err := os.WriteFile(p, []byte(gpxHead+gpxTail), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(p); err == nil {
		t.Fatal("文件名不合规应报错：靠钟点归属运动，名字对不上就是无从归属")
	}
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}
