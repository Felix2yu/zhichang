package pipeline

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/ingest/hae"
	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// v3 形态（2026 版 HAE 导出）的入库落点回归：
// 光有「解析不报错」不够——真正的坑是解析通过了、值却带着上游单位进库，
// 展示层再按字典单位渲染，界面上就会安静地显示错 4~60 倍的数值。
// 所以这里一路验到 daily_metrics_day 与 workouts 表，锁的是「界面拿到的数」。
func TestIngestV3UnitsLandInDB(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	reg, _, err := db.LoadMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	home, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open("../../../testdata/hae_export_v3.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	payload, err := hae.ParsePayload(f)
	if err != nil {
		t.Fatalf("v3 导出必须能解析: %v", err)
	}

	svc := New(db, reg, home, "strict")
	out, err := svc.Ingest(ctx, payload, postgres.RunMeta{Transport: "rest", AutomationID: "auto-v3", SessionID: "v3-1"})
	if err != nil {
		t.Fatalf("摄入失败: %v", err)
	}
	if out.Inserted != 5 || out.Workouts != 2 || out.Sleep != 1 {
		t.Fatalf("入库条数错误: inserted=%d workouts=%d sleep=%d", out.Inserted, out.Workouts, out.Sleep)
	}
	if len(out.UnknownMetrics) != 0 {
		t.Fatalf("v3 样例不该产生未登记指标: %v", out.UnknownMetrics)
	}

	// 样本层：值与其单位必须一起被换算，否则 unit 与 value 会互相矛盾
	var value float64
	var unit string
	if err := db.Pool.QueryRow(ctx,
		`SELECT value, unit FROM samples WHERE metric='active_energy'`).Scan(&value, &unit); err != nil {
		t.Fatal(err)
	}
	if math.Abs(value-300.0) > 1e-6 || unit != "kcal" {
		t.Fatalf("活动能量应换算成 300.0 kcal，实际 %v %s", value, unit)
	}

	// 聚合层：跑完队列后天级值必须是 kcal 口径，且字典声明的展示单位与之一致
	roll := postgres.NewRollup(db, reg)
	for i := 0; i < 50; i++ {
		n, err := roll.RecomputePending(ctx, 500)
		if err != nil {
			t.Fatalf("聚合失败: %v", err)
		}
		if n == 0 {
			break
		}
	}
	for m, want := range map[string]float64{
		"active_energy":       300.0,
		"basal_energy_burned": 2100.0,
		"body_mass":           68.0388555,
		"heart_rate":          72.41860465116279,
	} {
		var got float64
		if err := db.Pool.QueryRow(ctx,
			`SELECT value FROM daily_metrics_day WHERE metric=$1 AND local_date='2026-10-13'`, m).Scan(&got); err != nil {
			t.Fatalf("%s 天级聚合缺失: %v", m, err)
		}
		if math.Abs(got-want) > 1e-6 {
			t.Fatalf("%s 天级值应为 %v，实际 %v", m, want, got)
		}
		def, ok := reg.Get(m)
		if !ok || def.PreferredUnit == "" {
			t.Fatalf("%s 缺少字典定义，展示层无法取单位", m)
		}
		t.Logf("%s 天级值=%.6f 展示单位=%s", m, got, def.PreferredUnit)
	}

	// 运动层：时长分钟、距离 km、能耗 kcal，三项缺一都会让运动页显示空列或虚高数值
	rows, err := db.WorkoutRange(ctx, "2026-10-13", "2026-10-14")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("运动应为 2 条，实际 %d", len(rows))
	}
	var newShape, oldShape bool
	for _, r := range rows {
		if r.Name != "户外 步行" {
			continue
		}
		newShape = true
		if math.Abs(r.DurationMin-20.575) > 1e-6 {
			t.Fatalf("新版运动时长应为 14.841 分钟，实际 %v", r.DurationMin)
		}
		if r.TotalDistance == nil || math.Abs(*r.TotalDistance-1.8525) > 1e-9 || r.DistanceUnit != "km" {
			t.Fatalf("新版运动距离错误: %v %q", r.TotalDistance, r.DistanceUnit)
		}
		if r.TotalEnergy == nil || math.Abs(*r.TotalEnergy-120.0) > 1e-6 || r.EnergyUnit != "kcal" {
			t.Fatalf("新版运动能耗错误: %v %q", r.TotalEnergy, r.EnergyUnit)
		}
		if r.Source != "小明的Apple\u00a0Watch" {
			t.Fatalf("对象来源未取到 name: %q", r.Source)
		}
		oldShape = true
	}
	if !newShape || !oldShape {
		t.Fatal("新旧两种运动形态都应落库")
	}

	// 旧形态（分钟级 duration + totalDistance）在同一个库口径下不变
	var legacyMin float64
	if err := db.Pool.QueryRow(ctx,
		`SELECT duration_min FROM workouts WHERE name='Walking'`).Scan(&legacyMin); err != nil {
		t.Fatal(err)
	}
	if legacyMin != 30 {
		t.Fatalf("旧版运动时长应保持 30 分钟，实际 %v", legacyMin)
	}
}
