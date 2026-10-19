package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/store/postgres"
)

// 嵌入轨迹的入库落点。
//
// 「Normalize 返回值里有」不等于「库里有了」——basalEnergy 丢了一整年就是因为只验到前一层。
// 运动内的每一类数据都要验到表里，这条约定对后面的序列表同样适用。
const routePayload = `{"data":{"workouts":[{
	"name":"户外 步行","start":"2026-05-03 08:00:00 +0800","end":"2026-05-03 08:03:00 +0800",
	"duration":180,"source":"示例的Apple Watch",
	"totalEnergy":{"qty":41.84,"units":"kJ"},"distance":{"qty":0.2,"units":"km"},
	"route":[
		{"timestamp":"2026-05-03 08:00:00 +0800","latitude":30.25,"longitude":120.15,"altitude":4.5},
		{"timestamp":"2026-05-03 08:01:00 +0800","latitude":30.26,"longitude":120.16,"altitude":6.5},
		{"timestamp":"2026-05-03 08:02:00 +0800","latitude":30.27,"longitude":120.17,"altitude":5.5}
	]}]}}`

func TestIngestEmbeddedRouteLandsInDB(t *testing.T) {
	db, svc := service(t, "strict")
	ctx := context.Background()

	out, err := svc.Ingest(ctx, mustParse(t, routePayload),
		postgres.RunMeta{Transport: "rest", AutomationID: "auto-route", SessionID: "route-1"})
	if err != nil {
		t.Fatalf("摄入失败: %v", err)
	}
	if out.Workouts != 1 {
		t.Fatalf("应摄入 1 条运动: %+v", out.IngestStats)
	}

	var (
		file   string
		points int
		lats   []float64
		secs   []int32
	)
	err = db.Pool.QueryRow(ctx,
		`SELECT r.file_name, r.point_count, r.lats, r.at_secs
		   FROM workout_routes r JOIN workouts w ON w.id = r.workout_id
		  WHERE w.name = '户外 步行'`).Scan(&file, &points, &lats, &secs)
	if err != nil {
		t.Fatalf("内嵌轨迹没有随摄入落库: %v", err)
	}
	if file != "health-auto-export.json" {
		t.Fatalf("来源标记错了，覆盖规则会跟手工导入的 GPX 抢同一行: %q", file)
	}
	if points != 3 || len(lats) != 3 || secs[2] != 120 {
		t.Fatalf("折线落库不完整: points=%d lats=%v secs=%v", points, lats, secs)
	}

	// 重放同一份导出：不产生第二次写入（一年的导出是 30 万个点，重写一遍的代价看得见）。
	var first time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT ingested_at FROM workout_routes`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Ingest(ctx, mustParse(t, routePayload),
		postgres.RunMeta{Transport: "rest", AutomationID: "auto-route", SessionID: "route-2"}); err != nil {
		t.Fatal(err)
	}
	var (
		rows  int
		after time.Time
	)
	if err := db.Pool.QueryRow(ctx,
		`SELECT count(*), max(ingested_at) FROM workout_routes`).Scan(&rows, &after); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("重放不该多出第二行轨迹，实际 %d 行", rows)
	}
	if !after.Equal(first) {
		t.Fatalf("同内容重放不该重写轨迹行: %s → %s", first, after)
	}
}

// 运动内逐分钟数组的入库落点。基础能量丢了一整年就丢在「解析通过但没人写表」这一步，
// 只看 Normalize 的返回值算不出来，只有查得到表才算数。
const seriesPayload = `{"data":{"workouts":[{
	"name":"户外 步行","start":"2026-05-03 08:00:00 +0800","end":"2026-05-03 08:03:00 +0800",
	"duration":180,"source":"示例的Apple Watch",
	"stepCount":[{"date":"2026-05-03 08:00:00 +0800","qty":40,"units":"步"},
	             {"date":"2026-05-03 08:01:00 +0800","qty":60,"units":"步"}],
	"activeEnergy":[{"date":"2026-05-03 08:01:00 +0800","qty":20.92,"units":"kJ"}],
	"basalEnergy":[{"date":"2026-05-03 08:02:00 +0800","qty":8.368,"units":"kJ"}]
}]}}`

func TestIngestWorkoutMetricSeriesLandsInDB(t *testing.T) {
	db, svc := service(t, "strict")
	ctx := context.Background()

	if _, err := svc.Ingest(ctx, mustParse(t, seriesPayload),
		postgres.RunMeta{Transport: "rest", AutomationID: "auto-series", SessionID: "series-1"}); err != nil {
		t.Fatalf("摄入失败: %v", err)
	}

	// 值必须已换到字典口径：20.92 kJ → 5 kcal、8.368 kJ → 2 kcal。
	// 只换值不换单位（或反之）会让展示层把 kJ 标成 kcal，虚高 4.18 倍而且看不出错。
	cases := []struct {
		metric string
		unit   string
		want   []float64
	}{
		{"active_energy", "kcal", []float64{5}},
		{"basal_energy_burned", "kcal", []float64{2}},
		{"step_count", "步", []float64{40, 60}}, // 上游单位查不到换算，值原样、单位留原文
	}
	for _, c := range cases {
		var (
			vals []float64
			unit *string
		)
		q := `SELECT s.vals, s.unit FROM workout_metric_series s
		        JOIN workouts w ON w.id = s.workout_id
		       WHERE w.name = '户外 步行' AND s.metric = $1`
		if err := db.Pool.QueryRow(ctx, q, c.metric).Scan(&vals, &unit); err != nil {
			t.Fatalf("%s 没有随摄入落库: %v", c.metric, err)
		}
		if unit == nil || *unit != c.unit {
			t.Fatalf("%s 的单位应为 %q，实际 %v", c.metric, c.unit, unit)
		}
		if len(vals) != len(c.want) {
			t.Fatalf("%s 逐点数错误: %v", c.metric, vals)
		}
		for i, want := range c.want {
			if d := vals[i] - want; d > 1e-6 || d < -1e-6 {
				t.Fatalf("%s 第 %d 点应为 %v，实际 %v", c.metric, i, want, vals[i])
			}
		}
	}

	// 时轴相对运动起点：08:02:00 是第 120 秒，落库的显式秒数不能是别的。
	var secs []int32
	if err := db.Pool.QueryRow(ctx,
		`SELECT s.at_secs FROM workout_metric_series s
		   JOIN workouts w ON w.id = s.workout_id
		  WHERE w.name = '户外 步行' AND s.metric = 'basal_energy_burned'`).Scan(&secs); err != nil {
		t.Fatal(err)
	}
	if len(secs) != 1 || secs[0] != 120 {
		t.Fatalf("相对秒数错误: %v", secs)
	}
}
