package hae

import (
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// 一晚归属到哪个日历日，决定天级聚合怎么落格。真实导出里一年有 36 天同时装着
// 两个夜晚（27 号 02:23 入睡是 26 号那晚的延续，27 号 23:39 入睡是 27 号那晚），
// 按入睡时刻的日历日归属会把它们压进同一格：求和虚高一倍，取最长又丢掉一晚。
//
// 但只有「带真实入睡时刻」的记录才能这样判：HAE 的日桶 date 是当地午夜，
// 那是「哪天」不是「几点睡」，对它套用 18:00 规则会把整晚往前挪一天。
func TestNormalizeSleepAttributesToNight(t *testing.T) {
	raw := `{"data":{"metrics":[{"name":"sleep_analysis","units":"hr","data":[` +
		`{"date":"2026-08-27 00:00:00 +0800","totalSleep":5.08,"sleepStart":"2026-08-27 23:39:00 +0800","sleepEnd":"2026-08-28 08:20:00 +0800","source":"Apple Watch"},` +
		`{"date":"2026-08-27 00:00:00 +0800","totalSleep":5.28,"sleepStart":"2026-08-27 02:23:00 +0800","sleepEnd":"2026-08-27 08:09:00 +0800","source":"Apple Watch"},` +
		`{"date":"2026-08-29 00:00:00 +0800","totalSleep":6.40,"source":"健康"}` +
		`]}]}}`

	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	if len(res.Sleep) != 3 {
		t.Fatalf("应有 3 条会话，实际 %d（skipped=%d）", len(res.Sleep), res.Skipped)
	}
	want := map[string]float64{
		"2026-08-27": 5.08, // 23:39 入睡 → 27 号那晚
		"2026-08-26": 5.28, // 凌晨 02:23 入睡 → 26 号那晚，不再与上一行挤同一格
		"2026-08-29": 6.40, // 只有日桶、没有入睡时刻 → 不做本夜换算
	}
	got := map[string]float64{}
	for _, s := range res.Sleep {
		if s.TotalSleepHr == nil {
			t.Fatalf("会话 %s 没有总睡眠", s.LocalDate)
		}
		got[s.LocalDate] = *s.TotalSleepHr
	}
	for day, hr := range want {
		if got[day] != hr {
			t.Fatalf("%s 那晚应为 %v 小时，实际 %v（全部：%v）", day, hr, got[day], got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("归属后应正好 3 个不同的夜晚，实际 %v", got)
	}
}

// 上床时刻是入睡时刻的次级锚点：没有 sleepStart 时用 inBedStart 判夜晚。
func TestNormalizeSleepFallsBackToInBedStart(t *testing.T) {
	raw := `{"data":{"metrics":[{"name":"sleep_analysis","units":"hr","data":[` +
		`{"date":"2026-08-27 00:00:00 +0800","totalSleep":7.0,"inBedStart":"2026-08-27 01:10:00 +0800","source":"Apple Watch"}` +
		`]}]}}`
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	if len(res.Sleep) != 1 || res.Sleep[0].LocalDate != "2026-08-26" {
		t.Fatalf("凌晨 01:10 上床应归到 26 号那晚，实际 %+v", res.Sleep)
	}
}
