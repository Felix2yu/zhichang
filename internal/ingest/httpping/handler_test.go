package httpping

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Felix2yu/zhichang/internal/config"
	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/ingest/pipeline"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
	"github.com/Felix2yu/zhichang/internal/testdb"
)

const testToken = "sekret-ingest-token"

// 真实样例负载：手搓 JSON 会漏掉 HAE 的真实形态（聚合睡眠、Min/Avg/Max 三元组）。
const realPayloadPath = "../../../testdata/hae_export_v2.json"

// 2026 版 HAE 导出形态：来源是对象、duration 按秒、距离字段叫 distance、能量给 kJ。
const newShapePayloadPath = "../../../testdata/hae_export_v3.json"

// 一条最小可用负载，用于把体积边界卡到精确字节。
const minPayload = `{"data":{"metrics":[{"name":"step_count","units":"count",` +
	`"data":[{"date":"2026-10-13 08:00:00 +0800","qty":100,"source":"iPhone"}]}]}}`

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type env struct {
	db  *postgres.DB
	svc *pipeline.Service
	ctx context.Context
}

// newEnv 起一个真库上的服务；每个测试自己 TRUNCATE，避免用例之间互相污染计数。
func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	db, err := postgres.Open(ctx, config.Config{DBDSN: testdb.DSN(t)})
	if err != nil {
		t.Skipf("数据库不可用: %v", err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if _, err := db.SeedMetrics(ctx, metric.Defaults()); err != nil {
		t.Fatalf("字典写入失败: %v", err)
	}
	truncateAll(t, ctx, db)
	t.Cleanup(db.Close)

	home, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("缺少时区数据")
	}
	return &env{db: db, svc: pipeline.New(db, metric.New(metric.Defaults()), home, "strict"), ctx: ctx}
}

func truncateAll(t *testing.T, ctx context.Context, db *postgres.DB) {
	t.Helper()
	const q = `TRUNCATE samples, workout_series, workout_routes, workouts, sleep_sessions, ecg_records, ecg_voltages, ingest_runs, rollup_queue, sync_watermark, sources, daily_metrics, daily_metrics_day`
	if _, err := db.Pool.Exec(ctx, q); err != nil {
		t.Fatalf("清理失败: %v", err)
	}
}

func readRealPayload(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(realPayloadPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type decoded struct {
	RunID          int64    `json:"run_id"`
	Status         string   `json:"status"`
	Received       int      `json:"received"`
	Inserted       int      `json:"inserted"`
	Duplicates     int      `json:"duplicates"`
	Updated        int      `json:"updated"`
	Workouts       int      `json:"workouts"`
	Sleep          int      `json:"sleep"`
	ECG            int      `json:"ecg"`
	Skipped        int      `json:"skipped"`
	DuplicateRun   bool     `json:"duplicate_run"`
	UnknownMetrics []string `json:"unknown_metrics"`
	Affected       struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"affected"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) decoded {
	t.Helper()
	var d decoded
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("响应不是 JSON: %v / body=%s", err, rec.Body.String())
	}
	return d
}

type requestOpt func(*http.Request)

func header(k, v string) requestOpt {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

func do(t *testing.T, h *Handler, body []byte, opts ...requestOpt) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/ingest/hae", bytes.NewReader(body))
	r.Header.Set("X-Zhichang-Token", testToken)
	for _, o := range opts {
		o(r)
	}
	rec := httptest.NewRecorder()
	h.handle(rec, r)
	return rec
}

// TestNewDefaultsMaxBytes 锁住「不给上限时用 512MiB」这条路：
// HAE 全量导出可以到几百 MB，配置漏填时端点不能变成无限制。
func TestNewDefaultsMaxBytes(t *testing.T) {
	for _, v := range []int64{0, -1} {
		h := New(nil, testToken, v, "")
		if h.maxBytes != 512<<20 {
			t.Fatalf("maxBytes=%d 时应回落到 512MiB，实际 %d", v, h.maxBytes)
		}
	}
	h := New(nil, testToken, 1024, "")
	if h.maxBytes != 1024 {
		t.Fatalf("显式上限不该被改写，实际 %d", h.maxBytes)
	}
}

// TestUnauthorized 全部走 nil svc：鉴权失败必须在读体与入库之前就短路，
// 因此这些用例即使没有数据库也应该返回 401。
func TestUnauthorized(t *testing.T) {
	tests := []struct {
		name   string
		token  string
		header map[string]string
	}{
		{"什么头都不带", testToken, nil},
		{"token 配错", testToken, map[string]string{"Authorization": "Bearer wrong"}},
		{"自定义头配错", testToken, map[string]string{"X-Zhichang-Token": "wrong"}},
		{"Bearer 前缀写错", testToken, map[string]string{"Authorization": "token " + testToken}},
		{"Bearer 后为空", testToken, map[string]string{"Authorization": "Bearer "}},
		{"服务端没配 token", "", map[string]string{"X-Zhichang-Token": testToken}},
		{"服务端没配 token 且带 Bearer", "", map[string]string{"Authorization": "Bearer " + testToken}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(nil, tt.token, 0, "")
			r := httptest.NewRequest(http.MethodPost, "/api/ingest/hae", strings.NewReader(minPayload))
			rec := httptest.NewRecorder()
			h.handle(rec, r)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("应 401，实际 %d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "token 无效") {
				t.Fatalf("401 必须带可读原因，实际 %s", rec.Body.String())
			}
		})
	}
}

