package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrUnknownGoalMetric 是「指标不在字典里」：目标只能给已登记的指标设，
// 否则页面上永远不会有对应的线，写了也是死数据。
var ErrUnknownGoalMetric = errors.New("指标未登记，不能设目标")

// GoalRow 是一条目标。
type GoalRow struct {
	Metric    string    `json:"metric"`
	Target    float64   `json:"target"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Goals 返回全部目标（按指标名升序）。没有目标时是空数组而不是 nil。
func (db *DB) Goals(ctx context.Context) ([]GoalRow, error) {
	rows, err := db.Pool.Query(ctx, `SELECT metric, target, updated_at FROM goals ORDER BY metric`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]GoalRow, 0, 8)
	for rows.Next() {
		var g GoalRow
		if err := rows.Scan(&g.Metric, &g.Target, &g.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetGoal 写入或更新某个指标的目标。known 由调用方给（指标字典的判定留在 api 层，
// store 不依赖字典，避免存储层反向依赖领域注册表）。
func (db *DB) SetGoal(ctx context.Context, metric string, target float64, known bool) error {
	if !known {
		return ErrUnknownGoalMetric
	}
	if target <= 0 {
		return errors.New("目标必须大于 0")
	}
	_, err := db.Pool.Exec(ctx, `INSERT INTO goals (metric, target, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (metric) DO UPDATE SET target = EXCLUDED.target, updated_at = now()`,
		metric, target)
	return err
}

// DeleteGoal 去掉某个指标的目标；不存在时不报错（幂等）。
func (db *DB) DeleteGoal(ctx context.Context, metric string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM goals WHERE metric = $1`, metric)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}
