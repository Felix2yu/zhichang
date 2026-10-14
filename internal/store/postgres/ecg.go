package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// WriteECG 写入心电记录，返回新增的记录条数。
//
// 幂等锚点是 (start_time, source)：同来源同起始秒即同一次测量，重复提交同一份导出
// 时靠 ON CONFLICT DO NOTHING 去重，返回值天然是本次真正新增的条数
// （与 samples 的「duplicate run 回放 0」语义一致）。
func (db *DB) WriteECG(ctx context.Context, runID int64, list []sample.ECGRecord) (int, error) {
	if len(list) == 0 {
		return 0, nil
	}
	const insertRecord = `INSERT INTO ecg_records
		(start_time, end_time, classification, classification_code, average_heart_rate,
		 sampling_frequency, voltage_count, source, local_date, tz_offset_min, ingest_run_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (start_time, source) DO NOTHING
		RETURNING id`

	inserted := 0
	for _, r := range list {
		var id int64
		err := db.Pool.QueryRow(ctx, insertRecord,
			r.StartTime.UTC(), r.EndTime.UTC(), r.Classification, string(r.ClassificationCode),
			r.AverageHeartRate, r.SamplingFrequency, r.VoltageCount, r.Source, r.LocalDate,
			r.TZOffsetMin, runID,
		).Scan(&id)
		switch {
		case err == nil:
			inserted++
		case errors.Is(err, pgx.ErrNoRows):
			// 命中唯一约束，说明这条测量已经在了，跳过。
		default:
			return inserted, err
		}
	}
	return inserted, nil
}

// ECGRecordRow 是心电记录列表的一行。刻意不含波形：一次测量 15360 个点，
// 列表页没有画它们的地方，带上只会把响应撑到几百 KB。
type ECGRecordRow struct {
	ID                 int64    `json:"id"`
	LocalDate          string   `json:"local_date"`
	Start              string   `json:"start"`
	End                string   `json:"end"`
	DurationSec        float64  `json:"duration_sec"`
	Classification     string   `json:"classification"`
	ClassificationCode string   `json:"classification_code"`
	AverageHeartRate   *float64 `json:"average_heart_rate,omitempty"`
	SamplingFrequency  *int     `json:"sampling_frequency,omitempty"`
	VoltageCount       int      `json:"voltage_count"`
	Source             string   `json:"source"`
	TZOffsetMin        int      `json:"tz_offset_min"`
}

// ECGRange 读取区间内的心电记录（按 local_date 过滤，与运动/睡眠同口径）。
func (db *DB) ECGRange(ctx context.Context, from, to string) ([]ECGRecordRow, error) {
	fd, err := parseDate(from)
	if err != nil {
		return nil, err
	}
	td, err := parseDate(to)
	if err != nil {
		return nil, err
	}
	rows, err := db.Pool.Query(ctx, `SELECT id, local_date, start_time, end_time,
		EXTRACT(EPOCH FROM (end_time - start_time))::float8,
		classification, classification_code, average_heart_rate, sampling_frequency,
		voltage_count, source, tz_offset_min
		FROM ecg_records WHERE local_date BETWEEN $1 AND $2 ORDER BY start_time DESC`, fd, td)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ECGRecordRow, 0, 16)
	for rows.Next() {
		var r ECGRecordRow
		var ld pgtype.Date
		var st, et time.Time
		if err := rows.Scan(&r.ID, &ld, &st, &et, &r.DurationSec, &r.Classification,
			&r.ClassificationCode, &r.AverageHeartRate, &r.SamplingFrequency,
			&r.VoltageCount, &r.Source, &r.TZOffsetMin); err != nil {
			return nil, err
		}
		r.LocalDate = ld.Time.Format("2006-01-02")
		r.Start = st.UTC().Format(time.RFC3339)
		r.End = et.UTC().Format(time.RFC3339)
		out = append(out, r)
	}
	return out, rows.Err()
}
