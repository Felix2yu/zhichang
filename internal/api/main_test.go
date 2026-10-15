package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Felix2yu/zhichang/internal/config"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/domain/sample"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
	"github.com/Felix2yu/zhichang/internal/testdb"
)

// TestMain 起停本包的测试库；ZHICHANG_TEST_DSN 存在时直接用它。
func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

// 仪表盘这一层的口径全都绑在「本地时区」上（today、缺省区间、本夜轴），
// 所以测试固定用一个东八区时区，而不是跟着机器 TZ 变。
const homeTZ = "Asia/Shanghai"

var homeLoc = func() *time.Location {
	if loc, err := time.LoadLocation(homeTZ); err == nil {
		return loc
	}
	// 极简镜像里没有 tzdata：上海自 1991 年起无夏令时，固定 +0800 足够。
	return time.FixedZone(homeTZ, 8*3600)
}()

var (
	dbOnce sync.Once
	shared *postgres.DB
	dbErr  error
)

// testDB 复用同一个库连接：embedded-postgres 每次启动要 6-9 秒，
// 而表数据由各测试开头 TRUNCATE 保证隔离，不需要一测试一库。
func testDB(t *testing.T) *postgres.DB {
	t.Helper()
	// 先在外面问 DSN：库不可用时在这里干净地 Skip，
	// 不能在 sync.Once 里 Skip——Goexit 会把 once 标成已完成，后续测试拿到 nil 直接 panic。
	dsn := testdb.DSN(t)
	dbOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		shared, dbErr = postgres.Open(ctx, config.Config{DBDSN: dsn})
		if dbErr != nil {
			return
		}
		// goals 表（0003）故意留给 Migrate，与生产启动路径一致
		if _, dbErr = shared.Migrate(ctx); dbErr != nil {
			return
		}
		_, dbErr = shared.SeedMetrics(ctx, metric.Defaults())
	})
	if dbErr != nil {
		t.Fatalf("测试库准备失败: %v", dbErr)
	}
	return shared
}

// reset 清空业务表：测试之间不能依赖执行顺序。
func reset(t *testing.T, db *postgres.DB) {
	t.Helper()
	const q = `TRUNCATE samples, daily_metrics, daily_metrics_day, rollup_queue,
		workouts, sleep_sessions, ecg_records, ecg_voltages, sources, ingest_runs, sync_watermark, goals,
		app_settings`
	if _, err := db.Pool.Exec(context.Background(), q); err != nil {
		t.Fatalf("清表失败: %v", err)
	}
}

// newRouter 起一个挂着真实 Dashboard 路由的 chi.Router（生产怎么挂就怎么测）。
func newRouter(t *testing.T, settingsToken string) (chi.Router, *postgres.DB) {
	t.Helper()
	db := testDB(t)
	reset(t, db)
	reg, _, err := db.LoadMetrics(context.Background())
	if err != nil {
		t.Fatalf("加载指标字典失败: %v", err)
	}
	d := NewDashboard(db, reg, homeLoc, homeTZ, settingsToken)
	r := chi.NewRouter()
	d.Register(r)
	return r, db
}

// reply 是一次 HTTP 调用的结果。
type reply struct {
	status int
	body   string
	fields map[string]any
}

var runSeq atomic.Int64

// newRun 登记一次摄入，拿到能挂外键语义的 runID（同时让 sync/status 有东西可读）。
func newRun(t *testing.T, db *postgres.DB) int64 {
	t.Helper()
	id, dup, err := db.BeginRun(context.Background(), postgres.RunMeta{
		Transport:    "test",
		SessionID:    fmt.Sprintf("api-test-%d", runSeq.Add(1)),
		AutomationID: "api-test",
	})
	if err != nil || dup {
		t.Fatalf("登记摄入失败: %v dup=%v", err, dup)
	}
	return id
}

func call(t *testing.T, h http.Handler, method, target string, body []byte, hdr map[string]string, ctx context.Context) reply {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	out := reply{status: rec.Code, body: rec.Body.String()}
	// 这一层是前端唯一的取数口子，Content-Type 必须原样带 charset
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q，期望 application/json; charset=utf-8", ct)
	}
	if out.body != "" {
		if err := json.Unmarshal([]byte(out.body), &out.fields); err != nil {
			t.Fatalf("响应不是合法 JSON: %v\n%s", err, out.body)
		}
	}
	return out
}

