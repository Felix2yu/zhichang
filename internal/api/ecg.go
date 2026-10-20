package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Felix2yu/zhichang/internal/domain/ecg"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// ecgRecordItem 是对外暴露的一条心电记录。
//
// ClassificationLabel 是归一后的统一措辞，而不是上游原文——原文随手机语言变化
// （简中「窦性心律」/ 英文 "Sinus Rhythm"），直接显示会让同一份数据在不同语言下
// 长得不一样。只有码认不出时才回落展示原文，并用 ClassificationKnown=false
// 让前端知道「这是上游原话，不是我们的归一结论」。
type ecgRecordItem struct {
	postgres.ECGRecordRow
	ClassificationLabel string `json:"classification_label"`
	ClassificationKnown bool   `json:"classification_known"`
}

// ecgRecords 列出区间内的心电记录（不含波形）。
//
// 默认区间取 90 天而不是别处的 30：心电是低频事件（实测半个月 9 条，
// 且都集中在夜里），按 30 天给会让列表时不时只剩一两条，看不出任何趋势。
func (d *Dashboard) ecgRecords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := d.defaultRange(q.Get("from"), q.Get("to"), 90)
	rows, err := d.db.ECGRange(r.Context(), from, to)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	out := make([]ecgRecordItem, 0, len(rows))
	for _, row := range rows {
		label := ecg.DisplayZH(ecg.Code(row.ClassificationCode))
		known := label != ""
		if !known {
			label = row.Classification
		}
		out = append(out, ecgRecordItem{ECGRecordRow: row, ClassificationLabel: label, ClassificationKnown: known})
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"from": from, "to": to, "timezone": d.tzName, "records": out,
	})
}

// ecgWaveform 返回一条记录的电压波形。
//
// 单列一条接口而不是塞进列表里：一次测量 15360 个点（约 120KB），
// 而列表页只是给人扫一眼「哪天测了、结论是什么」。
//
// 顺带算出 R 峰下标一起返回：前端要在波形上标出每次心跳，
// 而检测必须用与统计分析完全相同的实现（domain/ecg）——
// 若前端自己检一遍，图上的标记与卡片里的指数就会是两套算法。
func (d *Dashboard) ecgWaveform(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusBadRequest, "id 缺失或非法")
		return
	}
	wf, err := d.db.ECGVoltages(r.Context(), id)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if wf == nil {
		WriteError(w, http.StatusNotFound, "心电图记录不存在")
		return
	}
	if wf.SamplingFrequency != nil && *wf.SamplingFrequency > 0 && len(wf.Voltages) > 0 {
		wf.Peaks = ecg.DetectRPeaks(wf.Voltages, float64(*wf.SamplingFrequency)).Peaks
	}
	if wf.Peaks == nil {
		// nil 会被编成 JSON null，前端 .length 直接抛 TypeError
		wf.Peaks = []int{}
	}
	WriteJSON(w, http.StatusOK, wf)
}
