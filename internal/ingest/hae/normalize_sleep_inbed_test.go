package hae

import (
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// 上游的卧床时长字段在真实导出里恒为 0（288/288 条），而 inBedStart/inBedEnd 每条都有值。
// 0 原样落库会让界面把「卧床 0 小时」画成一次真实观测——一条有起止时刻的会话不可能是
// 0 小时，这是字段没填而不是测量结果。所以按起止差值补算，补不出来才留空。
func TestNormalizeSleepDerivesInBedDurationFromTimes(t *testing.T) {
	cases := []struct {
		name string
		pt   string
		want float64
	}{
		{"上游给 0，按起止补算",
			`{"date":"2026-05-03 00:00:00 +0800","totalSleep":7.0,"inBed":0,` +
				`"inBedStart":"2026-05-02 23:10:00 +0800","inBedEnd":"2026-05-03 07:40:00 +0800"`, 8.5},
		{"上游没给这个字段，同样补算",
			`{"date":"2026-05-03 00:00:00 +0800","totalSleep":7.0,` +
				`"inBedStart":"2026-05-02 23:00:00 +0800","inBedEnd":"2026-05-03 07:00:00 +0800"`, 8.0},
		{"上游给的是有效值，不重算覆盖",
			`{"date":"2026-05-03 00:00:00 +0800","totalSleep":7.0,"inBed":8.25,` +
				`"inBedStart":"2026-05-02 23:00:00 +0800","inBedEnd":"2026-05-03 07:00:00 +0800"`, 8.25},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := `{"data":{"metrics":[{"name":"sleep_analysis","units":"hr","data":[` + c.pt + `,"source":"示例的Apple Watch"}]}]}}`
			res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
			if len(res.Sleep) != 1 {
				t.Fatalf("应有 1 条会话，实际 %d（skipped=%d）", len(res.Sleep), res.Skipped)
			}
			got := res.Sleep[0].InBedHr
			if got == nil {
				t.Fatalf("卧床时长应补算出 %v，实际为 NULL", c.want)
			}
			almost(t, "卧床时长", *got, c.want, 1e-6)
		})
	}
}

// 补算只在起止时刻本身讲得通的时候做：终点了早于起点、或一天跨度超过 24 小时，
// 说明这一行的时刻不可信，此时留 NULL 比写一个编出来的数好（宁缺勿造）。
func TestNormalizeSleepKeepsInBedNullWhenTimesAreNonsense(t *testing.T) {
	raw := `{"data":{"metrics":[{"name":"sleep_analysis","units":"hr","data":[
		{"date":"2026-05-03 00:00:00 +0800","totalSleep":7.0,"inBed":0,
		 "inBedStart":"2026-05-03 07:00:00 +0800","inBedEnd":"2026-05-02 23:00:00 +0800","source":"示例的Apple Watch"},
		{"date":"2026-05-04 00:00:00 +0800","totalSleep":7.0,"inBed":0,
		 "inBedStart":"2026-05-03 06:00:00 +0800","inBedEnd":"2026-05-04 08:00:00 +0800","source":"示例的Apple Watch"}]}]}}`
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	if len(res.Sleep) != 2 {
		t.Fatalf("应有 2 条会话，实际 %d", len(res.Sleep))
	}
	for i, s := range res.Sleep {
		if s.InBedHr != nil && *s.InBedHr != 0 {
			t.Fatalf("第 %d 条的时刻讲不通，不该补算出 %v", i+1, *s.InBedHr)
		}
	}
}

// 没有起止时刻时，上游的 0 就照原样留着：那不是「补不出来」，而是上游确实这么记的，
// 界面把它显示成 0 小时属于事实转述，改它才是造假。
func TestNormalizeSleepKeepsUpstreamZeroWithoutTimes(t *testing.T) {
	raw := `{"data":{"metrics":[{"name":"sleep_analysis","units":"hr","data":[` +
		`{"date":"2026-05-03 00:00:00 +0800","totalSleep":7.0,"inBed":0,"source":"示例的Apple Watch"}]}]}}`
	res := mustNormalize(t, metric.New(metric.Defaults()), time.UTC, raw)
	if len(res.Sleep) != 1 || res.Sleep[0].InBedHr == nil || *res.Sleep[0].InBedHr != 0 {
		t.Fatalf("无起止时刻时应原样保留上游的 0，实际 %+v", res.Sleep)
	}
}
