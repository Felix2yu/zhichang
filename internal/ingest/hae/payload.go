// Package hae 解析 Health Auto Export 的 JSON 导出（v1 / v2 通用结构）。
package hae

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Float 兼容数字与字符串两种数值形态，非法值视为缺失。
type Float struct {
	Value float64
	Set   bool
}

// UnmarshalJSON 实现 json.Unmarshaler。
func (f *Float) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(strings.Trim(string(b), `"`))
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // 非数值字段（如状态文本）忽略
	}
	f.Value, f.Set = v, true
	return nil
}

// Payload 是 HAE 导出 JSON 的顶层结构。
type Payload struct {
	Data Data `json:"data"`
}

// Data 包含各类健康数据。
type Data struct {
	Metrics  []Metric        `json:"metrics"`
	Workouts []Workout       `json:"workouts"`
	Sleep    json.RawMessage `json:"sleep"`
}

// Metric 是一类健康指标。
type Metric struct {
	Name  string  `json:"name"`
	Units string  `json:"units"`
	Data  []Point `json:"data"`
}

// Point 是一条原始记录，字段覆盖 HAE 的通用与特殊形态。
type Point struct {
	Date       string `json:"date"`
	StartDate  string `json:"startDate"`
	EndDate    string `json:"endDate"`
	Source     string `json:"source"`
	Device     string `json:"device"`
	Value      string `json:"value"`
	MealTime   string `json:"mealTime"`
	Reason     string `json:"reason"`
	Qty        Float  `json:"qty"`
	Min        Float  `json:"Min"`
	Avg        Float  `json:"Avg"`
	Max        Float  `json:"Max"`
	Systolic   Float  `json:"systolic"`
	Diastolic  Float  `json:"diastolic"`
	TotalSleep Float  `json:"totalSleep"`
	Asleep     Float  `json:"asleep"`
	Core       Float  `json:"core"`
	Deep       Float  `json:"deep"`
	REM        Float  `json:"REM"`
	Awake      Float  `json:"awake"`
	InBed      Float  `json:"inBed"`
	SleepStart string `json:"sleepStart"`
	SleepEnd   string `json:"sleepEnd"`
	InBedStart string `json:"inBedStart"`
	InBedEnd   string `json:"inBedEnd"`
}

// Workout 是一条运动记录（v2 结构）。
type Workout struct {
	Name          string         `json:"name"`
	Start         string         `json:"start"`
	End           string         `json:"end"`
	Duration      Float          `json:"duration"`
	Source        string         `json:"source"`
	Device        string         `json:"device"`
	TotalEnergy   Quantity       `json:"totalEnergy"`
	TotalDistance Quantity       `json:"totalDistance"`
	Meta          map[string]any `json:"-"`
}

// Quantity 是带单位的数量（如总能耗、总距离）。
type Quantity struct {
	Qty   Float  `json:"qty"`
	Units string `json:"units"`
}

// ParsePayload 解析导出 JSON。
func ParsePayload(r io.Reader) (*Payload, error) {
	dec := json.NewDecoder(r)
	var p Payload
	if err := dec.Decode(&p); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("请求体为空")
		}
		return nil, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	return &p, nil
}

// IsEmpty 判断导出是否没有任何数据。
func (p *Payload) IsEmpty() bool {
	return len(p.Data.Metrics) == 0 && len(p.Data.Workouts) == 0 && len(p.Data.Sleep) == 0
}

// Count 返回原始记录条数（指标样本 + 运动）。
func (p *Payload) Count() int {
	n := 0
	for _, m := range p.Data.Metrics {
		n += len(m.Data)
	}
	return n + len(p.Data.Workouts)
}
