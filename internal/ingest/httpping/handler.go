// Package httpping 提供 Health Auto Export 的 REST 接入端点。
package httpping

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Felix2yu/zhichang/internal/api"
	"github.com/Felix2yu/zhichang/internal/ingest/hae"
	"github.com/Felix2yu/zhichang/internal/ingest/pipeline"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// Handler 处理 HAE 的 POST 请求。
type Handler struct {
	svc       *pipeline.Service
	token     string
	maxBytes  int64
	archiveTo string
}

// New 构造接入处理器。
func New(svc *pipeline.Service, token string, maxBytes int64, archiveDir string) *Handler {
	if maxBytes <= 0 {
		maxBytes = 512 << 20
	}
	return &Handler{svc: svc, token: token, maxBytes: maxBytes, archiveTo: archiveDir}
}

// Register 挂载路由。
func (h *Handler) Register(r chi.Router) {
	r.Post("/api/ingest/hae", h.handle)
}

type response struct {
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
	UnknownMetrics []string `json:"unknown_metrics,omitempty"`
	Affected       struct {
		From string `json:"from,omitempty"`
		To   string `json:"to,omitempty"`
	} `json:"affected"`
	Message string `json:"message,omitempty"`
}

func (h *Handler) handle(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		api.WriteError(w, http.StatusUnauthorized, "token 无效")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, h.maxBytes+1))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "读取请求体失败")
		return
	}
	if int64(len(body)) > h.maxBytes {
		api.WriteError(w, http.StatusRequestEntityTooLarge, "请求体超过上限")
		return
	}

	sum := sha256.Sum256(body)
	meta := postgres.RunMeta{
		Transport:      "rest",
		AutomationID:   r.Header.Get("automation-id"),
		AutomationName: r.Header.Get("automation-name"),
		SessionID:      r.Header.Get("session-id"),
		Period:         r.Header.Get("automation-period"),
		Aggregation:    r.Header.Get("automation-aggregation"),
		PayloadSHA256:  hex.EncodeToString(sum[:]),
		PayloadBytes:   int64(len(body)),
	}
	if h.archiveTo != "" {
		if p, err := archive(h.archiveTo, meta, body); err != nil {
			slog.Warn("payload 归档失败", "error", err)
		} else {
			meta.ArchivedPath = p
		}
	}

	payload, err := hae.ParsePayload(bytes.NewReader(body))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	var resp response
	if payload.IsEmpty() {
		resp.Status = "empty"
		resp.Message = "本次导出没有数据"
		api.WriteJSON(w, http.StatusOK, resp)
		return
	}

	out, err := h.svc.Ingest(r.Context(), payload, meta)
	if err != nil {
		slog.Error("摄入失败", "error", err, "session", meta.SessionID, "automation", meta.AutomationID)
		api.WriteError(w, http.StatusInternalServerError, "入库失败: "+err.Error())
		return
	}

	resp.RunID = out.RunID
	resp.Status = "ok"
	if out.DuplicateRun {
		resp.Status = "duplicate"
		resp.Message = "该 session 已处理过，返回上次结果"
	}
	resp.Received, resp.Inserted = out.Received, out.Inserted
	resp.Duplicates, resp.Updated, resp.Workouts, resp.Sleep = out.Duplicates, out.Updated, out.Workouts, out.Sleep
	resp.ECG = out.ECG
	resp.Skipped, resp.DuplicateRun, resp.UnknownMetrics = out.Skipped, out.DuplicateRun, out.UnknownMetrics
	resp.Affected.From, resp.Affected.To = out.AffectedFrom, out.AffectedTo
	api.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) authorized(r *http.Request) bool {
	if h.token == "" {
		return false
	}
	given := r.Header.Get("X-Zhichang-Token")
	if given == "" {
		given = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(h.token)) == 1
}

func archive(dir string, meta postgres.RunMeta, body []byte) (string, error) {
	day := time.Now().Format("2006-01-02")
	target := filepath.Join(dir, day)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	name := meta.SessionID
	if name == "" {
		name = fmt.Sprintf("%d-%s", time.Now().UnixNano(), meta.PayloadSHA256[:12])
	}
	path := filepath.Join(target, name+".json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
