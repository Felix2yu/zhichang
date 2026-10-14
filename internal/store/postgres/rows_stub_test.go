package postgres

import (
	"errors"
	"testing"
)

// rows_stub_test.go 用假的游标把「扫行失败 / 游标中途出错」这两条真实库很难造出来的
// 分支打出来。它们在生产里对应连接被掐断、结果集被截断——
// 必须确认这时函数是「报错」，而不是「把半截数据当成完整结果返回」。

type stubRows struct {
	n         int   // Next 返回 true 的次数
	scanErrAt int   // 第几次 Scan 返回错误（0 = 都不报错）
	err       error // Err() 的返回值
	calls     int
}

func (s *stubRows) Next() bool { return s.calls < s.n }

func (s *stubRows) Scan(dest ...any) error {
	s.calls++
	if s.scanErrAt != 0 && s.calls == s.scanErrAt {
		return errors.New("扫描失败")
	}
	return nil
}

func (s *stubRows) Err() error { return s.err }

func (s *stubRows) Close() {}

// TestCollectDailyScanErrorAborts 锁定 collectDaily：任何一行扫失败就整体失败并回 nil，
// 绝不能把已经扫到的半截序列返回给前端画趋势图。
func TestCollectDailyScanErrorAborts(t *testing.T) {
	out, err := collectDaily(&stubRows{n: 2, scanErrAt: 2})
	if err == nil {
		t.Fatal("第二行扫描失败时必须报错")
	}
	if out != nil {
		t.Fatalf("报错时必须回 nil，实际 %#v", out)
	}

	// 行都扫成功、但游标本身出错：错误原样抛出，由调用方按 err 丢弃结果
	st := &stubRows{n: 1, err: errors.New("connection closed")}
	out2, err2 := collectDaily(st)
	if err2 == nil || err2.Error() != "connection closed" {
		t.Fatalf("游标错误必须原样抛出: %v", err2)
	}
	if len(out2) != 1 {
		t.Fatalf("已扫到的行仍会带出（由调用方按 err 决定丢弃），实际 %d", len(out2))
	}
}

// TestScanRawSegsErrorAborts 锁定分期分段的扫描：同样是一行失败就整体失败。
func TestScanRawSegsErrorAborts(t *testing.T) {
	if _, err := scanRawSegs(&stubRows{n: 1, scanErrAt: 1}); err == nil {
		t.Fatal("分段扫描失败必须报错")
	}
	segs, err := scanRawSegs(&stubRows{n: 2, err: errors.New("中断")})
	if err == nil || err.Error() != "中断" {
		t.Fatalf("游标中断必须报错: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("期望带出 2 条已扫到的分段，实际 %d", len(segs))
	}
}

// TestFilterNightMetrics 锁定夜间生理指标白名单的过滤口径：
//   - 空请求 = 整个白名单，且给的是副本（调用方改动不能污染全局白名单）；
//   - 白名单外的指标静默丢弃，不报错也不参与查询；
//   - 保留下来的项按请求顺序输出，重复请求也照原样带（SQL 里 ANY 数组重复不影响匹配）。
func TestFilterNightMetrics(t *testing.T) {
	got := filterNightMetrics(nil)
	if len(got) != len(NightPhysiologyMetrics) {
		t.Fatalf("空请求应给整个白名单，实际 %v", got)
	}
	got[0] = "被改了"
	if NightPhysiologyMetrics[0] == "被改了" {
		t.Fatal("返回的是白名单本身，调用方一改就污染了全局口径")
	}

	cases := []struct {
		name string
		in   []string
		want int
	}{
		{"全部命中", []string{"heart_rate", "oxygen_saturation"}, 2},
		{"部分命中", []string{"heart_rate", "step_count"}, 1},
		{"全在白名单外", []string{"step_count", "distance"}, 0},
		{"重复请求原样带上", []string{"heart_rate", "heart_rate"}, 2}, // 不额外去重：SQL 里 ANY 数组重复不影响匹配结果
		{"顺序无关", []string{"respiratory_rate", "heart_rate_variability"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if out := filterNightMetrics(c.in); len(out) != c.want {
				t.Fatalf("期望 %d 个指标，实际 %v", c.want, out)
			}
		})
	}
}

// TestDerefHelpers 锁定 NULL 回落：pgx 把 SQL NULL 扫成指针 nil，
// 出参要落成 0（JSON 里是数字而不是 null），前端才不用逐处判空。
func TestDerefHelpers(t *testing.T) {
	if deref(nil) != 0 || deref2(nil, 7) != 7 {
		t.Fatal("NULL 必须回落到 0 / 给定值")
	}
	v := 3.5
	if deref(&v) != 3.5 || deref2(&v, 7) != 3.5 {
		t.Fatal("有值时必须原样取值，不能丢失")
	}
}
