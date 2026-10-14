// Package hae 解析 Health Auto Export 的 JSON 导出（v1 / v2 通用结构）。
package hae

import (
	"bytes"
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

// SourceName 是来源名，兼容 HAE 的两种写法：
//
//	"小明的 Apple Watch"                        —— 旧版导出（字符串）
//	{"name":"小明的 Apple Watch","identifier":…} —— 新版导出（对象）
//
// 新版把 workouts[].source 从字符串改成对象，若按 string 解析，encoding/json 会抛
// UnmarshalTypeError 让**整份导出**解析失败（端点回 400，一个指标都入不了库）。
// 因此这里一律解析成纯名字：认不出的形态按空串处理，绝不让一个字段拖垮整包。
type SourceName string

// UnmarshalJSON 实现 json.Unmarshaler。
func (s *SourceName) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*s = ""
		return nil
	}
	switch trimmed[0] {
	case '"':
		var v string
		if err := json.Unmarshal(trimmed, &v); err != nil {
			*s = ""
			return nil
		}
		*s = SourceName(v)
	case '{':
		var obj struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			*s = ""
			return nil
		}
		*s = SourceName(obj.Name)
	default:
		*s = ""
	}
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
	ECG      []ECG           `json:"ecg"`
	Sleep    json.RawMessage `json:"sleep"`
}

// ECG 是一次心电测量。
//
// Classification 是**本地化文本**（简中「窦性心律」、英文 "Sinus Rhythm"），
// 不是稳定枚举——归一由 domain/ecg 负责，这里只负责把原文拿全。
type ECG struct {
	Start                       string               `json:"start"`
	End                         string               `json:"end"`
	Classification              string               `json:"classification"`
	Severity                    string               `json:"severity"`
	AverageHeartRate            Float                `json:"averageHeartRate"`
	NumberOfVoltageMeasurements Float                `json:"numberOfVoltageMeasurements"`
	SamplingFrequency           Float                `json:"samplingFrequency"`
	Source                      SourceName           `json:"source"`
	VoltageMeasurements         []VoltageMeasurement `json:"voltageMeasurements"`
}

// VoltageMeasurement 是一个电压采样点。
//
// 上游还带一个逐点的 date，这里**故意不解析**：它的形态在 HAE 各版本间变过
// （实测是 unix 秒浮点，官方文档举例却是日期字符串），而时轴完全可以由
// start_time 与 sampling_frequency 推出（第 i 点即 start + i/fs）。
// 存下来只会把两种形态一起固化，并让「时轴」有两个可能互相矛盾的事实来源。
type VoltageMeasurement struct {
	Voltage Float  `json:"voltage"`
	Units   string `json:"units"`
}

// Metric 是一类健康指标。
type Metric struct {
	Name  string  `json:"name"`
	Units string  `json:"units"`
	Data  []Point `json:"data"`
}

// Point 是一条原始记录，字段覆盖 HAE 的通用与特殊形态。
type Point struct {
	Date       string     `json:"date"`
	StartDate  string     `json:"startDate"`
	EndDate    string     `json:"endDate"`
	Source     SourceName `json:"source"`
	Device     string     `json:"device"`
	Value      string     `json:"value"`
	MealTime   string     `json:"mealTime"`
	Reason     string     `json:"reason"`
	Qty        Float      `json:"qty"`
	Min        Float      `json:"Min"`
	Avg        Float      `json:"Avg"`
	Max        Float      `json:"Max"`
	Systolic   Float      `json:"systolic"`
	Diastolic  Float      `json:"diastolic"`
	TotalSleep Float      `json:"totalSleep"`
	Asleep     Float      `json:"asleep"`
	Core       Float      `json:"core"`
	Deep       Float      `json:"deep"`
	REM        Float      `json:"REM"`
	Awake      Float      `json:"awake"`
	InBed      Float      `json:"inBed"`
	SleepStart string     `json:"sleepStart"`
	SleepEnd   string     `json:"sleepEnd"`
	InBedStart string     `json:"inBedStart"`
	InBedEnd   string     `json:"inBedEnd"`
}

// Workout 是一条运动记录（v2 结构）。
type Workout struct {
	Name        string     `json:"name"`
	Start       string     `json:"start"`
	End         string     `json:"end"`
	Duration    Float      `json:"duration"`
	Source      SourceName `json:"source"`
	Device      string     `json:"device"`
	TotalEnergy Quantity   `json:"totalEnergy"`
	// TotalDistance 是旧版的运动总距离字段；新版改叫 Distance，两者取其一。
	TotalDistance Quantity       `json:"totalDistance"`
	Distance      Quantity       `json:"distance"`
	Meta          map[string]any `json:"-"`
}

// Quantity 是带单位的数量（如总能耗、总距离）。
type Quantity struct {
	Qty   Float  `json:"qty"`
	Units string `json:"units"`
}

// UnmarshalJSON 容错解析，接受三种形态：
//
//	{"qty":316.9,"units":"kJ"}  正常对象
//	316.9                        裸数值（单位缺失，Qty 照样取到）
//	[{…},{…}]                    逐点数组等其它形态——整体忽略，不让它拖垮整包
//
// 与 Float 同样的取舍：宁可少一个字段，也不能因为上游改结构就让整份导出 400。
func (q *Quantity) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	switch trimmed[0] {
	case '{':
		var obj struct {
			Qty   Float  `json:"qty"`
			Units string `json:"units"`
		}
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return nil
		}
		q.Qty, q.Units = obj.Qty, obj.Units
	case '[', 't', 'f':
		return nil // 数组 / 布尔：不是数量形态，忽略
	default:
		var num Float
		if err := num.UnmarshalJSON(trimmed); err != nil {
			return nil
		}
		q.Qty, q.Units = num, ""
	}
	return nil
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
//
// 心电图也要算进来：它不产生 samples，但确实是数据。漏掉它的话，
// 一份「只导出了心电图」的自动化会被判成空包，回 200/empty 并整体丢弃，
// 而且日志里看不出任何异常。
func (p *Payload) IsEmpty() bool {
	return len(p.Data.Metrics) == 0 && len(p.Data.Workouts) == 0 &&
		len(p.Data.ECG) == 0 && len(p.Data.Sleep) == 0
}

// Count 返回原始记录条数（指标样本 + 运动）。
//
// 心电图与聚合睡眠都不计入：它们是事件表的数据，不产生 samples，
// 计进来会让 received 与落库条数对不上，掩盖真正的丢数。
// 心电图的接收条数单独走 IngestStats.ECG。
func (p *Payload) Count() int {
	n := 0
	for _, m := range p.Data.Metrics {
		n += len(m.Data)
	}
	return n + len(p.Data.Workouts)
}
