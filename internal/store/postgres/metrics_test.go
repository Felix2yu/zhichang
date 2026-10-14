package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// metrics_test.go 锁指标字典的读写口径：字典是全站聚合口径的唯一事实来源，
// 「重复播种不覆盖用户改过的定义」和「空表回落内置默认值」这两条一旦反了，
// 所有历史聚合结果都会跟着变。

func truncateMetricDefs(t *testing.T, db *DB) {
	t.Helper()
	mustExec(t, db, `TRUNCATE metric_defs`)
}

func testDef(metricKey, dayAgg, crossAgg string) metric.Def {
	return metric.Def{
		Metric: metricKey, DisplayNameZH: "测试指标",
		Kind: metric.Kind("cumulative"), DayAgg: metric.DayAgg(dayAgg), CrossSourceAgg: metric.CrossAgg(crossAgg),
		Unit: "count", PreferredUnit: "count", ValueField: "qty", Chart: "line",
		Enabled: true, SortOrder: 42,
	}
}

// TestSeedMetricsIsIdempotentAndNeverOverwrites 锁定播种口径：
//   - 首次把缺失的定义全部写入；
//   - 再跑一次一条都不写（服务每次启动都会 SeedMetrics，不能把用户改过的口径抹掉）；
//   - 用户把某个指标的 day_agg 改过之后重跑，改动必须原样保留。
func TestSeedMetricsIsIdempotentAndNeverOverwrites(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	truncateMetricDefs(t, db)

	defs := metric.Defaults()
	if len(defs) == 0 {
		t.Fatal("内置默认值不能是空的")
	}
	n, err := db.SeedMetrics(ctx, defs)
	if err != nil {
		t.Fatalf("首次播种失败: %v", err)
	}
	if n != len(defs) {
		t.Fatalf("空表时写入条数应等于默认值条数 %d，实际 %d", len(defs), n)
	}

	// 第二次：全都被 ON CONFLICT DO NOTHING 挡掉
	n2, err := db.SeedMetrics(ctx, defs)
	if err != nil {
		t.Fatalf("重复播种不该报错: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("重复播种应写 0 条，实际 %d", n2)
	}

	// 用户改过口径后重跑：改动存活（这正是 DO NOTHING 而不是 DO UPDATE 的原因）
	target := defs[0].Metric
	mustExec(t, db, `UPDATE metric_defs SET day_agg = 'min', updated_at = now() WHERE metric = $1`, target)
	if n3, err := db.SeedMetrics(ctx, defs); err != nil || n3 != 0 {
		t.Fatalf("改过口径后重跑不该写任何行：%d %v", n3, err)
	}
	reg, fromDB, err := db.LoadMetrics(ctx)
	if err != nil {
		t.Fatalf("LoadMetrics 失败: %v", err)
	}
	if !fromDB {
		t.Fatal("表里有数据时必须报「来自库里」，否则调用方会以为在用内置默认值")
	}
	d, ok := reg.Get(target)
	if !ok {
		t.Fatalf("字典里找不到 %s", target)
	}
	if d.DayAgg != metric.DayAgg("min") {
		t.Fatalf("用户改过的 day_agg 被播种覆盖了，实际 %q", d.DayAgg)
	}
	// 单位、中文名等展示字段也要原样读回来
	if d.DisplayNameZH == "" {
		t.Fatalf("中文名没读回来：%+v", d)
	}
	if d.SortOrder != defs[0].SortOrder {
		t.Fatalf("sort_order 不符：期望 %d 实际 %d", defs[0].SortOrder, d.SortOrder)
	}

	// 库里 unit 允许 NULL（内置定义就有留空的），COALESCE 成空串而不是报错
	var isNull bool
	if err := db.Pool.QueryRow(ctx, `SELECT unit IS NULL FROM metric_defs WHERE metric = 'height'`).Scan(&isNull); err == nil {
		if _, ok := reg.Get("height"); ok {
			h := mustGetDef(t, reg, "height")
			if isNull && h.Unit != "" {
				t.Fatalf("unit 为 NULL 时应回落空串，实际 %q", h.Unit)
			}
		}
	}

	// 违反字典表 CHECK 约束的定义：报错要指出是哪个指标，运维才知道改哪
	if _, err := db.SeedMetrics(ctx, []metric.Def{func() metric.Def {
		d := testDef("bogus_kind", "sum", "max")
		d.Kind = metric.Kind("不存在的类别")
		return d
	}()}); err == nil {
		t.Fatal("非法 kind 必须被库拦下")
	} else if !strings.Contains(err.Error(), "写入指标 bogus_kind 失败") {
		t.Fatalf("报错要带指标名: %v", err)
	}
	// 失败的那条没进表
	if _, ok := reg.Get("bogus_kind"); ok {
		t.Fatal("失败的定义不该在字典里")
	}
}

func mustGetDef(t *testing.T, reg *metric.Registry, key string) metric.Def {
	t.Helper()
	d, ok := reg.Get(key)
	if !ok {
		t.Fatalf("字典里没有 %s", key)
	}
	return d
}

// TestLoadMetricsFallsBackToDefaults 锁定空表回落：新库还没播种时，
// 服务必须能用内置默认值起起来（fromDB=false 让调用方知道要补种）。
func TestLoadMetricsFallsBackToDefaults(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	truncateMetricDefs(t, db)

	reg, fromDB, err := db.LoadMetrics(ctx)
	if err != nil {
		t.Fatalf("空表不该报错: %v", err)
	}
	if fromDB {
		t.Fatal("回落内置默认值时必须报 fromDB=false")
	}
	if reg == nil {
		t.Fatal("必须回一个可用的字典")
	}
	if len(reg.All()) == 0 {
		t.Fatal("内置默认值不能是空的")
	}
	// 回落出来的字典与 Defaults 同源：拿 step_count 对一下口径
	if _, ok := reg.Get("step_count"); !ok {
		t.Fatal("回落字典里应有 step_count")
	}

	// 关系不可见时报错，不能悄悄回默认值（那会让用户以为自己的改动丢了）
	broken := openMissingSchemaDB(t)
	if _, _, err := broken.LoadMetrics(ctx); err == nil {
		t.Fatal("读不到字典表必须报错")
	}
}

// TestSeedMetricsPartialGap 锁定「只补缺失」：库里已有两条、其中一条被用户改过，
// 播种只写入缺失的那条，返回的 n 是真正新增的条数。
func TestSeedMetricsPartialGap(t *testing.T) {
	db := openRollupDB(t)
	ctx := context.Background()
	truncateMetricDefs(t, db)

	if n, err := db.SeedMetrics(ctx, []metric.Def{
		testDef("probe_a", "sum", "max"),
		testDef("probe_b", "mean", "mean"),
	}); err != nil || n != 2 {
		t.Fatalf("首次播种 2 条：%d %v", n, err)
	}
	// 第三条是新的，前两条已被改过：只应写 1 条
	mustExec(t, db, `UPDATE metric_defs SET display_name_zh = '用户改名' WHERE metric = 'probe_a'`)
	n, err := db.SeedMetrics(ctx, []metric.Def{
		testDef("probe_a", "sum", "max"),
		testDef("probe_b", "mean", "mean"),
		testDef("probe_c", "count", "sum"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("只该补 1 条缺失定义，实际 %d", n)
	}
	reg, _, err := db.LoadMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustGetDef(t, reg, "probe_a"); got.DisplayNameZH != "用户改名" {
		t.Fatalf("用户改的中文名被覆盖成 %q", got.DisplayNameZH)
	}
	if got := mustGetDef(t, reg, "probe_c"); got.DayAgg != metric.DayAgg("count") {
		t.Fatalf("新指标没进来：%+v", got)
	}
	if len(reg.All()) != 3 {
		t.Fatalf("字典应只有 3 条，实际 %d", len(reg.All()))
	}
}
