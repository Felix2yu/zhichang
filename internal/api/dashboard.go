package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// Dashboard 提供仪表盘只读查询接口。
type Dashboard struct {
	db     *postgres.DB
	metric *metric.Registry
	loc    *time.Location
	tzName string
}

// NewDashboard 构造仪表盘 API。
func NewDashboard(db *postgres.DB, reg *metric.Registry, loc *time.Location, tzName string) *Dashboard {
	return &Dashboard{db: db, metric: reg, loc: loc, tzName: tzName}
}

// Register 挂载只读查询路由。
func (d *Dashboard) Register(r chi.Router) {
	r.Get("/api/metrics", d.listMetrics)
	r.Get("/api/summary", d.summary)
	r.Get("/api/daily", d.daily)
	r.Get("/api/samples", d.samples)
	r.Get("/api/sleep", d.sleep)
	r.Get("/api/sleep/stages", d.sleepStages)
	r.Get("/api/workouts", d.workouts)
	r.Get("/api/sources", d.sources)
	r.Get("/api/sync/status", d.syncStatus)
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
				Segments: []postgres.SleepStageSeg{},
				Totals:   map[string]float64{},
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
