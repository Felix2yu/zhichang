// Package sample 定义归一化后的健康样本模型。
package sample

import (
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/ecg"
	"github.com/Felix2yu/zhichang/internal/domain/tz"
)

// Sample 是一条归一化样本。start_time / end_time 为 UTC，local_date 由 payload 自带偏移推导。
type Sample struct {
	Metric      string
	Source      string
	Device      string
	StartTime   time.Time
	EndTime     time.Time
	Value       float64
	ValueMin    *float64
	ValueMax    *float64
	Unit        string
	LocalDate   string
	TZOffsetMin int
	Meta        map[string]any
}

// Normalize 保证 end_time 不早于 start_time，并补齐 local_date。
func (s *Sample) Normalize(home *time.Location) {
	if s.EndTime.IsZero() || s.EndTime.Before(s.StartTime) {
		s.EndTime = s.StartTime
	}
	if s.LocalDate == "" {
		s.LocalDate = tz.LocalDate(s.StartTime, s.TZOffsetMin, home)
	}
}

// SetTime 从 HAE 时间字符串设置起止时间，并推导 local_date。
func (s *Sample) SetTime(start, end string, home *time.Location) bool {
	st, off, ok := tz.ParseWithFallback(start, home)
	if !ok {
		return false
	}
	s.StartTime = st
	s.TZOffsetMin = tz.OffsetOrDefault(off, st, home)
	if end != "" {
		if et, _, ok := tz.ParseWithFallback(end, home); ok {
			s.EndTime = et
		}
	}
	s.Normalize(home)
	return true
}

// WorkoutPoint 是运动过程中的一点采样：相对运动起点的秒数与取值。
//
// 存相对秒数而不是绝对时刻，与心电波形同一取舍：时轴只留一个事实来源
// （绝对时刻 = 运动的 StartTime + AtSec）。
type WorkoutPoint struct {
	AtSec int32
	Value float64
}

// Workout 是一条归一化运动记录。
type Workout struct {
	Name          string
	Source        string
	StartTime     time.Time
	EndTime       time.Time
	DurationMin   float64
	TotalEnergy   *float64
	EnergyUnit    string
	ActiveEnergy  *float64
	ActiveUnit    string
	TotalDistance *float64
	DistanceUnit  string
	AvgHR         *float64
	MaxHR         *float64
	EffortScore   *float64
	// HR / Recovery 是按时间升序的逐点心率与运动后恢复心率；可能为空。
	HR       []WorkoutPoint
	Recovery []WorkoutPoint
	// Splits 是上游分段原文（公里/英里两套并存、字段名换过），只用于核对。
	Splits []byte
	// Route 是导出内嵌的 GPS 轨迹；nil 表示这条运动没有可用轨迹
	// （室内运动本来就没有，或上游形态不可用）。
	Route *WorkoutRoute
	// Series 是运动内部按分钟摊开的量（步数、能量、距离），按指标字典的规范名与
	// 字典单位存。归属是「这一次运动里」，不是 (local_date, metric)——后者已有日内聚合。
	Series      []WorkoutSeries
	LocalDate   string
	TZOffsetMin int
	Meta        map[string]any
}

// WorkoutSeries 是一条运动内某个量的逐分钟序列。
type WorkoutSeries struct {
	Metric string
	Unit   string
	Points []WorkoutPoint
}

// WorkoutRoutePoint 是轨迹上的一个点：相对第一个点的秒数与坐标。
//
// 不存逐点绝对时刻：时轴 = StartTime + AtSec[i]，与 GPX 导入侧共用一个事实来源。
type WorkoutRoutePoint struct {
	AtSec int32
	Lat   float64
	Lon   float64
	Ele   float64
}

// WorkoutRoute 是一条运动的轨迹。
type WorkoutRoute struct {
	StartTime time.Time // 第一个点的绝对时刻（UTC）
	EndTime   time.Time // 最后一个点的绝对时刻（UTC）
	Points    []WorkoutRoutePoint
}

// SleepSession 是一段归一化睡眠。
type SleepSession struct {
	LocalDate    string
	TZOffsetMin  int
	Source       string
	SleepStart   *time.Time
	SleepEnd     *time.Time
	InBedStart   *time.Time
	InBedEnd     *time.Time
	TotalSleepHr *float64
	AsleepHr     *float64
	CoreHr       *float64
	DeepHr       *float64
	RemHr        *float64
	AwakeHr      *float64
	InBedHr      *float64
}

// ECGRecord 是一次归一化心电测量。
//
// Classification 与 ClassificationCode 并存：前者是上游原文（随手机语言本地化），
// 后者是归一后的稳定码。原文不能省——码认不出时界面要回落展示它；
// 码也不能省——按本地化文本做统计，换一次手机语言历史数据就与之后的分裂了。
type ECGRecord struct {
	Source             string
	StartTime          time.Time
	EndTime            time.Time
	LocalDate          string
	TZOffsetMin        int
	Classification     string
	ClassificationCode ecg.Code
	AverageHeartRate   *float64
	SamplingFrequency  *int
	// VoltageCount 恒等于 len(Voltages)——上游声明的 numberOfVoltageMeasurements 不参与落库。
	// 所以它是 0 有两种来路：上游本就没给波形，或某点缺电压把整段判掉了（见 collectVoltages）。
	VoltageCount int
	VoltageUnit  string
	// Voltages 按时间升序；第 i 点对应 StartTime + i/SamplingFrequency。
	Voltages []float64
}
