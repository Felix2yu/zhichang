// Package hae 解析 Health Auto Export 的 JSON 导出（v1 / v2 通用结构）。
package hae

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
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
//
// 解析出的名字统一过 sample.CleanSource：上游用的是 NBSP 而不是普通空格
// （实测「小明的Apple\u00a0Watch」），不清洗的话同一台设备会分裂成两个来源，
// 去重唯一索引也会把同一份数据放行两遍。这里是所有来源的**唯一出口**——
// 指标点、运动、睡眠、心电的 source 都从这里来，所以清洗只做一次就全覆盖。
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
		*s = SourceName(sample.CleanSource(v))
	case '{':
		var obj struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			*s = ""
			return nil
		}
		*s = SourceName(sample.CleanSource(obj.Name))
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

	// Subtypes 收集「不认识的数字键」。类别型指标按子类型分键导出时没有 qty，
	// 事件数写在诸如 "Unspecified":1 这样的键里（实测性生活的日汇总就是这样），
	// 只按已知字段取值会把整条记录判成无值并静默丢弃。
	Subtypes map[string]float64 `json:"-"`
}

// pointKnownKeys 是 Point 自己认识的 JSON 键，由结构体标签反射得出。
// 手写这份清单迟早会和字段漂移，而漂移的后果是某个已知字段被同时
// 当成子类型再算一遍——数值翻倍且不报错。
var pointKnownKeys = func() map[string]bool {
	m := map[string]bool{"subtypes": true}
	for i := 0; i < reflect.TypeOf(Point{}).NumField(); i++ {
		tag := reflect.TypeOf(Point{}).Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		m[strings.Split(tag, ",")[0]] = true
	}
	return m
}()

// UnmarshalJSON 在常规解析之外，把未知的数字键收进 Subtypes。
// 收不进来的形态（未知键是字符串/对象）一律忽略，绝不因此报错。
func (p *Point) UnmarshalJSON(b []byte) error {
	type alias Point // 断开递归
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*p = Point(a)

	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return nil // 不是对象：没有子类型可收
	}
	for k, raw := range all {
		if pointKnownKeys[k] {
			continue
		}
		var f float64
		if err := json.Unmarshal(raw, &f); err == nil {
			if p.Subtypes == nil {
				p.Subtypes = map[string]float64{}
			}
			p.Subtypes[k] = f
		}
	}
	return nil
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

	// —— 运动详情字段。缺失是常态（不同机型与版本给的范围差别很大），
	// 且全部走容错类型：任何一个换成别的形态都不许让整份导出解析失败。
	ActiveEnergy Quantity `json:"activeEnergyBurned"`
	// BasalEnergy 新版是逐分钟数组、旧版是聚合对象，两种都要收（见 Totals）。
	BasalEnergy   Totals   `json:"basalEnergy"`
	AvgHeartRate  Quantity `json:"avgHeartRate"`
	MaxHeartRate  Quantity `json:"maxHeartRate"`
	EffortScore   Quantity `json:"effortScore"`
	Flights       Quantity `json:"flightsClimbed"`
	ElevationUp   Quantity `json:"elevationUp"`
	ElevationDown Quantity `json:"elevationDown"`
	Temperature   Quantity `json:"temperature"`
	Humidity      Quantity `json:"humidity"`
	AvgSpeed      Quantity `json:"avgSpeed"`
	MaxSpeed      Quantity `json:"maxSpeed"`
	Indoor        LaxBool  `json:"isIndoor"`
	Place         Text     `json:"location"`
	HeartRateData Series   `json:"heartRateData"`
	Recovery      Series   `json:"heartRateRecovery"`
	// Route 是导出内嵌的 GPS 轨迹。HAE 的自动化只能发 JSON 负载，而这份 JSON 里
	// 本来就带着逐点经纬度（实测一年 109 条运动、30 万余个点）——
	// 不解析它并不会让位置数据少上传一分，只是把它丢掉。
	Route RouteSeries `json:"route"`
	// Splits 原样留着：分段结构随上游版本变（公里与英里两套并存、字段名换过），
	// 解析成列会把形态固化，而它只用于核对，不参与聚合。
	Splits json.RawMessage `json:"splits"`
}

// LaxBool 兼容 true/false、0/1 与 "yes"/"no" 文本；认不出按未提供处理。
//
// 与 Float/Quantity 同一取舍：上游把某个字段换个形态时，代价必须是「少一个字段」，
// 不能是「整份导出 400、一个指标都入不了库」。
type LaxBool struct {
	Value bool
	Set   bool
}

// UnmarshalJSON 实现 json.Unmarshaler。
func (b *LaxBool) UnmarshalJSON(p []byte) error {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(string(p)), `"`)) {
	case "true", "1", "yes", "y":
		b.Value, b.Set = true, true
	case "false", "0", "no", "n":
		b.Value, b.Set = false, true
	}
	return nil // 其余形态（null、对象、乱串）一律按未提供，不报错
}

// Text 取任意形态里的可读文本：字符串原样，裸数字转字符串，对象/数组按未提供。
// 用于 location 这类「上游给的是人话标签」的字段。
type Text string

