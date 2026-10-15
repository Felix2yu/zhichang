package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// 应用级偏好的登记清单：键名 → 取值校验。
//
// 白名单是必须的——不限键名就等于把这张表当成任意 KV 存储，
// 以后没人知道哪些键真的有人读。加新偏好只动这张表，别让校验散落进 handler。
var settingDefs = map[string]func(string) error{
	// 预期入睡时间，HH:MM 的 24 小时制墙钟时刻
	"bedtime_target": validateClock,
}

// errBadClock 的文案会原样出现在界面上，所以它要说清「该写成什么样」。
var errBadClock = errors.New("入睡时间要写成 HH:MM，比如 22:30")

// validateClock 校验 HH:MM。故意不用 time.Parse：
// 它对 "9:30" 这类一位数小时同样宽容，而前端要的是定长两位。
func validateClock(v string) error {
	parts := strings.Split(v, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return errBadClock
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return errBadClock
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return errBadClock
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return errBadClock
	}
	return nil
}

// settingItem 是一条偏好的响应体；结构与 goals 对齐，前端好共用同一套渲染。
type settingItem struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (d *Dashboard) listSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := d.db.AppSettings(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]settingItem, 0, len(rows))
	for _, s := range rows {
		out = append(out, settingItem{Key: s.Key, Value: s.Value, UpdatedAt: s.UpdatedAt})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"settings": out})
}

func (d *Dashboard) putSetting(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	// 偏好体本来就几十字节，不设上限等于把内存交给带令牌就能访问的口子
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		WriteError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	key := strings.TrimSpace(in.Key)
	validate, known := settingDefs[key]
	if !known {
		WriteError(w, http.StatusBadRequest, postgres.ErrUnknownSettingKey.Error())
		return
	}
	value := strings.TrimSpace(in.Value)
	if err := validate(value); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := d.db.SetAppSetting(r.Context(), key, value, true); err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.listSettings(w, r)
}

func (d *Dashboard) deleteSetting(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		WriteError(w, http.StatusBadRequest, "key 不能为空")
		return
	}
	if err := d.db.DeleteAppSetting(r.Context(), key); err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.listSettings(w, r)
}
