package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// Dashboard 提供仪表盘只读查询接口；目标与偏好写入是唯一需要令牌的口子。
type Dashboard struct {
	db     *postgres.DB
	metric *metric.Registry
	loc    *time.Location
	tzName string
	// settingsToken 为空时目标与设置只读：宁可不给写，也不能在未配置令牌时敞开写接口。
	settingsToken string
}

// NewDashboard 构造仪表盘 API。
func NewDashboard(db *postgres.DB, reg *metric.Registry, loc *time.Location, tzName, settingsToken string) *Dashboard {
	return &Dashboard{db: db, metric: reg, loc: loc, tzName: tzName, settingsToken: settingsToken}
}

// Register 挂载查询路由。
func (d *Dashboard) Register(r chi.Router) {
	r.Get("/api/metrics", d.listMetrics)
	r.Get("/api/summary", d.summary)
	r.Get("/api/daily", d.daily)
	r.Get("/api/baseline", d.baseline)
	r.Get("/api/insights", d.insights)
	r.Get("/api/coverage", d.coverage)
	r.Get("/api/samples", d.samples)
	r.Get("/api/sleep", d.sleep)
	r.Get("/api/sleep/stages", d.sleepStages)
	r.Get("/api/sleep/stages/range", d.sleepStagesRange)
	r.Get("/api/sleep/night-metrics", d.sleepNightMetrics)
	r.Get("/api/workouts", d.workouts)
	r.Get("/api/workouts/series", d.workoutSeries)
	r.Get("/api/workouts/route", d.workoutRoute)
	r.Get("/api/ecg", d.ecgRecords)
	r.Get("/api/ecg/waveform", d.ecgWaveform)
	r.Get("/api/sources", d.sources)
	r.Get("/api/goals", d.listGoals)
	r.Get("/api/settings", d.listSettings)
	r.Get("/api/sync/status", d.syncStatus)
	// 写接口：整个仪表盘只有这四条，必须带 ZHICHANG_SETTINGS_TOKEN
	r.Put("/api/goals", d.requireSettings(d.putGoal))
	r.Delete("/api/goals", d.requireSettings(d.deleteGoal))
	r.Put("/api/settings", d.requireSettings(d.putSetting))
	r.Delete("/api/settings", d.requireSettings(d.deleteSetting))
}

// requireSettings 校验 Bearer 令牌。未配置令牌时直接 503——
// 不能因为「还没设令牌」就把写接口敞开。
func (d *Dashboard) requireSettings(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.settingsToken == "" {
			WriteError(w, http.StatusServiceUnavailable, "未配置 ZHICHANG_SETTINGS_TOKEN，目标与设置只读")
			return
		}
		given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(given), []byte(d.settingsToken)) != 1 {
			WriteError(w, http.StatusUnauthorized, "令牌不正确")
			return
		}
		next(w, r)
	}
}

func (d *Dashboard) listGoals(w http.ResponseWriter, r *http.Request) {
	rows, err := d.db.Goals(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 单位与中文名一律从指标字典取，goals 表只存数值
	out := make([]goalItem, 0, len(rows))
	for _, g := range rows {
		item := goalItem{Metric: g.Metric, Target: g.Target, DisplayName: g.Metric, UpdatedAt: g.UpdatedAt}
		if def, ok := d.metric.Get(g.Metric); ok {
			item.DisplayName = def.DisplayNameZH
			item.Unit = def.PreferredUnit
		}
		out = append(out, item)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"goals": out})
}

func (d *Dashboard) putGoal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Metric string  `json:"metric"`
		Target float64 `json:"target"`
	}
	// 目标体很小，给 4KB 上限足够，别让未鉴权的请求体撑内存
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		WriteError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if in.Metric == "" {
		WriteError(w, http.StatusBadRequest, "metric 不能为空")
		return
	}
	if err := d.db.SetGoal(r.Context(), in.Metric, in.Target, d.metric.IsKnown(in.Metric)); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	d.listGoals(w, r)
}

func (d *Dashboard) deleteGoal(w http.ResponseWriter, r *http.Request) {
	metricName := strings.TrimSpace(r.URL.Query().Get("metric"))
	if metricName == "" {
		WriteError(w, http.StatusBadRequest, "metric 不能为空")
		return
	}
	if err := d.db.DeleteGoal(r.Context(), metricName); err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.listGoals(w, r)
}