// TestAuthRejectsBeforeReadingBody：未授权时连请求体都不该被消费，
// 否则任何人都能用一个超大 body 打到磁盘/内存上。
func TestAuthRejectsBeforeReadingBody(t *testing.T) {
	h := New(nil, testToken, 1<<20, "")
	r := httptest.NewRequest(http.MethodPost, "/api/ingest/hae", nil)
	r.Body = io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 4<<20)))
	rec := httptest.NewRecorder()
	h.handle(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("超大 body 也必须先判鉴权，实际 %d", rec.Code)
	}
}

func TestAuthorizedViaBothHeaders(t *testing.T) {
	e := newEnv(t)
	h := New(e.svc, testToken, 0, "")
	tests := []struct {
		name   string
		header map[string]string
	}{
		{"X-Zhichang-Token", map[string]string{"X-Zhichang-Token": testToken}},
		{"Authorization Bearer", map[string]string{"Authorization": "Bearer " + testToken}},
		{"自定义头优先于 Bearer", map[string]string{"X-Zhichang-Token": testToken, "Authorization": "Bearer wrong"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncateAll(t, e.ctx, e.db)
			var opts []requestOpt
			for k, v := range tt.header {
				opts = append(opts, header(k, v))
			}
			rec := do(t, h, []byte(minPayload), opts...)
			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("合法凭据被判 401: %s", rec.Body.String())
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("应 200，实际 %d body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
				t.Fatalf("响应类型应为 JSON，实际 %q", got)
			}
		})
	}
}

// TestRegisterMountsRoute 验证路由挂载（Register 走的是真实 chi 路由器）。
func TestRegisterMountsRoute(t *testing.T) {
	e := newEnv(t)
	h := New(e.svc, testToken, 0, "")
	r := chi.NewRouter()
	h.Register(r)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/ingest/hae", strings.NewReader(minPayload))
	req.Header.Set("X-Zhichang-Token", testToken)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("经路由器应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	// 方法不对时必须 405，而不是静默 404/200：HAE 自动化配错方法要能立刻看出来。
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ingest/hae", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405，实际 %d", rec.Code)
	}
}

