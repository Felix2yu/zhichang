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
