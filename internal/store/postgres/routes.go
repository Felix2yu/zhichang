package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Felix2yu/zhichang/internal/gpx"
)

// MatchWorkoutByWall 按「运动名 + 本地起始钟点」找轨迹该挂到哪条运动记录。
//
// 钟点比较放在 SQL 里：workouts 存的是 UTC 的 start_time 加记录自带偏移，
// 而 HAE 的文件名只给本地钟点、不给偏移。在 Go 侧换算等于再引入一个时区假设，
// 用同一个表达式把两边拉到一把尺子上才是对的。
//
// 必须先 `AT TIME ZONE 'UTC'` 再加偏移：to_char 作用于 timestamptz 时按**会话时区**
// 渲染，直接加偏移得到的字符串会跟着数据库的 TimeZone 漂（测试库是 +08 时，
// 同一条记录会算出两个不同的钟点，匹配静默失败、报告里只显示「未匹配」）。
//
// 返回多个 id 不是错误：同名同时刻的两条记录只在来源不同时存在（唯一键是
// name+source+start_time），调用方必须把它当歧义处理，不能挑一条写。
func (db *DB) MatchWorkoutByWall(ctx context.Context, name, wall string) ([]int64, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id FROM workouts
		 WHERE name = $1
		   AND to_char((start_time AT TIME ZONE 'UTC') + make_interval(mins => tz_offset_min),
		               'YYYYMMDDHH24MISS') = $2
		 ORDER BY id`, name, wall)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// WriteRoute 写入一条轨迹，返回是否真的写了（false = 指纹相同、内容没变）。
//
// 幂等靠 sha256 而不是「先查后插」：DO UPDATE 的 WHERE 让同指纹的重放直接空转，
// 一次往返就够，也不用担心两个进程同时导入同一目录。
func (db *DB) WriteRoute(ctx context.Context, workoutID int64, r *gpx.Route, sha string) (bool, error) {
	latMin, latMax := minMax(r.Lats)
	lonMin, lonMax := minMax(r.Lons)
	eleMin, eleMax := minMax(r.Eles)
	tag, err := db.Pool.Exec(ctx, `INSERT INTO workout_routes
		(workout_id, file_name, sha256, point_count, start_time, end_time,
		 lat_min, lat_max, lon_min, lon_max, elev_min, elev_max,
		 lats, lons, eles, at_secs)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (workout_id) DO UPDATE SET
			file_name = EXCLUDED.file_name,
			sha256 = EXCLUDED.sha256,
			point_count = EXCLUDED.point_count,
			start_time = EXCLUDED.start_time,
			end_time = EXCLUDED.end_time,
			lat_min = EXCLUDED.lat_min, lat_max = EXCLUDED.lat_max,
			lon_min = EXCLUDED.lon_min, lon_max = EXCLUDED.lon_max,
			elev_min = EXCLUDED.elev_min, elev_max = EXCLUDED.elev_max,
			lats = EXCLUDED.lats, lons = EXCLUDED.lons,
			eles = EXCLUDED.eles, at_secs = EXCLUDED.at_secs,
			ingested_at = now()
		WHERE workout_routes.sha256 IS DISTINCT FROM EXCLUDED.sha256`,
		workoutID, r.FileName, sha, r.Count(), r.StartTime.UTC(), r.EndTime.UTC(),
		latMin, latMax, lonMin, lonMax, eleMin, eleMax,
		r.Lats, r.Lons, r.Eles, r.AtSec)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RouteFingerprint 返回某条运动已入库轨迹的指纹（不存在时为空串）。
// 供导入报告区分「新写入 / 内容没变 / 首次写入」。
//
// 空表是这条路径的常态（第一次导入时谁都还没有），所以「查不到」不是错误。
// 注意哨兵是 pgx 的那一个：池子是 pgx 的，返回 database/sql 的 ErrNoRows
// 会让第一次导入在第一个文件上就整体失败。
func (db *DB) RouteFingerprint(ctx context.Context, workoutID int64) (string, error) {
	var sha string
	err := db.Pool.QueryRow(ctx, `SELECT sha256 FROM workout_routes WHERE workout_id = $1`, workoutID).Scan(&sha)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return sha, nil
}

func minMax(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	lo, hi := xs[0], xs[0]
	for _, x := range xs[1:] {
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
	}
	return lo, hi
}