func TestIngestHappyPath(t *testing.T) {
	e := newEnv(t)
	h := New(e.svc, testToken, 0, "")
	body := readRealPayload(t)

	rec := do(t, h, body,
		header("session-id", "http-sess-1"),
		header("automation-id", "auto-1"),
		header("automation-name", "每日导出"),
		header("automation-period", "week"),
		header("automation-aggregation", "average"),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got := decode(t, rec)
	if got.Status != "ok" {
		t.Fatalf("status 应为 ok，实际 %q", got.Status)
	}
	if got.RunID == 0 {
		t.Fatal("成功摄入必须返回 run_id")
	}
	// received 是原始条数（9 个指标点 + 1 条运动），inserted 才是落库样本数：
	// 聚合睡眠不产生 sample，两者混用会让「数据少了」的排查彻底跑偏。
	if got.Received != 10 || got.Inserted != 8 {
		t.Fatalf("received/inserted 应为 10/8，实际 %d/%d", got.Received, got.Inserted)
	}
	if got.Duplicates != 0 || got.Updated != 0 || got.Skipped != 0 {
		t.Fatalf("首次纯净数据不该有重复/更新/丢弃: %+v", got)
	}
	if got.Workouts != 1 || got.Sleep != 1 {
		t.Fatalf("运动/睡眠计数错误: workouts=%d sleep=%d", got.Workouts, got.Sleep)
	}
	if got.Affected.From != "2026-10-13" || got.Affected.To != "2026-10-13" {
		t.Fatalf("affected 区间应为样例的 2026-10-13，实际 %q~%q", got.Affected.From, got.Affected.To)
	}
	// 样例里已没有字典外的指标（环境音量已登记进字典），
	// 「未知指标透传」这条口径改由 TestUnknownMetricStillIngested 用假名字覆盖。
	if len(got.UnknownMetrics) != 0 {
		t.Fatalf("样例里不该还有未登记指标，实际 %v", got.UnknownMetrics)
	}

	// 落库与审计必须和响应一致：这是端点唯一可被外部验证的地方。
	var samples int
	if err := e.db.Pool.QueryRow(e.ctx, `SELECT count(*) FROM samples`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if samples != 8 {
		t.Fatalf("库中应有 8 条样本，实际 %d", samples)
	}
	var (
		transport, automationID, automationName, period, aggregation, sha string
		archived                                                          *string
		payloadBytes                                                      int64
	)
	err := e.db.Pool.QueryRow(e.ctx,
		`SELECT transport, COALESCE(automation_id,''), COALESCE(automation_name,''),
		        COALESCE(period,''), COALESCE(aggregation,''), payload_sha256, payload_bytes, archived_path
		 FROM ingest_runs WHERE session_id = 'http-sess-1'`).
		Scan(&transport, &automationID, &automationName, &period, &aggregation, &sha, &payloadBytes, &archived)
	if err != nil {
		t.Fatal(err)
	}
	// transport 恒为 rest：走的是 HTTP 端点，不该被 header 左右。
	if transport != "rest" {
		t.Fatalf("transport 应为 rest，实际 %q", transport)
	}
	// automation_id 是水位表的外键式关联键，丢了它「这个 automation 同步到哪了」就无从查起。
	if automationID != "auto-1" {
		t.Fatalf("automation-id header 未落库，实际 %q", automationID)
	}
	if automationName != "每日导出" || period != "week" || aggregation != "average" {
		t.Fatalf("automation header 未落到审计: %q %q %q", automationName, period, aggregation)
	}
	sum := sha256.Sum256(body)
	if sha != hex.EncodeToString(sum[:]) {
		t.Fatalf("payload_sha256 应为请求体摘要，实际 %q", sha)
	}
	if payloadBytes != int64(len(body)) {
		t.Fatalf("payload_bytes 应为 %d，实际 %d", len(body), payloadBytes)
	}
	if archived != nil && *archived != "" {
		t.Fatalf("未配置归档目录时 archived_path 必须为空，实际 %q", *archived)
	}
}

// TestSameSessionTwiceIsDuplicate 是幂等口径的核心：手机重推同一个 session
// 必须回放上次结果、复用 run_id，且库里的样本一条不多。
func TestSameSessionTwiceIsDuplicate(t *testing.T) {
	e := newEnv(t)
	h := New(e.svc, testToken, 0, "")
	body := readRealPayload(t)

	first := decode(t, do(t, h, body, header("session-id", "http-dup-1")))
	if first.Status != "ok" || first.DuplicateRun {
		t.Fatalf("首次应为 ok: %+v", first)
	}

	second := decode(t, do(t, h, body, header("session-id", "http-dup-1")))
	if second.Status != "duplicate" {
		t.Fatalf("第二次 status 应为 duplicate，实际 %q", second.Status)
	}
	if !second.DuplicateRun {
		t.Fatal("duplicate_run 标志必须为 true，客户端据此区分重推与正常写入")
	}
	if second.Message == "" {
		t.Fatal("duplicate 必须带说明文案，否则用户在 HAE 日志里看不到原因")
	}
	if second.RunID != first.RunID {
		t.Fatalf("重复 session 必须复用同一条 run，实际 %d vs %d", first.RunID, second.RunID)
	}
	// 回放的是上次落库的统计，不能是本次 payload 的数字。
	if second.Received != first.Received || second.Inserted != first.Inserted ||
		second.Duplicates != first.Duplicates || second.Affected.To != first.Affected.To {
		t.Fatalf("回放统计与首次不一致: first=%+v second=%+v", first, second)
	}

	var samples, runs int
	if err := e.db.Pool.QueryRow(e.ctx, `SELECT count(*) FROM samples`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if err := e.db.Pool.QueryRow(e.ctx, `SELECT count(*) FROM ingest_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if samples != 8 {
		t.Fatalf("重复推送不能双计，库中样本应为 8，实际 %d", samples)
	}
	if runs != 1 {
		t.Fatalf("重复 session 不该新增 run，实际 run 数 %d", runs)
	}

	// 换了 session 但数据相同：走样本级去重，而不是 run 级回放。
	third := decode(t, do(t, h, body, header("session-id", "http-dup-2")))
	if third.Status != "ok" {
		t.Fatalf("新 session 应正常入库，实际 %q", third.Status)
	}
	if third.RunID == first.RunID {
		t.Fatal("新 session 必须开新的 run")
	}
	if third.Inserted != 0 || third.Duplicates != 8 {
		t.Fatalf("相同数据应全部命中样本级去重，实际 inserted=%d duplicates=%d", third.Inserted, third.Duplicates)
	}
	if err := e.db.Pool.QueryRow(e.ctx, `SELECT count(*) FROM samples`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if samples != 8 {
		t.Fatalf("样本级去重后库里仍是 8 条，实际 %d", samples)
	}
}

// TestNoSessionHeaderSkipsIdempotency：HAE 手动导出没有 session-id，
// 此时不能做 run 级幂等（否则第二次手动导出会被判重复、数据永远进不来）。
func TestNoSessionHeaderSkipsIdempotency(t *testing.T) {
	e := newEnv(t)
	h := New(e.svc, testToken, 0, "")
	body := readRealPayload(t)

	first := decode(t, do(t, h, body))
	if first.Status != "ok" {
		t.Fatalf("无 session 也应摄入成功，实际 %q body=%q", first.Status, first.Message)
	}
	second := decode(t, do(t, h, body))
	if second.Status != "ok" || second.RunID == first.RunID {
		t.Fatalf("无 session 时每次都开新 run: first=%d second=%+v", first.RunID, second)
	}
	var samples int
	if err := e.db.Pool.QueryRow(e.ctx, `SELECT count(*) FROM samples`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if samples != 8 {
		t.Fatalf("重复数据靠样本级去重挡住，库里仍是 8 条，实际 %d", samples)
	}
}

func TestSizeLimitBoundary(t *testing.T) {
	e := newEnv(t)
	body := []byte(minPayload)
	n := int64(len(body))
	tests := []struct {
		name     string
		maxBytes int64
		wantCode int
	}{
		{"正好等于上限要收", n, http.StatusOK},
		{"超出一个字节就拒", n - 1, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncateAll(t, e.ctx, e.db)
			h := New(e.svc, testToken, tt.maxBytes, "")
			rec := do(t, h, body)
			if rec.Code != tt.wantCode {
				t.Fatalf("应 %d，实际 %d body=%s", tt.wantCode, rec.Code, rec.Body.String())
			}
		})
	}
	// 上限的判定发生在归档与解析之前：超限请求不能在审计/归档里留下半个 payload。
	h := New(e.svc, testToken, 10, "")
	rec := do(t, h, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("应 413，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "请求体超过上限") {
		t.Fatalf("413 应带原因，实际 %s", rec.Body.String())
	}
	var runs int
	if err := e.db.Pool.QueryRow(e.ctx, `SELECT count(*) FROM ingest_runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("413 不该留下审计，实际 run 数 %d", runs)
	}
}

func TestBadJSONIsBadRequest(t *testing.T) {
	e := newEnv(t)
	h := New(e.svc, testToken, 0, "")
	tests := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{"空请求体", "", "请求体为空"},
		{"截断的 JSON", `{"data":{"metrics":[`, "解析 JSON 失败"},
		{"类型不符", `{"data":{"metrics":"nope"}}`, "解析 JSON 失败"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncateAll(t, e.ctx, e.db)
			rec := do(t, h, []byte(tt.body), header("session-id", "bad-json-"+strings.ReplaceAll(tt.name, " ", "-")))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("应 400，实际 %d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantMsg) {
				t.Fatalf("400 应带解析原因 %q，实际 %s", tt.wantMsg, rec.Body.String())
			}
			// 解析失败发生在 BeginRun 之前：审计表与样本表都不能被碰。
			var runs, samples int
			if err := e.db.Pool.QueryRow(e.ctx, `SELECT count(*) FROM ingest_runs`).Scan(&runs); err != nil {
				t.Fatal(err)
			}
			if err := e.db.Pool.QueryRow(e.ctx, `SELECT count(*) FROM samples`).Scan(&samples); err != nil {
				t.Fatal(err)
			}
			if runs != 0 || samples != 0 {
				t.Fatalf("坏 JSON 不该入库，runs=%d samples=%d", runs, samples)
			}
		})
	}
}

// TestUnreadableBody：请求体读取失败（客户端断连等）必须是 400 而不是 500——
// 这是客户端问题，不该污染服务的错误率。
func TestUnreadableBody(t *testing.T) {
	e := newEnv(t)
	h := New(e.svc, testToken, 0, "")
	r := httptest.NewRequest(http.MethodPost, "/api/ingest/hae", nil)
	r.Header.Set("X-Zhichang-Token", testToken)
	r.Body = io.NopCloser(errReader{})
	rec := httptest.NewRecorder()
	h.handle(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("应 400，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "读取请求体失败") {
		t.Fatalf("应说明读取失败，实际 %s", rec.Body.String())
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, fmt.Errorf("连接被对端重置") }

// TestEmptyPayloadReturnsEmpty 锁 IsEmpty 的真实口径（见 hae.TestIsEmpty）：
// 「空」只指三类数据结构一个都没有；「结构在、点为空」不是空，
// 它照样开一条 run 让审计能对上手机侧的那次导出。
func TestEmptyPayloadReturnsEmpty(t *testing.T) {
	e := newEnv(t)
	h := New(e.svc, testToken, 0, "")
	tests := []struct {
		name      string
		body      string
		wantRun   bool
		wantStats bool // status 期望值：wantRun=false 时为 empty
	}{
		{"空对象", `{}`, false, false},
		{"只有 data 键", `{"data":{}}`, false, false},
		{"三类都空数组", `{"data":{"metrics":[],"workouts":[],"sleep":[]}}`, true, true},
		{"只有指标名没有点", `{"data":{"metrics":[{"name":"step_count","units":"count","data":[]}]}}`, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncateAll(t, e.ctx, e.db)
			rec := do(t, h, []byte(tt.body), header("session-id", "empty-"+strings.ReplaceAll(tt.name, " ", "-")))
			if rec.Code != http.StatusOK {
				t.Fatalf("空导出不该报错，应 200，实际 %d body=%s", rec.Code, rec.Body.String())
			}
			got := decode(t, rec)
			if got.Received != 0 || got.Inserted != 0 || got.Workouts != 0 || got.Sleep != 0 {
				t.Fatalf("没有数据点时计数必须全 0: %+v", got)
			}
			var runs int
			if err := e.db.Pool.QueryRow(e.ctx, `SELECT count(*) FROM ingest_runs`).Scan(&runs); err != nil {
				t.Fatal(err)
			}
			if !tt.wantRun {
				if got.Status != "empty" || got.RunID != 0 {
					t.Fatalf("status 应为 empty 且无 run，实际 %q run=%d", got.Status, got.RunID)
				}
				if got.Message != "本次导出没有数据" {
					t.Fatalf("empty 应带固定文案，实际 %q", got.Message)
				}
				if runs != 0 {
					t.Fatalf("empty 导出不应占审计，实际 run 数 %d", runs)
				}
				return
			}
			if got.Status != "ok" || got.RunID == 0 {
				t.Fatalf("有结构无数据点时应正常开 run，实际 status=%q run=%d", got.Status, got.RunID)
			}
			if runs != 1 {
				t.Fatalf("应留下 1 条审计记录，实际 %d", runs)
			}
			// 没有样本就没有受影响日期：affected 必须留空，否则前端会去重算不存在的那天。
			if got.Affected.From != "" || got.Affected.To != "" {
				t.Fatalf("无数据时 affected 应为空，实际 %q~%q", got.Affected.From, got.Affected.To)
			}
		})
	}
}

func TestArchiveWritesPayloadToDisk(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	h := New(e.svc, testToken, 0, dir)
	body := readRealPayload(t)

	rec := do(t, h, body, header("session-id", "archive-sess-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}

	// 归档按 2006-01-02 建日子目录、以 session-id 为文件名：这是回溯某次推送的唯一凭证。
	day := time.Now().Format("2006-01-02")
	want := filepath.Join(dir, day, "archive-sess-1.json")
	raw, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("归档文件应落在 %s: %v", want, err)
	}
	if !bytes.Equal(raw, body) {
		t.Fatal("归档内容必须与请求体逐字节一致")
	}

	// 归档路径同时写进审计表，运维才能从 run 反查文件。
	var archived string
	if err := e.db.Pool.QueryRow(e.ctx,
		`SELECT COALESCE(archived_path,'') FROM ingest_runs WHERE session_id='archive-sess-1'`).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if archived != want {
		t.Fatalf("审计里的归档路径应为 %s，实际 %q", want, archived)
	}
}

func TestArchiveFileNameWithoutSession(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	h := New(e.svc, testToken, 0, dir)
	body := readRealPayload(t)

	if rec := do(t, h, body); rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	// 没有 session-id 时用 纳秒-摘要前12位，保证同一秒内的两次手动导出不会互相覆盖。
	sum := sha256.Sum256(body)
	wantSuffix := hex.EncodeToString(sum[:])[:12]

	day := time.Now().Format("2006-01-02")
	entries, err := os.ReadDir(filepath.Join(dir, day))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("应只归档 1 个文件，实际 %d", len(entries))
	}
	name := entries[0].Name()
	if !regexp.MustCompile(`^[0-9]{10,}-` + wantSuffix + `\.json$`).MatchString(name) {
		t.Fatalf("文件名应为 <纳秒时间戳>-<sha 前 12 位>.json，实际 %q", name)
	}

	// 再来一次同样的 payload（仍无 session）：摘要相同但纳秒不同，必须是两个文件。
	if rec := do(t, h, body); rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d", rec.Code)
	}
	entries, err = os.ReadDir(filepath.Join(dir, day))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("无 session 的重复推送应各留一份归档，实际 %d 个", len(entries))
	}
}

