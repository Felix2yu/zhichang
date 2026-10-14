package api

import (
	"net/http"

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
