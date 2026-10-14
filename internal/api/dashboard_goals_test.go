package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// 目标写入是整个仪表盘唯一的写口子：令牌校验之外，脏输入必须全部挡在 400，
// 不能让 goals 表里混进画不出线死数据。
func TestPutGoal(t *testing.T) {
	const token = "s3cr3t"

	t.Run("未配令牌即只读", func(t *testing.T) {
		r, _ := newRouter(t, "")
		putJSON(t, r, "/api/goals", `{"metric":"step_count","target":8000}`, token).
			wantErr(t, http.StatusServiceUnavailable)
		deleteWith(t, r, "/api/goals?metric=step_count", token).
			wantErr(t, http.StatusServiceUnavailable)
	})

	t.Run("令牌错误", func(t *testing.T) {
		r, _ := newRouter(t, token)
		putJSON(t, r, "/api/goals", `{"metric":"step_count","target":8000}`, "wrong").
			wantErr(t, http.StatusUnauthorized)
		deleteWith(t, r, "/api/goals?metric=step_count", "wrong").
			wantErr(t, http.StatusUnauthorized)
		if len(get(t, r, "/api/goals").arr(t, "goals")) != 0 {
			t.Fatal("被拒的写入不能落库")
		}
	})

	t.Run("写入成功后回读整表", func(t *testing.T) {
		r, _ := newRouter(t, token)
		got := putJSON(t, r, "/api/goals", `{"metric":"step_count","target":8000}`, token)
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		goals := got.arr(t, "goals")
		if len(goals) != 1 {
			t.Fatalf("PUT 后应回读整表: %s", got.body)
		}
		item := goals[0].(map[string]any)
		if item["metric"] != "step_count" || item["target"] != float64(8000) {
			t.Fatalf("写入值不符: %v", item)
		}
		// 单位与中文名一律从字典取，表里只存数值
		if item["display_name"] != "步数" || item["unit"] != "count" {
			t.Fatalf("展示字段应从字典补全: %v", item)
		}

		again := get(t, r, "/api/goals")
		if len(again.arr(t, "goals")) != 1 {
			t.Fatalf("GET 应读到同一条: %s", again.body)
		}

		// 同指标覆盖写：ON CONFLICT 更新而不是插新行
		putJSON(t, r, "/api/goals", `{"metric":"step_count","target":10000}`, token)
		after := get(t, r, "/api/goals").arr(t, "goals")
		if len(after) != 1 || after[0].(map[string]any)["target"] != float64(10000) {
			t.Fatalf("重复设置应覆盖: %v", after)
		}
	})

	t.Run("脏输入一律 400", func(t *testing.T) {
		r, _ := newRouter(t, token)
		cases := []struct {
			name string
			body string
			msg  string // 错误文案要能告诉用户到底哪儿错了
		}{
			{"非法 JSON", `{"metric":`, "请求体不是合法 JSON"},
			{"不是对象", `[1,2]`, "请求体不是合法 JSON"},
			{"空体", `{}`, "metric 不能为空"},
			// 现状记录：putGoal 不对 metric 做 TrimSpace（deleteGoal 做了），
			// 所以全是空白的指标名会以「未登记」被拒而不是「不能为空」
			{"metric 为空白", `{"metric":"  ","target":8000}`, postgres.ErrUnknownGoalMetric.Error()},
			{"未登记指标", `{"metric":"nosuchmetric","target":8000}`, postgres.ErrUnknownGoalMetric.Error()},
			{"目标为 0", `{"metric":"step_count","target":0}`, "目标必须大于 0"},
			{"目标为负", `{"metric":"step_count","target":-1}`, "目标必须大于 0"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				got := putJSON(t, r, "/api/goals", c.body, token)
				got.wantErr(t, http.StatusBadRequest)
				if !strings.Contains(got.str(t, "error"), c.msg) {
					t.Fatalf("错误文案应含 %q，实际 %q", c.msg, got.str(t, "error"))
				}
			})
		}
		if len(get(t, r, "/api/goals").arr(t, "goals")) != 0 {
			t.Fatal("被拒的请求不应落库")
		}
	})

	// 请求体上限 4KB：写目标本来就是几十个字节的体，
	// 不设上限就等于把内存交给一个带令牌就能访问的口子。
	t.Run("超过 4096 字节的请求体被拒", func(t *testing.T) {
		r, _ := newRouter(t, token)
		large := `{"metric":"step_count","target":8000,"note":"` + strings.Repeat("x", 5000) + `"}`
		putJSON(t, r, "/api/goals", large, token).wantErr(t, http.StatusBadRequest)

		small := `{"metric":"step_count","target":8000,"note":"` + strings.Repeat("y", 3000) + `"}`
		if got := putJSON(t, r, "/api/goals", small, token); got.status != http.StatusOK {
			t.Fatalf("4KB 以内应正常写入: %d %s", got.status, got.body)
		}
	})
}

