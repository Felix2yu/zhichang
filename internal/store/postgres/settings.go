package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrUnknownSettingKey 是「偏好项不在登记清单里」。
// 与目标同理会白名单校验：不限键名就等于把这张表当成任意 KV 存储，
// 以后没人知道哪些键真的有人读。
var ErrUnknownSettingKey = errors.New("设置项未登记，不能保存")

// AppSetting 是一条应用级偏好。value 一律存文本，语义由键名决定。
type AppSetting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AppSettings 返回全部偏好（按键名升序）。没有时是空数组而不是 nil。
func (db *DB) AppSettings(ctx context.Context) ([]AppSetting, error) {
	rows, err := db.Pool.Query(ctx, `SELECT key, value, updated_at FROM app_settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AppSetting, 0, 8)
	for rows.Next() {
		var s AppSetting
		if err := rows.Scan(&s.Key, &s.Value, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetAppSetting 写入或更新一条偏好。known 由调用方给（登记与取值校验留在 api 层）。
func (db *DB) SetAppSetting(ctx context.Context, key, value string, known bool) error {
	if !known {
		return ErrUnknownSettingKey
	}
	_, err := db.Pool.Exec(ctx, `INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		key, value)
	return err
}

// DeleteAppSetting 去掉一条偏好；不存在时不报错（幂等）。
func (db *DB) DeleteAppSetting(ctx context.Context, key string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM app_settings WHERE key = $1`, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}