func get(t *testing.T, h http.Handler, target string) reply {
	t.Helper()
	return call(t, h, http.MethodGet, target, nil, nil, nil)
}

func getWith(t *testing.T, h http.Handler, target string, ctx context.Context) reply {
	t.Helper()
	return call(t, h, http.MethodGet, target, nil, nil, ctx)
}

func putJSON(t *testing.T, h http.Handler, target, body, token string) reply {
	t.Helper()
	return call(t, h, http.MethodPut, target, []byte(body), map[string]string{"Authorization": "Bearer " + token}, nil)
}

func deleteWith(t *testing.T, h http.Handler, target, token string) reply {
	t.Helper()
	return call(t, h, http.MethodDelete, target, nil, map[string]string{"Authorization": "Bearer " + token}, nil)
}

// arr 取一个必须是数组的键。null 会让前端 .map() 直接抛错，所以这个断言不能省。
func (r reply) arr(t *testing.T, key string) []any {
	t.Helper()
	v, ok := r.fields[key]
	if !ok {
		t.Fatalf("响应缺少键 %q: %s", key, r.body)
	}
	a, ok := v.([]any)
	if !ok {
		t.Fatalf("键 %q 必须是 JSON 数组而不是 null: %s", key, r.body)
	}
	return a
}

func (r reply) str(t *testing.T, key string) string {
	t.Helper()
	v, ok := r.fields[key]
	if !ok {
		t.Fatalf("响应缺少键 %q: %s", key, r.body)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("键 %q 不是字符串: %v", key, v)
	}
	return s
}

func (r reply) num(t *testing.T, key string) float64 {
	t.Helper()
	v, ok := r.fields[key]
	if !ok {
		t.Fatalf("响应缺少键 %q: %s", key, r.body)
	}
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("键 %q 不是数字: %v", key, v)
	}
	return n
}

// wantErr 校验统一错误结构 {error, status}，错误文案是给用户看的，不能为空。
func (r reply) wantErr(t *testing.T, code int) {
	t.Helper()
	if r.status != code {
		t.Fatalf("状态码 = %d，期望 %d（body=%s）", r.status, code, r.body)
	}
	if r.str(t, "error") == "" {
		t.Fatalf("错误响应缺少 error 文案: %s", r.body)
	}
	if int(r.num(t, "status")) != code {
		t.Fatalf("status 字段 = %v，期望 %d", r.fields["status"], code)
	}
}

// dateInLoc 返回基准日 ±delta 天在本地时区下的 YYYY-MM-DD（断言别写死随时间变化的日期）。
func dateInLoc(delta int) string {
	return time.Now().In(homeLoc).AddDate(0, 0, delta).Format("2006-01-02")
}

// wallAt 把某个本地日期的墙钟时刻换算成 UTC 瞬间（库内一律存 UTC）。
// 日期都来自用例字面量，解析失败即测试写错，直接 panic 更好定位。
func wallAt(date string, hour, minute int) time.Time {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(fmt.Sprintf("用例里的日期非法 %q: %v", date, err))
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, homeLoc)
}

// addSamples 走真实写接口造样本数据。
func addSamples(t *testing.T, db *postgres.DB, runID int64, ss ...sample.Sample) {
	t.Helper()
	for i := range ss {
		if ss[i].EndTime.IsZero() {
			ss[i].EndTime = ss[i].StartTime
		}
	}
	if _, _, _, _, err := db.WriteSamples(context.Background(), runID, ss, "strict"); err != nil {
		t.Fatalf("写样本失败: %v", err)
	}
}

func stepSample(date string, hour, minute int, v float64, src string) sample.Sample {
	at := wallAt(date, hour, minute).UTC()
	return sample.Sample{
		Metric: "step_count", Source: src, StartTime: at, EndTime: at,
		Value: v, Unit: "count", LocalDate: date, TZOffsetMin: 480,
	}
}

// recompute 用生产聚合口径把样本收敛到天级（daily_metrics_day 是 summary/daily 的数据源）。
func recompute(t *testing.T, db *postgres.DB, dates, metrics []string) {
	t.Helper()
	reg, _, err := db.LoadMetrics(context.Background())
	if err != nil {
		t.Fatalf("加载字典失败: %v", err)
	}
	if err := postgres.NewRollup(db, reg).Recompute(context.Background(), dates, metrics); err != nil {
		t.Fatalf("重算天级聚合失败: %v", err)
	}
}
