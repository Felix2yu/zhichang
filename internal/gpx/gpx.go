// Package gpx 解析 Health Auto Export 导出的运动轨迹文件（GPX 1.1）。
//
// 为什么走本地导入而不是接入端点：轨迹点是位置数据，比任何生理指标都更直接地
// 暴露「人在哪里」。HAE 的 REST 自动化只能发 JSON 负载，把逐点经纬度同步到
// 服务器上等于把行踪上传；而这份导出目录里的 .gpx 本来就在本机。
// 所以这里只有一条路：命令行读本机文件，直接写本地库。
package gpx

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Route 是一条轨迹：文件名给出的归属信息 + 逐点序列。
//
// 逐点不存绝对时间戳：绝对时刻 = StartTime + AtSec[i]。时间轴只有一个事实来源，
// 与心电波形的取舍一致（上游逐点时间的形态在版本间变过）。
type Route struct {
	FileName    string
	WorkoutName string    // 文件名里 "-Route-" 之前的一段，对应 workouts.name
	Wall        string    // 文件名里的本地钟点，紧凑形式 "20261014123037"
	StartTime   time.Time // 第一个点的绝对时刻（UTC）
	EndTime     time.Time // 最后一个点的绝对时刻（UTC）
	Lats        []float64
	Lons        []float64
	Eles        []float64
	AtSec       []int32 // 相对第一个点的秒数
}

// Count 返回点数。
func (r *Route) Count() int { return len(r.Lats) }

// wallLayout 是 HAE 写进文件名的时间格式（本地钟点，不带偏移）。
const wallLayout = "20060102_150405"

// ErrNoPoints 表示文件里没有任何有效轨迹点。
var ErrNoPoints = errors.New("GPX 里没有有效轨迹点")

var nameRe = regexp.MustCompile(`^(.*)-Route-(\d{8}_\d{6})\.gpx$`)

// ParseFileName 从 HAE 的轨迹文件名里取出运动名与本地起始钟点。
//
// 文件名里的钟点是本地时间且不带偏移，所以只用来与 workouts 的
// 「start_time + tz_offset_min」比对，绝不当成 UTC 用。
func ParseFileName(path string) (name, wall string, ok bool) {
	m := nameRe.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return "", "", false
	}
	t, err := time.Parse(wallLayout, m[2])
	if err != nil {
		return "", "", false
	}
	return m[1], t.Format("20060102150405"), true
}

// ParseFile 解析一个 GPX 文件。
//
// 用 token 流而不是整体 Unmarshal：单文件实测两万多个点（每点还带
// speed/course/hAcc/vAcc 四个扩展字段），全量物化只是把内存花在用不上的形态上。
// 逐点只取经纬度、高程与时间。
func ParseFile(path string) (*Route, error) {
	name, wall, ok := ParseFileName(path)
	if !ok {
		return nil, fmt.Errorf("文件名不符合 <运动名>-Route-<YYYYMMDD_HHMMSS>.gpx: %s", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := &Route{FileName: filepath.Base(path), WorkoutName: name, Wall: wall}
	dec := xml.NewDecoder(f)
	for {
		tk, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("解析 %s: %w", r.FileName, err)
		}
		se, isElem := tk.(xml.StartElement)
		if !isElem || se.Name.Local != "trkpt" {
			continue
		}
		if err := readPoint(dec, r, se); err != nil {
			return nil, fmt.Errorf("解析 %s: %w", r.FileName, err)
		}
	}
	if r.Count() == 0 {
		return nil, fmt.Errorf("%s: %w", r.FileName, ErrNoPoints)
	}
	return r, nil
}

// readPoint 读完一个 trkpt（调用方已消费起始标签，属性还在 StartElement 上）。
//
// 两条硬约定，与心电波形同一套取舍：
//   - 缺经纬度或坐标为 (0,0) 的点不入库。它会让 bbox 与折线同时错，
//     而「赤道与本初子午线交点」在真实轨迹里不会出现，留着只会被当成数据。
//   - 任一点缺时间就整条轨迹不入库。时轴是按 start_time + at_sec[i] 推的，
//     中间少一个时间，后面的点全部错位——宁可不要，也不能画出一条时间轴错的轨迹。
func readPoint(dec *xml.Decoder, r *Route, se xml.StartElement) error {
	var lat, lon float64
	var hasLat, hasLon bool
	for _, a := range se.Attr {
		switch a.Name.Local {
		case "lat":
			v, err := strconv.ParseFloat(a.Value, 64)
			if err == nil {
				lat, hasLat = v, true
			}
		case "lon":
			v, err := strconv.ParseFloat(a.Value, 64)
			if err == nil {
				lon, hasLon = v, true
			}
		}
	}

	var (
		ele    float64
		at     time.Time
		hasAt  bool
		skipMe = !hasLat || !hasLon || (lat == 0 && lon == 0)
	)
	for {
		tk, err := dec.Token()
		if err != nil {
			return err
		}
		switch v := tk.(type) {
		case xml.StartElement:
			switch v.Name.Local {
			case "ele":
				if err := dec.DecodeElement(&ele, &v); err != nil {
					return err
				}
			case "time":
				var s string
				if err := dec.DecodeElement(&s, &v); err != nil {
					return err
				}
				if t, err := time.Parse(time.RFC3339, strings.TrimSpace(s)); err == nil {
					at, hasAt = t.UTC(), true
				}
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement: // </trkpt>
			if skipMe {
				return nil
			}
			if !hasAt {
				return errors.New("有轨迹点缺少时间戳，整条轨迹不入库")
			}
			if r.Count() == 0 {
				r.StartTime = at
			}
			r.EndTime = at
			r.Lats = append(r.Lats, lat)
			r.Lons = append(r.Lons, lon)
			r.Eles = append(r.Eles, ele)
			r.AtSec = append(r.AtSec, int32(at.Sub(r.StartTime).Seconds()))
			return nil
		}
	}
}