// TestArchiveFailureDoesNotBlockIngest：归档只是排障用的旁路，
// 目录不可写时绝不能把摄入打挂（手机侧无法重试成功的推送会累积重传）。
// 三种失败形态要分开锁：MkdirAll 撞普通文件（ENOTDIR）、MkdirAll 撞只读目录（EACCES）、
// 日期目录已存在但不可写时卡在 WriteFile——它们是 archive() 里两条不同的 return 路径。
func TestArchiveFailureDoesNotBlockIngest(t *testing.T) {
	day := time.Now().Format("2006-01-02")
	tests := []struct {
		name       string
		wantDayDir bool // 日期目录是否本来就存在（存在时只查有没有留下半个文件）
		prepare    func(t *testing.T, base string) string
	}{
		{"归档根是普通文件", false, func(t *testing.T, base string) string {
			p := filepath.Join(base, "afile")
			if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			return p
		}},
		{"归档根不可写", false, func(t *testing.T, base string) string {
			p := filepath.Join(base, "locked")
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			// 0o500：可读可进但不可写，MkdirAll 建日期子目录必然失败。
			if err := os.Chmod(p, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(p, 0o755) })
			return p
		}},
		{"日期目录已存在但不可写", true, func(t *testing.T, base string) string {
			p := filepath.Join(base, "preset")
			inner := filepath.Join(p, day)
			if err := os.MkdirAll(inner, 0o755); err != nil {
				t.Fatal(err)
			}
			// 日期目录已就位，MkdirAll 会直接放过，失败点落到 WriteFile。
			if err := os.Chmod(inner, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(inner, 0o755) })
			return p
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			base := t.TempDir()
			archiveTo := tt.prepare(t, base)

			h := New(e.svc, testToken, 0, archiveTo)
			body := readRealPayload(t)
			rec := do(t, h, body, header("session-id", "archive-locked"))
			if rec.Code != http.StatusOK {
				t.Fatalf("归档失败不该影响入库，应 200，实际 %d body=%s", rec.Code, rec.Body.String())
			}
			got := decode(t, rec)
			if got.Status != "ok" || got.Inserted != 8 {
				t.Fatalf("数据应正常入库: %+v", got)
			}

			var archived string
			if err := e.db.Pool.QueryRow(e.ctx,
				`SELECT COALESCE(archived_path,'') FROM ingest_runs WHERE session_id='archive-locked'`).Scan(&archived); err != nil {
				t.Fatal(err)
			}
			if archived != "" {
				t.Fatalf("归档失败时路径必须留空，实际 %q", archived)
			}
			// 归档路径不可用时也不能凭空造出目录。
			if !tt.wantDayDir {
				if _, err := os.Stat(filepath.Join(archiveTo, day)); err == nil {
					t.Fatal("归档失败时不应建出日期目录")
				}
			}
			// 目录不可写时也不能留下半个文件。
			if entries, err := os.ReadDir(filepath.Join(archiveTo, day)); err == nil && len(entries) > 0 {
				t.Fatalf("归档失败时日期目录应为空，实际 %d 个文件", len(entries))
			}
		})
	}
}

