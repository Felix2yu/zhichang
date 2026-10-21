package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 这里跑的是真的 MCP 协议栈（内存传输 + 客户端 initialize/CallTool），
// 而不是直接调 Deps 的方法：工具的 schema 绑定、AddTool 注册闭包、
// textResult 把 error 折成 IsError:true 的分支，只有走这条链路才会被执行。

// toolNames 是对外承诺的工具清单：少一个就说明某次改动把注册闭包删了。
var toolNames = []string{
	"health_list_metrics",
	"health_daily_summary",
	"health_query",
	"health_compare",
	"health_latest",
	"health_sleep",
	"health_sleep_detail",
	"health_sleep_delete",
	"health_workouts",
	"health_ecg",
	"health_data_coverage",
	"health_correlation",
}

func connect(t *testing.T, d *Deps) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()
	// 服务端必须先 Connect：是客户端在连接时发起 MCP 初始化的
	ss, err := New(d).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("服务端连接失败: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	c := mcp.NewClient(&mcp.Implementation{Name: "zhichang-test", Version: "0.0.1"}, nil)
	cs, err := c.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("客户端连接失败: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// callTool 返回工具的首段文本与 IsError 标记。
// 断言取 Content[0] 而不是 StructuredContent：AI 客户端读的是这段中文文本，
// 而 textResult 的「查询失败: 」前缀只有在这里才看得见。
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("调用 %s 失败: %v", name, err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("%s 应只回一段文本（否则客户端会读到重复内容），实际 %d 段", name, len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s 返回了非文本内容 %T", name, res.Content[0])
	}
	// 结构化输出必须与文本同值：客户端二选一读取，不能给两份事实。
	// 缺失只允许发生在报错时——SDK 在参数校验阶段就拒掉的调用没进过 handler。
	sc, hasStructured := res.StructuredContent.(map[string]any)
	if !hasStructured {
		if res.StructuredContent != nil {
			t.Fatalf("%s 结构化输出类型异常: %#v", name, res.StructuredContent)
		}
		if !res.IsError {
			t.Fatalf("%s 成功却没给结构化输出", name)
		}
		return tc.Text, res.IsError
	}
	if got, want := sc["text"], strings.TrimPrefix(tc.Text, "查询失败: "); got != want {
		t.Errorf("%s 的结构化文本与内容文本不一致: %#v vs %q", name, got, want)
	}
	return tc.Text, res.IsError
}

func TestToolRegistry(t *testing.T) {
	d := newDeps(t)
	cs := connect(t, d)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("列工具失败: %v", err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	if len(res.Tools) != len(toolNames) {
		t.Errorf("工具数量应为 %d，实际 %d", len(toolNames), len(res.Tools))
	}
	for _, name := range toolNames {
		tool, ok := byName[name]
		if !ok {
			t.Errorf("缺少工具 %s", name)
			continue
		}
		if tool.Description == "" {
			t.Errorf("%s 没有描述，模型无从选它", name)
		}
	}

	// 参数说明写在 jsonschema tag 上，靠 AddTool 绑进 input schema：
	// 这是模型唯一能看到的「date 传睡眠日而不是起床日」这类口径说明。
	q := byName["health_query"]
	if q == nil || q.InputSchema == nil {
		t.Fatal("health_query 没有 input schema")
	}
	raw, err := json.Marshal(q.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	requireParts(t, string(raw),
		`"metrics"`, "逗号分隔的指标名", "起始日期 YYYY-MM-DD，默认 30 天前", "结束日期 YYYY-MM-DD，默认今天",
	)
	corr := byName["health_correlation"]
	if corr == nil {
		t.Fatal("health_correlation 未注册")
	}
	corrs, err := json.Marshal(corr.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	// a / b 没写 omitempty，所以是必填项；schema 必须真的把它们标成 required
	requireParts(t, string(corrs), `"required"`)

	// 删除工具的安全语义必须写进 schema：模型只能看到这些说明，
	// 不能让它在用户没要求时凭默认值就把数据清掉。
	del := byName["health_sleep_delete"]
	if del == nil {
		t.Fatal("health_sleep_delete 未注册")
	}
	dels, err := json.Marshal(del.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	requireParts(t, string(dels), `"confirm"`, "留空表示不限")
	requireParts(t, del.Description, "confirm=true", "不可撤销", "预演")
	// 危险操作要让客户端看得见：缺了注解，UI 会把它当普通查询工具。
	if del.Annotations == nil || del.Annotations.DestructiveHint == nil || !*del.Annotations.DestructiveHint {
		t.Error("health_sleep_delete 应标记为 destructive")
	}

	// 出参 schema 由 Result 推导，客户端据此校验 structuredContent
	out, err := json.Marshal(q.OutputSchema)
	if err != nil {
		t.Fatalf("出参 schema 序列化失败: %v", err)
	}
	requireParts(t, string(out), `"text"`)
}

func TestToolsOverTransport(t *testing.T) {
	t.Run("指标目录", func(t *testing.T) {
		d := newDeps(t)
		cs := connect(t, d)
		out, isErr := callTool(t, cs, "health_list_metrics", map[string]any{})
		if isErr {
			t.Fatalf("不该报错: %s", out)
		}
		requireParts(t, out, "指标目录（时区 Asia/Shanghai", "| 步数 |")
	})

	t.Run("某天概览走默认今天", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, daily("step_count", day(-1), 1000), daily("step_count", day(0), 2050))
		cs := connect(t, d)
		out, _ := callTool(t, cs, "health_daily_summary", map[string]any{})
		requireParts(t, out, day(0)+" 概览", "- 步数(step_count): 2050.00 count（前一日 1000.00 count，+105.0%）")
	})

	t.Run("序列查询", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, daily("step_count", day(0), 2050))
		cs := connect(t, d)
		out, _ := callTool(t, cs, "health_query", map[string]any{
			"metrics": "step_count", "from": day(0), "to": day(0),
		})
		requireParts(t, out, "步数(step_count)（"+day(0)+" ~ "+day(0)+"，1 天，单位 count）", "2050.00")
	})

	t.Run("区间对比", func(t *testing.T) {
		d := newDeps(t)
		var rows []row
		rows = append(rows, rowsFor("step_count", -6, 0, 100)...)
		rows = append(rows, rowsFor("step_count", -13, -7, 50)...)
		putDaily(t, d, rows...)
		cs := connect(t, d)
		out, _ := callTool(t, cs, "health_compare", map[string]any{"metrics": "step_count"})
		requireParts(t, out, "| 步数(step_count) | 100.00 | 50.00 | +50.00 | +100.0% |")
	})

	t.Run("最新值", func(t *testing.T) {
		d := newDeps(t)
		putDaily(t, d, daily("body_mass", day(-3), 70))
		cs := connect(t, d)
		out, _ := callTool(t, cs, "health_latest", map[string]any{"metrics": "body_mass"})
		requireParts(t, out, "- 体重(body_mass): 70.00 kg ("+day(-3))
	})

	t.Run("睡眠与睡眠详情", func(t *testing.T) {
		d := newDeps(t)
		date := day(-1)
		putSleep(t, d, *sleepNight(date))
		putSamples(t, d,
			seg(date, 23*60+30, 40, "AsleepDeep", "Apple Watch"),
			row{metric: "heart_rate", source: "Apple Watch", at: at(nextDay(date), 3, 12), value: 70, vmin: f64(58), unit: "count/min"},
		)
		cs := connect(t, d)
		out, _ := callTool(t, cs, "health_sleep", map[string]any{"from": date, "to": date})
		requireParts(t, out, "| "+date+" | 7.5 | 1.5 | 4.0 | 2.0 | 8.0 | 58 bpm | Apple Watch |")

		detail, _ := callTool(t, cs, "health_sleep_detail", map[string]any{"date": date})
		requireParts(t, detail, "睡眠详情 "+date, "  23:30–00:10 深睡（40 分）", "心率 最低 58 bpm")
	})

	t.Run("清空睡眠先预演再确认", func(t *testing.T) {
		d := newDeps(t)
		date := day(-1)
		putSleep(t, d, *sleepNight(date))
		putSamples(t, d,
			seg(date, 23*60+30, 40, "AsleepDeep", "Apple Watch"),
			seg(date, 24*60+10, 120, "AsleepCore", "Apple Watch"),
		)
		cs := connect(t, d)

		preview, isErr := callTool(t, cs, "health_sleep_delete", map[string]any{"from": date, "to": date})
		if isErr {
			t.Fatalf("预演不该报错: %s", preview)
		}
		requireParts(t, preview, "睡眠记录清理预演（"+date+" ~ "+date+"）",
			"- 睡眠会话 1 条", "- 分期样本 2 条", "confirm=true")
		// 预演必须真的不动数据：确认之前还能正常查到这一晚。
		still, _ := callTool(t, cs, "health_sleep", map[string]any{"from": date, "to": date})
		requireParts(t, still, "| "+date+" | 7.5 |")

		done, isErr := callTool(t, cs, "health_sleep_delete", map[string]any{
			"from": date, "to": date, "confirm": true,
		})
		if isErr {
			t.Fatalf("确认删除不该报错: %s", done)
		}
		requireParts(t, done, "睡眠记录已清空（"+date+" ~ "+date+"）",
			"删除睡眠会话 1 条、分期样本 2 条", "现在可以重新导入睡眠数据了")

		gone, _ := callTool(t, cs, "health_sleep", map[string]any{"from": date, "to": date})
		requireParts(t, gone, date+" ~ "+date+" 没有睡眠记录")
	})

	t.Run("运动", func(t *testing.T) {
		d := newDeps(t)
		putWorkouts(t, d, workout(day(0), "户外跑步", 32.4, 299.6, 5.234))
		cs := connect(t, d)
		out, _ := callTool(t, cs, "health_workouts", map[string]any{"from": day(0), "to": day(0)})
		requireParts(t, out, "| "+day(0)+" | 户外跑步 | 32 | 300 kcal | 5.23 km | Apple Watch |")
	})

	t.Run("覆盖度", func(t *testing.T) {
		d := newDeps(t)
		cs := connect(t, d)
		out, _ := callTool(t, cs, "health_data_coverage", map[string]any{"days": 30})
		requireParts(t, out, "最近同步: 尚无记录", "近 30 天缺失")
	})

	t.Run("相关性", func(t *testing.T) {
		d := newDeps(t)
		var rows []row
		for i, v := range []float64{1, 2, 3} {
			rows = append(rows, daily("step_count", day(-3+i), v), daily("active_energy", day(-3+i), 2*v))
		}
		putDaily(t, d, rows...)
		cs := connect(t, d)
		out, _ := callTool(t, cs, "health_correlation", map[string]any{
			"a": "step_count", "b": "active_energy", "from": day(-3), "to": day(-1),
		})
		requireParts(t, out, "皮尔逊相关系数 r = 1.000")
	})
}

// TestToolErrorBranches 盯 textResult 的失败分支：
// 工具报错必须以 IsError:true + 「查询失败: 」前缀回到客户端，
// 而不是把 Go 错误抛成 JSON-RPC 层错误让整次调用失败。
func TestToolErrorBranches(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"未知指标", "health_query", map[string]any{"metrics": "bogus"}, "未登记的指标: bogus"},
		{"未知指标对比", "health_compare", map[string]any{"metrics": "bogus"}, "没有可用指标"},
		{"未知指标最新值", "health_latest", map[string]any{"metrics": "bogus"}, "没有可用指标"},
		{"坏日期概览", "health_daily_summary", map[string]any{"date": "2026-13-01"}, "日期格式应为 YYYY-MM-DD"},
		{"坏日期序列", "health_query", map[string]any{"metrics": "step_count", "from": "昨天"}, "日期格式应为 YYYY-MM-DD"},
		{"坏日期睡眠", "health_sleep", map[string]any{"from": "昨天"}, "日期格式应为 YYYY-MM-DD"},
		{"坏日期睡眠详情", "health_sleep_detail", map[string]any{"date": "昨天"}, "日期格式应为 YYYY-MM-DD"},
		{"坏日期清空", "health_sleep_delete", map[string]any{"from": "昨天", "confirm": true}, "日期格式应为 YYYY-MM-DD"},
		{"坏日期运动", "health_workouts", map[string]any{"to": "昨天"}, "日期格式应为 YYYY-MM-DD"},
		{"相关性未登记", "health_correlation", map[string]any{"a": "bogus", "b": "step_count"}, "指标未登记"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDeps(t)
			putDaily(t, d, daily("step_count", day(0), 100))
			out, isErr := callTool(t, connect(t, d), tc.tool, tc.args)
			if !isErr {
				t.Fatalf("应标记为工具错误: %q", out)
			}
			requireParts(t, out, "查询失败: ", tc.want)
		})
	}

	t.Run("必填参数缺失由schema拦下", func(t *testing.T) {
		d := newDeps(t)
		out, isErr := callTool(t, connect(t, d), "health_correlation", map[string]any{})
		// 这种错误不该进业务代码：没带 a/b 就必须在绑定阶段被拒，
		// 否则会拿空指标名去打数据库。
		if !isErr {
			t.Fatalf("缺必填参数应报错: %q", out)
		}
		requireParts(t, out, `validating "arguments"`)
	})

	t.Run("真实数据库故障", func(t *testing.T) {
		d := newDeps(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		// 用取消掉的 ctx 直接构造一次调用：工具层必须把仓储层错误原样折进文本
		msg := fail(t, func() (string, error) { return d.Query(ctx, "step_count", day(0), day(0)) })
		if !strings.Contains(msg, "context") {
			t.Errorf("应透出数据库错误: %q", msg)
		}
	})
}

