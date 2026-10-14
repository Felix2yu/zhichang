package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// New 构造 MCP 服务器并注册全部只读工具。
func New(deps *Deps) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "zhichang", Version: "0.1.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "health_list_metrics",
		Description: "列出全部可用健康指标：中文名、单位、聚合口径（日内 sum/mean、跨来源 max/加权 mean）",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, Result, error) {
		return textResult(deps.ListMetrics(ctx))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "health_daily_summary",
		Description: "查询某一天的健康全貌（步数、能量、心率、体重、睡眠等），含与前一日对比",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Date string `json:"date,omitempty" jsonschema:"日期，格式 YYYY-MM-DD，默认今天"`
	}) (*mcp.CallToolResult, Result, error) {
		return textResult(deps.DailySummary(ctx, in.Date))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "health_query",
		Description: "按指标与日期区间查询天级序列（自动套用该指标的聚合口径）",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Metrics string `json:"metrics,omitempty" jsonschema:"逗号分隔的指标名，留空表示全部启用指标"`
		From    string `json:"from,omitempty" jsonschema:"起始日期 YYYY-MM-DD，默认 30 天前"`
		To      string `json:"to,omitempty" jsonschema:"结束日期 YYYY-MM-DD，默认今天"`
	}) (*mcp.CallToolResult, Result, error) {
		return textResult(deps.Query(ctx, in.Metrics, in.From, in.To))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "health_compare",
		Description: "对比两个日期区间的日均水平（如本周 vs 上周），给出变化量与变化率",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Metrics string `json:"metrics,omitempty" jsonschema:"逗号分隔的指标名"`
		AFrom   string `json:"a_from,omitempty" jsonschema:"区间 A 起始日期，默认 7 天前"`
		ATo     string `json:"a_to,omitempty" jsonschema:"区间 A 结束日期，默认今天"`
		BFrom   string `json:"b_from,omitempty" jsonschema:"区间 B 起始日期，默认 14 天前"`
		BTo     string `json:"b_to,omitempty" jsonschema:"区间 B 结束日期，默认 8 天前"`
	}) (*mcp.CallToolResult, Result, error) {
		return textResult(deps.Compare(ctx, in.Metrics, in.AFrom, in.ATo, in.BFrom, in.BTo))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "health_latest",
		Description: "查询各指标最近一次有数据的日期与数值",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Metrics string `json:"metrics,omitempty" jsonschema:"逗号分隔的指标名"`
	}) (*mcp.CallToolResult, Result, error) {
		return textResult(deps.Latest(ctx, in.Metrics))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "health_sleep",
		Description: "查询区间内的睡眠记录：总时长、深睡/核心/REM 分期、在床时长、夜间最低心率（按入睡时间窗关联）",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		From string `json:"from,omitempty" jsonschema:"起始日期 YYYY-MM-DD，默认 30 天前"`
		To   string `json:"to,omitempty" jsonschema:"结束日期 YYYY-MM-DD，默认今天"`
	}) (*mcp.CallToolResult, Result, error) {
		return textResult(deps.Sleep(ctx, in.From, in.To))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "health_sleep_detail",
		Description: "查询某一晚的睡眠详情：入睡/起床时刻、入睡潜伏期、入睡后清醒（WASO）、" +
			"逐段分期时间线、窗口内的夜间心率/HRV/血氧/呼吸频率。date 传睡眠日（入睡时刻那天的日期）",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Date string `json:"date,omitempty" jsonschema:"睡眠日 YYYY-MM-DD（以入睡时刻归属），默认今天"`
	}) (*mcp.CallToolResult, Result, error) {
		return textResult(deps.SleepDetail(ctx, in.Date))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "health_workouts",
		Description: "查询区间内的运动记录：项目、时长、消耗、距离",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		From string `json:"from,omitempty" jsonschema:"起始日期 YYYY-MM-DD，默认 30 天前"`
		To   string `json:"to,omitempty" jsonschema:"结束日期 YYYY-MM-DD，默认今天"`
	}) (*mcp.CallToolResult, Result, error) {
		return textResult(deps.Workouts(ctx, in.From, in.To))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "health_ecg",
		Description: "查询区间内的心电图记录：Apple 的判定结论（窦性心律/房颤/不确定等）、" +
			"当时平均心率、采样率与点数。不含电压波形（一次测量 15360 个点）",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		From string `json:"from,omitempty" jsonschema:"起始日期 YYYY-MM-DD，默认 90 天前"`
		To   string `json:"to,omitempty" jsonschema:"结束日期 YYYY-MM-DD，默认今天"`
	}) (*mcp.CallToolResult, Result, error) {
		return textResult(deps.ECG(ctx, in.From, in.To))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "health_data_coverage",
		Description: "查看数据新鲜度与覆盖情况：最近同步时间、缺失日、同步水位。用于判断数据是否可信",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Days int `json:"days,omitempty" jsonschema:"检查最近多少天，默认 30"`
	}) (*mcp.CallToolResult, Result, error) {
		return textResult(deps.Coverage(ctx, in.Days))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "health_correlation",
		Description: "计算两个指标在天粒度上的皮尔逊相关系数（如步数与静息心率）",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		A    string `json:"a" jsonschema:"指标 A 名称"`
		B    string `json:"b" jsonschema:"指标 B 名称"`
		From string `json:"from,omitempty" jsonschema:"起始日期 YYYY-MM-DD，默认 90 天前"`
		To   string `json:"to,omitempty" jsonschema:"结束日期 YYYY-MM-DD，默认今天"`
	}) (*mcp.CallToolResult, Result, error) {
		return textResult(deps.Correlation(ctx, in.A, in.B, in.From, in.To))
	})

	return s
}

// DepsFrom 便捷构造依赖。
func DepsFrom(db *postgres.DB, reg *metric.Registry, loc *time.Location, tzName string) *Deps {
	return &Deps{DB: db, Metrics: reg, Location: loc, Timezone: tzName}
}

func textResult(s string, err error) (*mcp.CallToolResult, Result, error) {
	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "查询失败: " + err.Error()}},
			IsError: true,
		}, Result{Text: err.Error()}, nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}, Result{Text: s}, nil
}