func TestDeleteGoal(t *testing.T) {
	const token = "s3cr3t"
	r, db := newRouter(t, token)
	if err := db.SetGoal(context.Background(), "step_count", 8000, true); err != nil {
		t.Fatalf("预置目标失败: %v", err)
	}

	t.Run("缺 metric 参数", func(t *testing.T) {
		deleteWith(t, r, "/api/goals", token).wantErr(t, http.StatusBadRequest)
		deleteWith(t, r, "/api/goals?metric=%20%20", token).wantErr(t, http.StatusBadRequest)
	})

	t.Run("删除后回读整表", func(t *testing.T) {
		got := deleteWith(t, r, "/api/goals?metric=step_count", token)
		if got.status != http.StatusOK {
			t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
		}
		if len(got.arr(t, "goals")) != 0 {
			t.Fatalf("删完应为空数组而不是 null: %s", got.body)
		}
	})

	t.Run("删不存在的指标是幂等的", func(t *testing.T) {
		got := deleteWith(t, r, "/api/goals?metric=heart_rate", token)
		if got.status != http.StatusOK || len(got.arr(t, "goals")) != 0 {
			t.Fatalf("删不存在的目标应 200 并回空表: %d %s", got.status, got.body)
		}
	})

	t.Run("存储层报错时 500", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		call(t, r, http.MethodDelete, "/api/goals?metric=step_count", nil,
			map[string]string{"Authorization": "Bearer " + token}, ctx).
			wantErr(t, http.StatusInternalServerError)
	})
}

func TestListGoalsEnrichesFromDictionary(t *testing.T) {
	r, db := newRouter(t, "")
	if err := db.SetGoal(context.Background(), "heart_rate", 60, true); err != nil {
		t.Fatalf("预置目标失败: %v", err)
	}
	// 字典里没有的指标名（用户后来从字典删掉了）：中文名要回落键名而不是空着
	if _, err := db.Pool.Exec(context.Background(),
		`INSERT INTO goals (metric, target) VALUES ('legacy_metric', 5)`); err != nil {
		t.Fatalf("塞入历史目标失败: %v", err)
	}

	got := get(t, r, "/api/goals")
	if got.status != http.StatusOK {
		t.Fatalf("状态码 = %d（body=%s）", got.status, got.body)
	}
	goals := got.arr(t, "goals")
	if len(goals) != 2 {
		t.Fatalf("应回两条: %s", got.body)
	}
	byName := map[string]map[string]any{}
	for _, raw := range goals {
		item := raw.(map[string]any)
		byName[item["metric"].(string)] = item
	}
	hr := byName["heart_rate"]
	if hr["display_name"] != "心率" || hr["unit"] != "bpm" || hr["target"] != float64(60) {
		t.Fatalf("字典字段不符: %v", hr)
	}
	if s, ok := hr["updated_at"].(string); !ok || s == "" {
		t.Fatalf("updated_at 应可读: %v", hr)
	}
	legacy := byName["legacy_metric"]
	if legacy["display_name"] != "legacy_metric" || legacy["unit"] != "" {
		t.Fatalf("未登记指标应回落键名: %v", legacy)
	}

	// 目标列表按指标名升序，页面据此对齐卡片顺序
	var order []string
	for _, raw := range goals {
		order = append(order, raw.(map[string]any)["metric"].(string))
	}
	if b, err := json.Marshal(order); err != nil || string(b) != `["heart_rate","legacy_metric"]` {
		t.Fatalf("排序不符: %v", order)
	}
}