// UnmarshalJSON 实现 json.Unmarshaler。
func (t *Text) UnmarshalJSON(p []byte) error {
	trimmed := bytes.TrimSpace(p)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	switch trimmed[0] {
	case '"':
		var v string
		if err := json.Unmarshal(trimmed, &v); err == nil {
			*t = Text(v)
		}
	case '[', '{', 't', 'f':
		// 数组 / 对象 / 布尔：不是文本形态，忽略
	default:
		*t = Text(string(trimmed))
	}
	return nil
}

// SeriesPoint 是运动过程中的一点采样（逐点心率、恢复心率）。
//
// 只取时间与 Avg：实测上游把每点的 Min/Max 都写成与 Avg 相同的值，
// 存三份一样的数既浪费，又会让人误以为那是三个不同的量。
type SeriesPoint struct {
	Date  string `json:"date"`
	Avg   Float  `json:"Avg"`
	Units string `json:"units"`
}

// Series 是逐点采样序列。
//
// 上游偶尔把整段写成对象或单个数字，数组里的元素也可能换成不认识的形态，
// 所以逐点独立解码：认不出的点丢掉，其余照常入库。
// 这里绝不能返回错误——一个数组的形状变化会让整份导出 400、一个指标都入不了库。
type Series []SeriesPoint

// UnmarshalJSON 实现 json.Unmarshaler。非数组形态按「没有这条序列」处理。
func (s *Series) UnmarshalJSON(p []byte) error {
	trimmed := bytes.TrimSpace(p)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil
	}
	out := make([]SeriesPoint, 0, len(raw))
	for _, one := range raw {
		var pt SeriesPoint
		if err := json.Unmarshal(one, &pt); err == nil {
			out = append(out, pt)
		}
	}
	*s = out
	return nil
}

// RoutePoint 是导出内嵌轨迹（workouts[].route）的一个点。
//
// 只收画折线用得上的四项：上游还带 course/speed/*Accuracy，那是传感器航向、瞬时速度
// 与定位置信度，界面上没有消费方。解析成列会把形态固化（与 splits 原样留着的同一取舍），
// 而这四项之外的值留在导出文件里，需要时随时可以再收。
type RoutePoint struct {
	Latitude  Float `json:"latitude"`
	Longitude Float `json:"longitude"`
	Altitude  Float `json:"altitude"`
	Timestamp Text  `json:"timestamp"`
}

// RouteSeries 是轨迹点数组。
//
// 与 Series 同一取舍：逐点独立解码，认不出的点丢掉。这里绝不能返回错误——
// 上游把 route 写成对象或空值时，代价必须是「少一条轨迹」，
// 而不是「整份导出 400、一个指标都入不了库」。
type RouteSeries []RoutePoint

// UnmarshalJSON 实现 json.Unmarshaler。非数组形态按「没有轨迹」处理。
func (rs *RouteSeries) UnmarshalJSON(p []byte) error {
	trimmed := bytes.TrimSpace(p)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil
	}
	out := make([]RoutePoint, 0, len(raw))
	for _, one := range raw {
		var pt RoutePoint
		if err := json.Unmarshal(one, &pt); err == nil {
			out = append(out, pt)
		}
	}
	*rs = out
	return nil
}

// Quantity 是带单位的数量（如总能耗、总距离）。
type Quantity struct {
	Qty   Float  `json:"qty"`
	Units string `json:"units"`
}

// TotalPoint 是累计量的一个逐点采样（新版导出把运动内的量按分钟摊开）。
type TotalPoint struct {
	Date  string `json:"date"`
	Qty   Float  `json:"qty"`
	Units string `json:"units"`
}

// Totals 是「上游可能给聚合对象、也可能给逐点数组」的累计量。
//
// 为什么需要一个专门的类型而不是让 Quantity 也认数组：Quantity 挂在平均心率、
// 速度这类速率字段上，那些字段换成数组形态说明上游改了口径，求和会把「bpm」
// 加成一个没有意义的数。只有本来就是累计量的字段才允许按数组求和，
// 所以这条能力显式挂在需要的字段上，其余字段维持「数组=不认识=忽略」。
//
// 数组形态下 Qty 取逐点之和（累计量的日内口径就是求和），Units 取第一个带单位的点；
// 逐点原样留在 Points 里，供序列表落库。
type Totals struct {
	Qty    Float
	Units  string
	Points []TotalPoint
}

// UnmarshalJSON 实现 json.Unmarshaler。认不出的形态一律忽略，绝不让整份导出失败。
func (t *Totals) UnmarshalJSON(b []byte) error {
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
		t.Qty, t.Units = obj.Qty, obj.Units
	case '[':
		var pts []TotalPoint
		if err := json.Unmarshal(trimmed, &pts); err != nil {
			return nil
		}
		var sum float64
		for _, p := range pts {
			if !p.Qty.Set {
				continue
			}
			sum += p.Qty.Value
			if t.Units == "" {
				t.Units = p.Units
			}
			t.Points = append(t.Points, p)
		}
		if len(t.Points) > 0 {
			t.Qty = Float{Value: sum, Set: true}
		}
	case 't', 'f':
		return nil // 布尔：不是数量形态，忽略
	default:
		var num Float
		if err := num.UnmarshalJSON(trimmed); err != nil {
			return nil
		}
		t.Qty = num
	}
	return nil
}

// Quantity 收成聚合形态，供只关心总量的调用方使用。
func (t Totals) Quantity() Quantity {
	return Quantity{Qty: t.Qty, Units: t.Units}
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
