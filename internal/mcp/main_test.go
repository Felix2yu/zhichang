package mcp

import (
	"context"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/config"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
	"github.com/Felix2yu/zhichang/internal/testdb"
)

// TestMain 起停本包的测试库；ZHICHANG_TEST_DSN 存在时直接用它。
func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

// testLoc 是被测代码的「本地时区」。MCP 的所有默认区间都按这个时区的今天算，
// 测试里的期望日期必须用同一把尺子推导，否则换台机器就红。
var testLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	return loc
}()

const testTZName = "Asia/Shanghai"

var (
	dbOnce    sync.Once
	shared    *postgres.DB
	sharedErr error
)

// dsnOnTest 必须在测试 goroutine 上调用：testdb.DSN 库不可用时会 t.Skip，
// 而 Skip 发生在 sync.Once 里会让 Once 提前标记完成、把 nil 库留给后面的测试。
func dsnOnTest(t *testing.T) string {
	t.Helper()
	return testdb.DSN(t)
}

func sharedDB(t *testing.T) *postgres.DB {
	t.Helper()
	dsn := dsnOnTest(t)
	dbOnce.Do(func() {
		ctx := context.Background()
		shared, sharedErr = postgres.Open(ctx, config.Config{DBDSN: dsn})
		if sharedErr != nil {
			return
		}
		_, sharedErr = shared.Migrate(ctx)
	})
	if sharedErr != nil {
		t.Skipf("数据库不可用: %v", sharedErr)
	}
	return shared
}

// newDeps 返回一个指向干净库、用内置口径字典的 Deps。
// 每个测试自己 TRUNCATE + 播种，所以互不依赖执行顺序。
func newDeps(t *testing.T) *Deps {
	t.Helper()
	db := sharedDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `TRUNCATE samples, workout_series, workout_routes, workouts, sleep_sessions, ecg_records, ecg_voltages, ingest_runs,
		sync_watermark, sources, daily_metrics, daily_metrics_day, rollup_queue`); err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	return DepsFrom(db, metric.New(metric.Defaults()), testLoc, testTZName)
}

// day 与 Deps.today/shift 同源：断言里的边界日期必须跟着被测代码的「今天」走。
func day(offset int) string {
	return time.Now().In(testLoc).AddDate(0, 0, offset).Format("2006-01-02")
}

func at(date string, hour, min int) time.Time {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hour, min, 0, 0, testLoc)
}

// atMinute 把「本夜轴分钟」（睡眠日本地 00:00 起算，跨零点顺延）还原成本地时刻。
func atMinute(date string, min int) time.Time {
	return at(date, 0, 0).Add(time.Duration(min) * time.Minute)
}

func nextDay(date string) string {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return d.AddDate(0, 0, 1).Format("2006-01-02")
}

func f64(v float64) *float64 { return &v }

func ptrAt(v time.Time) *time.Time { return &v }

// sleepNight 是一晚标准样本：本地 23:30 入睡、次晨 07:00 起床、总睡眠 7.5h。
func sleepNight(date string) *sample.SleepSession {
	return &sample.SleepSession{
		LocalDate: date, Source: "Apple Watch", TZOffsetMin: 480,
		SleepStart: ptrAt(at(date, 23, 30)), SleepEnd: ptrAt(at(nextDay(date), 7, 0)),
		TotalSleepHr: f64(7.5), DeepHr: f64(1.5), CoreHr: f64(4.0), RemHr: f64(2.0), InBedHr: f64(8.0),
	}
}

// workout 是一条带消耗与距离的运动记录（时间固定在本地 07:00 起）。
func workout(date, name string, durationMin, energy, distance float64) sample.Workout {
	return sample.Workout{
		Name: name, Source: "Apple Watch", LocalDate: date, TZOffsetMin: 480,
		StartTime: at(date, 7, 0), EndTime: at(date, 7, 32), DurationMin: durationMin,
		TotalEnergy: f64(energy), EnergyUnit: "kcal", TotalDistance: f64(distance), DistanceUnit: "km",
	}
}

// rowsFor 在「今天偏移 from..to」的连续日期上各造一条单值样本，
// 用于钉住默认区间的两端（区间外的日期必须进不了日均）。
func rowsFor(met string, from, to int, v float64) []row {
	out := make([]row, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, daily(met, day(i), v))
	}
	return out
}

