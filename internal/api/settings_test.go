package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// 偏好写接口与目标同为带令牌的口子：脏输入必须全部挡在 400，
// 否则界面读到一条画不出来的脏值，用户除了重设 nothing 能做。
func TestPutSetting(t *testing.T) {
	const token = "s3cr3t"

	t.Run("未配令牌即只读", func(t *testing.T) {
		r, _ := newRouter(t, "")
		putJSON(t, r, "/api/settings", `{"key":"bedtime_target","value":"22:30"}`, token).
			wantErr(t, http.StatusServiceUnavailable)
		deleteWith(t, r, "/api/settings?key=bedtime_target", token).
			wantErr(t, http.StatusServiceUnavailable)
	})

	t.Run("令牌错误", func(t *testing.T) {
		r, _ := newRouter(t, token)
		putJSON(t, r, "/api/settings", `{"key":"bedtime_target","value":"22:30"}`, "wrong").
			wantErr(t, http.StatusUnauthorized)
		if len(get(t, r, "/api/settings").arr(t, "settings")) != 0 {
			t.Fatal("被拒的写入不能落库")
		}
	})

	t.Run("写入成功后回读整表", func(t *testing.T) {
		r, _ := newRouter(t, token)
		got := putJSON(t, r, "/api/settings", `{"key":"bedtime_target","value":"22:30"}`, token)
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		items := got.arr(t, "settings")
		if len(items) != 1 {
			t.Fatalf("PUT 后应回读整表: %s", got.body)
		}
		item := items[0].(map[string]any)
		if item["key"] != "bedtime_target" || item["value"] != "22:30" {
			t.Fatalf("写入值不符: %v", item)
		}
		if s, ok := item["updated_at"].(string); !ok || s == "" {
			t.Fatalf("updated_at 应可读: %v", item)
		}

		// 覆盖写：同一键不能攒出两行
		putJSON(t, r, "/api/settings", `{"key":"bedtime_target","value":"23:15"}`, token)
		after := get(t, r, "/api/settings").arr(t, "settings")
		if len(after) != 1 || after[0].(map[string]any)["value"] != "23:15" {
			t.Fatalf("重复设置应覆盖: %v", after)
		}
	})

	t.Run("脏输入一律 400", func(t *testing.T) {
		r, _ := newRouter(t, token)
		cases := []struct {
			name string
			body string
			msg  string
		}{
			{"非法 JSON", `{"key":`, "请求体不是合法 JSON"},
			{"不是对象", `[1,2]`, "请求体不是合法 JSON"},
			{"空体", `{}`, postgres.ErrUnknownSettingKey.Error()},
			{"键名全是空白", `{"key":"  ","value":"22:30"}`, postgres.ErrUnknownSettingKey.Error()},
			{"未登记键名", `{"key":"wake_target","value":"07:00"}`, postgres.ErrUnknownSettingKey.Error()},
			{"一位小时", `{"key":"bedtime_target","value":"9:30"}`, "HH:MM"},
			{"缺分钟", `{"key":"bedtime_target","value":"22"}`, "HH:MM"},
			{"秒", `{"key":"bedtime_target","value":"22:30:00"}`, "HH:MM"},
			{"小时越界", `{"key":"bedtime_target","value":"24:00"}`, "HH:MM"},
			{"分钟越界", `{"key":"bedtime_target","value":"22:60"}`, "HH:MM"},
			{"非数字", `{"key":"bedtime_target","value":"ab:cd"}`, "HH:MM"},
			{"空值", `{"key":"bedtime_target","value":""}`, "HH:MM"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				got := putJSON(t, r, "/api/settings", c.body, token)
				got.wantErr(t, http.StatusBadRequest)
				if !strings.Contains(got.str(t, "error"), c.msg) {
					t.Fatalf("错误文案应含 %q，实际 %q", c.msg, got.str(t, "error"))
				}
			})
		}
		if len(get(t, r, "/api/settings").arr(t, "settings")) != 0 {
			t.Fatal("被拒的请求不应落库")
		}
	})

	t.Run("超过 4096 字节的请求体被拒", func(t *testing.T) {
		r, _ := newRouter(t, token)
		large := `{"key":"bedtime_target","value":"` + strings.Repeat("x", 5000) + `"}`
		putJSON(t, r, "/api/settings", large, token).wantErr(t, http.StatusBadRequest)
	})
}

func TestDeleteSetting(t *testing.T) {
	const token = "s3cr3t"
	r, db := newRouter(t, token)
	if err := db.SetAppSetting(context.Background(), "bedtime_target", "22:30", true); err != nil {
		t.Fatalf("预置偏好失败: %v", err)
	}

	t.Run("缺 key 参数", func(t *testing.T) {
		deleteWith(t, r, "/api/settings", token).wantErr(t, http.StatusBadRequest)
		deleteWith(t, r, "/api/settings?key=%20%20", token).wantErr(t, http.StatusBadRequest)
	})

	t.Run("删除后回读整表", func(t *testing.T) {
		got := deleteWith(t, r, "/api/settings?key=bedtime_target", token)
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		if len(got.arr(t, "settings")) != 0 {
			t.Fatalf("删完应为空数组而不是 null: %s", got.body)
		}
	})

	t.Run("删不存在的键是幂等的", func(t *testing.T) {
		got := deleteWith(t, r, "/api/settings?key=bedtime_target", token)
		if got.status != http.StatusOK || len(got.arr(t, "settings")) != 0 {
			t.Fatalf("删不存在的偏好应 200 并回空表: %d %s", got.status, got.body)
		}
	})

	t.Run("存储层报错时 500", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		call(t, r, http.MethodDelete, "/api/settings?key=bedtime_target", nil,
			map[string]string{"Authorization": "Bearer " + token}, ctx).
			wantErr(t, http.StatusInternalServerError)
	})
}

// 空库是生产刚部署完的必然状态：只读接口必须回 []，不能回 null。
func TestListSettingsEmpty(t *testing.T) {
	r, _ := newRouter(t, "")
	got := get(t, r, "/api/settings")
	if got.status != http.StatusOK {
		t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
	}
	if len(got.arr(t, "settings")) != 0 {
		t.Fatalf("空库应回空数组: %s", got.body)
	}
}
