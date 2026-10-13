// Package sample 定义归一化后的健康样本模型。
package sample

import (
	"time"

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

// Workout 是一条归一化运动记录。
type Workout struct {
	Name          string
	Source        string
	StartTime     time.Time
	EndTime       time.Time
	DurationMin   float64
	TotalEnergy   *float64
	EnergyUnit    string
	TotalDistance *float64
	DistanceUnit  string
	LocalDate     string
	TZOffsetMin   int
	Meta          map[string]any
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