// goalItem 是对外暴露的一条目标（带字典里的中文名与单位）。
type goalItem struct {
	Metric      string    `json:"metric"`
	DisplayName string    `json:"display_name"`
	Unit        string    `json:"unit"`
	Target      float64   `json:"target"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (d *Dashboard) listMetrics(w http.ResponseWriter, r *http.Request) {
	defs := d.metric.All()
	type item struct {
		metric.Def
		DisplayName string `json:"display_name"`
	}
	out := make([]item, 0, len(defs))
	for _, def := range defs {
		out = append(out, item{Def: def, DisplayName: def.DisplayNameZH})
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"timezone": d.tzName,
		"metrics":  out,
	})
}

func (d *Dashboard) summary(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().In(d.loc).Format("2006-01-02")
	}
	only := splitList(r.URL.Query().Get("metrics"))
	filter := map[string]struct{}{}
	for _, m := range only {
		filter[m] = struct{}{}
	}
	items, err := d.db.Summary(r.Context(), date, filter)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	for i := range items {
		if def, ok := d.metric.Get(items[i].Metric); ok {
			items[i].DisplayName = def.DisplayNameZH
			items[i].Unit = def.PreferredUnit
		}
	}
	today := time.Now().In(d.loc).Format("2006-01-02")
	WriteJSON(w, http.StatusOK, map[string]any{
		"date":     date,
		"today":    today,
		"timezone": d.tzName,
		"items":    items,
	})
}

func (d *Dashboard) daily(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	metrics := d.resolveMetrics(q.Get("metrics"))
	from, to := d.defaultRange(q.Get("from"), q.Get("to"), 30)
	if len(metrics) == 0 {
		WriteError(w, http.StatusBadRequest, "没有匹配的指标")
		return
	}
	points, err := d.db.DailySeries(r.Context(), metrics, from, to)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"from": from, "to": to, "metrics": metrics, "timezone": d.tzName,
		"series": groupByMetric(points, metrics),
	})
}

// baseline 返回「最新值 vs 我的常态」：常态是对比日之前窗口内天级值的中位数。
//
// date 缺省取服务所在时区的今天；window 缺省 30 天。今天还没有数据时，
// 对比日自动回落到最后有数据的那天，并把 stale 置为 true——前端据此标注
// 「显示的是 X 月 X 日」，避免把三天前的数据当今天的看。
func (d *Dashboard) baseline(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	metrics := d.resolveMetrics(q.Get("metrics"))
	if len(metrics) == 0 {
		WriteError(w, http.StatusBadRequest, "没有匹配的指标")
		return
	}
	date := strings.TrimSpace(q.Get("date"))
	if date == "" {
		date = time.Now().In(d.loc).Format("2006-01-02")
	}
	window, _ := strconv.Atoi(q.Get("window"))
	items, err := d.db.Baseline(r.Context(), metrics, date, window)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"date": date, "window": window, "timezone": d.tzName, "items": items,
	})
}

// insights 返回偏离常态最明显的若干条提示。
func (d *Dashboard) insights(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	metrics := d.resolveMetrics(q.Get("metrics"))
	if len(metrics) == 0 {
		WriteError(w, http.StatusBadRequest, "没有匹配的指标")
		return
	}
	date := strings.TrimSpace(q.Get("date"))
	if date == "" {
		date = time.Now().In(d.loc).Format("2006-01-02")
	}
	window, _ := strconv.Atoi(q.Get("window"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, err := d.db.Insights(r.Context(), metrics, date, window, limit)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"date": date, "window": window, "timezone": d.tzName, "items": items,
	})
}

// coverage 返回窗口内各指标的数据覆盖情况（有多少天有数据、最后更新于哪天）。
func (d *Dashboard) coverage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	date := strings.TrimSpace(q.Get("date"))
	if date == "" {
		date = time.Now().In(d.loc).Format("2006-01-02")
	}
	days, _ := strconv.Atoi(q.Get("days"))
	cov, err := d.db.CoverageOf(r.Context(), date, days)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, cov)
}

func (d *Dashboard) samples(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	m := q.Get("metric")
	if m == "" || !d.metric.IsKnown(m) {
		WriteError(w, http.StatusBadRequest, "指标缺失或未登记")
		return
	}
	from, to := d.defaultRange(q.Get("from"), q.Get("to"), 7)
	limit, _ := strconv.Atoi(q.Get("limit"))
	rows, err := d.db.Samples(r.Context(), m, from, to, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"metric": m, "from": from, "to": to, "rows": rows})
}

func (d *Dashboard) sleep(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := d.defaultRange(q.Get("from"), q.Get("to"), 30)
	rows, err := d.db.SleepRange(r.Context(), from, to)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "rows": rows})
}

// sleepStages 返回某一晚的分期时间线（hypnogram）。
// date 缺省取「最近一晚有分期的睡眠日」，这样页面首次进来即有内容可画。
func (d *Dashboard) sleepStages(w http.ResponseWriter, r *http.Request) {
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		latest, err := d.db.LatestSleepStageDate(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if latest == "" {
			// 完全没有分期数据：返回零值而不是 404，前端据此显示占位提示。
			WriteJSON(w, http.StatusOK, &postgres.SleepStages{
				Segments:   []postgres.SleepStageSeg{},
				Totals:     map[string]float64{},
				Physiology: []postgres.NightSeries{},
			})
			return
		}
		date = latest
	}
	res, err := d.db.SleepStages(r.Context(), date)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

// sleepNightMetrics 返回区间内每一晚「入睡时间窗」内的生理指标聚合（夜间心率等）。
//
// 关联口径与 awake_count、分期时间线一致：按入睡窗口取样本，不按 local_date。
// 一晚一指标一行，只在窗口内确有样本时出现。
func (d *Dashboard) sleepNightMetrics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := d.defaultRange(q.Get("from"), q.Get("to"), 30)
	rows, err := d.db.SleepNightMetrics(r.Context(), from, to, splitList(q.Get("metrics")))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "rows": rows})
}

// sleepStagesRange 返回区间内每晚的分期序列（睡眠时间表矩阵）。
// 矩阵最多画最近几十个夜晚，再多就看不出单晚了，所以服务端把窗口截到 120 天。
func (d *Dashboard) sleepStagesRange(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := d.defaultRange(q.Get("from"), q.Get("to"), 30)
	if fd, err := time.Parse("2006-01-02", from); err == nil {
		if td, err2 := time.Parse("2006-01-02", to); err2 == nil && td.Sub(fd) > 120*24*time.Hour {
			from = td.AddDate(0, 0, -119).Format("2006-01-02")
		}
	}
	rows, err := d.db.SleepStagesRange(r.Context(), from, to)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "rows": rows})
}

func (d *Dashboard) workouts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := d.defaultRange(q.Get("from"), q.Get("to"), 30)
	rows, err := d.db.WorkoutRange(r.Context(), from, to)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "rows": rows})
}

// workoutSeries 返回一条运动的逐点心率、运动后恢复心率、分段原文与轨迹摘要。
//
// 单独一条端点而不是塞进 /api/workouts：曲线是数组，列表页一次要读几十条运动，
// 把曲线跟着列表返回会让列表查询付出它根本用不到的开销（与心电波形分表同一理由）。
// 轨迹只回「有没有 + 点数」这种摘要，折线本体留给真正要画地图的那一次请求。
func (d *Dashboard) workoutSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusBadRequest, "id 缺失或非法")
		return
	}
	s, err := d.db.WorkoutSeriesFor(r.Context(), id)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s == nil {
		WriteError(w, http.StatusNotFound, "运动记录不存在")
		return
	}
	WriteJSON(w, http.StatusOK, s)
}

// workoutRoute 返回一条运动的轨迹折线（本机 GPX 导入的那份数据）。
//
// 没有轨迹回 404 而不是空数组：界面上要能分清「这次运动没导轨迹」与
// 「导了但一个点都没有」，前者是常态（只有带路线的记录才有 GPX）。
func (d *Dashboard) workoutRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusBadRequest, "id 缺失或非法")
		return
	}
	route, err := d.db.RouteFor(r.Context(), id)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if route == nil {
		WriteError(w, http.StatusNotFound, "这条运动没有导入轨迹")
		return
	}
	WriteJSON(w, http.StatusOK, route)
}

func (d *Dashboard) sources(w http.ResponseWriter, r *http.Request) {
	rows, err := d.db.Sources(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"sources": rows})
}

func (d *Dashboard) syncStatus(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 30
	}
	st, err := d.db.SyncStatusOf(r.Context(), days)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	st.HomeTZ = d.tzName
	WriteJSON(w, http.StatusOK, st)
}

func (d *Dashboard) resolveMetrics(raw string) []string {
	want := splitList(raw)
	if len(want) == 0 {
		for _, def := range d.metric.Enabled() {
			want = append(want, def.Metric)
		}
		return want
	}
	out := make([]string, 0, len(want))
	for _, m := range want {
		if d.metric.IsKnown(m) {
			out = append(out, m)
		}
	}
	return out
}

func (d *Dashboard) defaultRange(from, to string, days int) (string, string) {
	if from == "" {
		from = time.Now().In(d.loc).AddDate(0, 0, -days+1).Format("2006-01-02")
	}
	if to == "" {
		to = time.Now().In(d.loc).Format("2006-01-02")
	}
	return from, to
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func groupByMetric(points []postgres.DailyPoint, order []string) map[string][]postgres.DailyPoint {
	out := make(map[string][]postgres.DailyPoint, len(order))
	for _, m := range order {
		out[m] = []postgres.DailyPoint{}
	}
	for _, p := range points {
		out[p.Metric] = append(out[p.Metric], p)
	}
	return out
}