// TestIngestFailureIs500AndRecorded：入库失败必须 500，且审计里留下 error 状态与原因，
// 否则运维只能看到「手机推过了但库里没有」。
func TestIngestFailureIs500AndRecorded(t *testing.T) {
	e := newEnv(t)
	// 用一个必然违约的约束把 WriteSamples 打挂：这是最贴近真实的失败注入点，
	// 不依赖删表之类的破坏性操作，收尾时摘掉即可恢复。
	const chk = `ing_test_value_cap`
	ctx := e.ctx
	if _, err := e.db.Pool.Exec(ctx, `ALTER TABLE samples ADD CONSTRAINT `+chk+` CHECK (value < 1000000)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.db.Pool.Exec(context.Background(), `ALTER TABLE samples DROP CONSTRAINT IF EXISTS `+chk)
	})

	bad := []byte(`{"data":{"metrics":[{"name":"step_count","units":"count",` +
		`"data":[{"date":"2026-10-13 08:00:00 +0800","qty":2000000,"source":"iPhone"}]}]}}`)
	h := New(e.svc, testToken, 0, "")
	rec := do(t, h, bad, header("session-id", "fail-500"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("入库失败应 500，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "入库失败") {
		t.Fatalf("500 应带原因，实际 %s", rec.Body.String())
	}

	var status, errMsg string
	if err := e.db.Pool.QueryRow(ctx,
		`SELECT status, COALESCE(error,'') FROM ingest_runs WHERE session_id='fail-500'`).Scan(&status, &errMsg); err != nil {
		t.Fatal(err)
	}
	if status != "error" || errMsg == "" {
		t.Fatalf("失败的 run 必须记 error 状态与原因，实际 %q / %q", status, errMsg)
	}
	var samples int
	if err := e.db.Pool.QueryRow(ctx, `SELECT count(*) FROM samples`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if samples != 0 {
		t.Fatalf("失败的合并语句不应留下半截数据，实际 %d 条", samples)
	}

	// 同一 session 重推时命中 run 级幂等：回放上次（失败）的统计而不是重新摄入。
	// 这条行为要显式锁住——运维必须知道「换个 session-id 才能补数据」。
	rec = do(t, h, bad, header("session-id", "fail-500"))
	if rec.Code != http.StatusOK {
		t.Fatalf("重推同一 session 应回放上次结果，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got := decode(t, rec)
	if got.Status != "duplicate" || !got.DuplicateRun {
		t.Fatalf("重推应判 duplicate: %+v", got)
	}
	if err := e.db.Pool.QueryRow(ctx, `SELECT count(*) FROM samples`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if samples != 0 {
		t.Fatalf("重推失败 session 不该补数据，实际 %d 条", samples)
	}
}

// TestUnknownMetricStillIngested：未知指标会透传给客户端，但仍然落库
// （字典只约束聚合口径，不该静默丢数据）。
//
// 指标名刻意用构造出来的假名：拿「当下恰好未登记」的真实指标来测，
// 那个指标一旦被登记进字典，这条测试就静默地不再测任何东西
// （environmental_audio_exposure 就经历了这个过程）。
func TestUnknownMetricStillIngested(t *testing.T) {
	e := newEnv(t)
	h := New(e.svc, testToken, 0, "")
	body := `{"data":{"metrics":[{"name":"zzz_probe_unregistered","units":"count","data":[
		{"date":"2026-10-13 08:00:00 +0800","qty":7,"source":"iPhone"}]}]}}`
	rec := do(t, h, []byte(body), header("session-id", "unknown-metrics"))
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got := decode(t, rec)
	if len(got.UnknownMetrics) != 1 || got.UnknownMetrics[0] != "zzz_probe_unregistered" {
		t.Fatalf("未知指标必须回传，实际 %v", got.UnknownMetrics)
	}
	var n int
	if err := e.db.Pool.QueryRow(e.ctx, `SELECT count(*) FROM samples WHERE metric='zzz_probe_unregistered'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("未知指标也应入库，实际 %d 条", n)
	}
}