// seg 是一条睡眠分期样本：startMin 是本夜轴分钟（跨零点顺延），durMin 是时长。
// value 存小时数——仓储层的 WASO 就是按入睡窗口内清醒分段的 value 求和的。
func seg(date string, startMin int, durMin float64, stage, source string) row {
	return row{
		metric: "sleep_analysis", source: source, at: atMinute(date, startMin),
		value: durMin / 60, durMin: durMin, unit: "hr",
		meta: map[string]any{"stage": stage},
	}
}

func putSleep(t *testing.T, d *Deps, list ...sample.SleepSession) {
	t.Helper()
	runSeq++
	if _, err := d.DB.WriteSleep(context.Background(), runSeq, list); err != nil {
		t.Fatalf("写入睡眠失败: %v", err)
	}
}

func putWorkouts(t *testing.T, d *Deps, list ...sample.Workout) {
	t.Helper()
	runSeq++
	if _, err := d.DB.WriteWorkouts(context.Background(), runSeq, list); err != nil {
		t.Fatalf("写入运动失败: %v", err)
	}
}

// row 是一条待落库的原始样本。at 用本地时刻表达，偏移与 local_date 由它推导，
// 这样测试不必手写 UTC 换算，也就不会和仓库的口径写错成两套。
type row struct {
	metric string
	source string
	at     time.Time
	value  float64
	durMin float64
	unit   string
	vmin   *float64
	vmax   *float64
	meta   map[string]any
}

// daily 造一条只影响单个指标天级聚合的样本（默认手表来源、本地 08:00）。
func daily(met, date string, v float64) row {
	return row{metric: met, source: "Apple Watch", at: at(date, 8, 0), value: v}
}

var runSeq int64

// putSamples 只写原始样本、不重算：睡眠分期这类不该进天级聚合的数据走这条路。
func putSamples(t *testing.T, d *Deps, rows ...row) {
	t.Helper()
	list := make([]sample.Sample, 0, len(rows))
	for _, r := range rows {
		_, off := r.at.Zone()
		end := r.at
		if r.durMin > 0 {
			end = r.at.Add(time.Duration(r.durMin) * time.Minute)
		}
		list = append(list, sample.Sample{
			Metric: r.metric, Source: r.source, Device: r.source,
			StartTime: r.at, EndTime: end,
			Value: r.value, ValueMin: r.vmin, ValueMax: r.vmax,
			Unit:        r.unit,
			LocalDate:   r.at.In(testLoc).Format("2006-01-02"),
			TZOffsetMin: off / 60, Meta: r.meta,
		})
	}
	runSeq++
	if _, _, _, _, err := d.DB.WriteSamples(context.Background(), runSeq, list, "strict"); err != nil {
		t.Fatalf("写入样本失败: %v", err)
	}
}

// putDaily 走「写样本 + 真实 rollup」：MCP 读的是 daily_metrics_day，
// 只有经过这条生产路径，跨来源收敛（sum 后取 max、按样本数加权 mean）才会被一起锁住。
func putDaily(t *testing.T, d *Deps, rows ...row) {
	t.Helper()
	putSamples(t, d, rows...)
	dateSet, metricSet := map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		dateSet[r.at.In(testLoc).Format("2006-01-02")] = true
		metricSet[r.metric] = true
	}
	var dates, mets []string
	for s := range dateSet {
		dates = append(dates, s)
	}
	for s := range metricSet {
		mets = append(mets, s)
	}
	sort.Strings(dates)
	sort.Strings(mets)
	if err := postgres.NewRollup(d.DB, d.Metrics).Recompute(context.Background(), dates, mets); err != nil {
		t.Fatalf("重算天级聚合失败: %v", err)
	}
}

// text 调用返回文本的查询方法；期望成功时使用，失败即 Fatal。
// 传闭包而不是直接传 (string, error)：Go 不允许把多返回值和 t 混在一组实参里，
// 而每个测试都得能一行写完、失败时直接看到真实错误。
func text(t *testing.T, call func() (string, error)) string {
	t.Helper()
	s, err := call()
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	return s
}

// fail 调用期望报错的查询方法，返回错误文本。
func fail(t *testing.T, call func() (string, error)) string {
	t.Helper()
	s, err := call()
	if err == nil {
		t.Fatalf("期望报错，实际成功: %q", s)
	}
	return err.Error()
}

// requireParts 断言文本同时包含若干语义片段，并带上完整输出便于排障。
func requireParts(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(out, p) {
			t.Errorf("输出缺少片段 %q\n实际输出:\n%s", p, out)
		}
	}
}

func forbidParts(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(out, p) {
			t.Errorf("输出不应包含 %q\n实际输出:\n%s", p, out)
		}
	}
}