func TestNewBuildsServer(t *testing.T) {
	d := newDeps(t)
	s := New(d)
	if s == nil {
		t.Fatal("New 返回 nil")
	}
	// 服务器名/版本会出现在 initialize 结果里，客户端据此认服务
	cs := connect(t, d)
	info := cs.InitializeResult()
	if info == nil {
		t.Fatal("initialize 结果为空")
	}
	if got, want := info.ServerInfo.Name+":"+info.ServerInfo.Version, "zhichang:0.1.0"; got != want {
		t.Errorf("服务标识错: %q", got)
	}
}

func TestRegistryFromDBFallsBackToDefaults(t *testing.T) {
	// 指标字典为空时仓储层回落到内置口径，MCP 的中文名/单位就跟着它
	db := sharedDB(t)
	reg, fromDB, err := db.LoadMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d := DepsFrom(db, reg, testLoc, testTZName, false)
	out := text(t, func() (string, error) { return d.ListMetrics(context.Background()) })
	if !strings.Contains(out, "步数") {
		t.Errorf("字典应能给出中文指标名（fromDB=%v）\n%s", fromDB, out)
	}
	if d.Metrics != reg || !d.known("step_count") {
		t.Error("仓储层字典没接进 Deps")
	}
	if len(d.Metrics.Enabled()) == 0 {
		t.Error("字典不应为空")
	}
}
