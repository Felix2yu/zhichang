package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Felix2yu/zhichang/internal/domain/sample"
)

// WriteECG 写入心电记录与波形，返回新增的记录条数。
//
// 幂等分两层：记录按 (start_time, source) 去重，波形按 ecg_id 去重。
// 记录已存在时**照样尝试写波形**——这样「先只导出了记录、之后波形才到」
// 或者早期版本没存波形的历史行，都能被后续导出补齐，而不是永远缺一张图。
//
// 记录与波形分开两条语句，是因为波形可能整段缺失（见 collectVoltages）：
// 判定是这条记录最有价值的部分，波形没有也该把记录留下。
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
	const selectRecord = `SELECT id FROM ecg_records WHERE start_time = $1 AND source = $2`
	const insertVoltages = `INSERT INTO ecg_voltages (ecg_id, unit, voltages) VALUES ($1,$2,$3)
		ON CONFLICT (ecg_id) DO NOTHING`

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
			// 已存在：回查 id 才能把波形补上。ON CONFLICT DO NOTHING 不返回行，
			// 所以这里必须再查一次。
			if err := db.Pool.QueryRow(ctx, selectRecord, r.StartTime.UTC(), r.Source).Scan(&id); err != nil {
				return inserted, err
			}
		default:
			return inserted, err
		}
		if len(r.Voltages) == 0 {
			continue
		}
		if _, err := db.Pool.Exec(ctx, insertVoltages, id, r.VoltageUnit, r.Voltages); err != nil {
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

// ECGWaveform 是一条记录的电压波形。
type ECGWaveform struct {
	ID                int64  `json:"id"`
	Start             string `json:"start"`
	Unit              string `json:"unit"`
	SamplingFrequency *int   `json:"sampling_frequency,omitempty"`
	// DurationSec 由点数与采样率推出（第 i 点即 start + i/fs），
	// 也就是这条波形在图上真正铺开的时间跨度。
	DurationSec float64   `json:"duration_sec"`
	Voltages    []float64 `json:"voltages"`
}

// ECGVoltages 读取一条记录的波形。记录不存在时返回 nil, nil；
// 记录存在但没存波形时返回一个 Voltages 为空的波形——两者对上层是不同的事
// （前者 404，后者是「这条记录上游没给波形」），所以用 LEFT JOIN 而不是内连接。
func (db *DB) ECGVoltages(ctx context.Context, id int64) (*ECGWaveform, error) {
	var out ECGWaveform
	var st time.Time
	err := db.Pool.QueryRow(ctx, `SELECT r.id, r.start_time, r.sampling_frequency,
		COALESCE(v.unit, ''), v.voltages
		FROM ecg_records r LEFT JOIN ecg_voltages v ON v.ecg_id = r.id
		WHERE r.id = $1`, id).
		Scan(&out.ID, &st, &out.SamplingFrequency, &out.Unit, &out.Voltages)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out.Start = st.UTC().Format(time.RFC3339)
	if out.Voltages == nil {
		// nil slice 会被编成 JSON null，前端 .length 直接抛 TypeError。
		out.Voltages = []float64{}
	}
	if out.SamplingFrequency != nil && *out.SamplingFrequency > 0 {
		out.DurationSec = float64(len(out.Voltages)) / float64(*out.SamplingFrequency)
	}
	return &out, nil
}