// TestIngestNewExportShapeIsNotBadRequest 回归的是最贵的一次故障：
// 新版 HAE 把 workouts[].source 从字符串改成对象后，解析层抛类型错误 → 整包 400，
// 指标、运动、睡眠一条都进不去，而客户端只看到「请求体不是合法 JSON」。
// 这里从端点一路验到库里的数值与单位——端点通过但数值虚高的故障同样致命。
func TestIngestNewExportShapeIsNotBadRequest(t *testing.T) {
	e := newEnv(t)
	h := New(e.svc, testToken, 0, "")
	body, err := os.ReadFile(newShapePayloadPath)
	if err != nil {
		t.Fatal(err)
	}

	rec := do(t, h, body, header("session-id", "v3-shape"))
	if rec.Code != http.StatusOK {
		t.Fatalf("新版导出必须能入库，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got := decode(t, rec)
	if got.Status != "ok" {
		t.Fatalf("status 应为 ok，实际 %q（%s）", got.Status, got.Message)
	}
	if got.Inserted != 5 || got.Workouts != 2 || got.Sleep != 1 || got.Skipped != 0 {
		t.Fatalf("入库计数错误: %+v", got)
	}
	if len(got.UnknownMetrics) != 0 {
		t.Fatalf("新版样例不该有未登记指标: %v", got.UnknownMetrics)
	}

	// 能量必须在端点这一层就已经是字典口径：库里留 kJ 会让界面显示错 4.18 倍
	var value float64
	var unit string
	if err := e.db.Pool.QueryRow(e.ctx,
		`SELECT value, unit FROM samples WHERE metric='active_energy'`).Scan(&value, &unit); err != nil {
		t.Fatal(err)
	}
	if unit != "kcal" || value < 300.0 || value > 300.01 {
		t.Fatalf("活动能量应为约 300.00 kcal，实际 %v %s", value, unit)
	}

	// 运动时长按分钟落库（新版给的是秒）：1234.5 秒 → 20.58 分钟
	var durationMin float64
	if err := e.db.Pool.QueryRow(e.ctx,
		`SELECT duration_min FROM workouts WHERE name='户外 步行'`).Scan(&durationMin); err != nil {
		t.Fatal(err)
	}
	if durationMin < 20.57 || durationMin > 20.58 {
		t.Fatalf("运动时长应为约 20.58 分钟（秒未换算会得到 1234），实际 %v", durationMin)
	}

	// 距离字段换了名字也必须有值
	var distance *float64
	if err := e.db.Pool.QueryRow(e.ctx,
		`SELECT total_distance FROM workouts WHERE name='户外 步行'`).Scan(&distance); err != nil {
		t.Fatal(err)
	}
	if distance == nil || *distance < 1.85 || *distance > 1.86 {
		t.Fatalf("运动距离应有值（约 1.8525 km），实际 %v", distance)
	}
}

// 心电图整条链路：解析（source 是对象、电压带 mcV 单位）→ 归一（本地化判定
// 收敛成稳定码）→ 落记录表 + 波形表 → 响应回报 ecg 计数。
//
// 这条路径在此之前是完全缺失的：payload 结构体里根本没有 ecg 字段，
// json.Decode 默认忽略未知字段，于是 9 条记录连同判定结果在解析第一步就
// 静默消失——不报错、不提示、日志一片干净。
func TestIngestECGRecordsAndWaveform(t *testing.T) {
	e := newEnv(t)
	h := New(e.svc, testToken, 0, "")

	body, err := os.ReadFile(newShapePayloadPath)
	if err != nil {
		t.Fatal(err)
	}
	rec := do(t, h, body, header("session-id", "ecg-shape"))
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	got := decode(t, rec)
	if got.ECG != 1 {
		t.Fatalf("响应应回报 1 条心电记录，实际 %d（body=%s）", got.ECG, rec.Body.String())
	}
	// 心电不产生 samples，所以它不能混进 received：否则 received 与落库条数对不上，
	// 真正丢数时反而看不出来。
	if got.Received != 8 {
		t.Fatalf("received 应仍为 8（6 个指标点 + 2 条运动），实际 %d", got.Received)
	}
	if got.Skipped != 0 {
		t.Fatalf("不该跳过任何记录，实际 %d", got.Skipped)
	}

	var class, code, unit string
	var avgHR float64
	var fs, n int
	if err := e.db.Pool.QueryRow(e.ctx,
		`SELECT classification, classification_code, average_heart_rate, sampling_frequency, voltage_count
		 FROM ecg_records`).Scan(&class, &code, &avgHR, &fs, &n); err != nil {
		t.Fatalf("心电记录未入库: %v", err)
	}
	if class != "窦性心律" {
		t.Fatalf("上游原文必须原样保留，实际 %q", class)
	}
	if code != "sinus_rhythm" {
		t.Fatalf("本地化判定应收敛成稳定码 sinus_rhythm，实际 %q", code)
	}
	if avgHR != 66 || fs != 512 || n != 5 {
		t.Fatalf("心率/采样率/点数错误: %v %d %d", avgHR, fs, n)
	}

	var voltages []float64
	if err := e.db.Pool.QueryRow(e.ctx,
		`SELECT unit, voltages FROM ecg_voltages`).Scan(&unit, &voltages); err != nil {
		t.Fatalf("波形未入库: %v", err)
	}
	if unit != "mcV" {
		t.Fatalf("电压单位应原样保留 mcV，实际 %q", unit)
	}
	if len(voltages) != 5 {
		t.Fatalf("波形应有 5 点，实际 %d", len(voltages))
	}
	// 浮点必须原样往返，任何按 float32 落库的实现都会在这里露馅
	if voltages[0] != -63.284713745117188 || voltages[4] != 407.3128662109375 {
		t.Fatalf("电压值被截断: %v ~ %v", voltages[0], voltages[4])
	}

	// 心电图是事件数据，不该在天级聚合里留下任何东西
	var rolled int
	if err := e.db.Pool.QueryRow(e.ctx,
		`SELECT count(*) FROM rollup_queue WHERE metric LIKE '%ecg%'`).Scan(&rolled); err != nil {
		t.Fatal(err)
	}
	if rolled != 0 {
		t.Fatalf("心电不该进聚合队列，实际 %d 条", rolled)
	}
}
