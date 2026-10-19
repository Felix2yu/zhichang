package hae

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/zhichang/internal/domain/metric"
)

// 采集面契约。
//
// 这份样例是从一年真实导出里逐指标抄下**形态**（指标名、上游单位串、点的键集合、
// 运动与心电的键集合），数值全部换成合成值、设备名用「小明的…」占位。
// 它锁的不是「某个数是多少」，而是「上游发来的每一个字段，应用要么收、要么写明不收」——
// 基础能量丢了一整年、轨迹丢了一年、卧床时长存成恒 0，全是这一类：
// 上游形态与解析假设不一致，而界面完全看不出来。docs/hae-setup.md 里的「采集面自查」
// 原本靠人一年手动对照一次，这里把它变成跑一次测试。
const shapesFile = "../../../testdata/hae_export_upstream_shapes.json"

func loadShapes(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(shapesFile)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("样例本身必须能解析: %v", err)
	}
	return doc
}

func section(t *testing.T, doc map[string]any, name string) []any {
	t.Helper()
	data, _ := doc["data"].(map[string]any)
	arr, ok := data[name].([]any)
	if !ok {
		t.Fatalf("样例里没有 data.%s", name)
	}
	return arr
}

// 上游出现的每一个指标名，收敛到规范名之后必须在字典里。
// 不在字典里不等于「少一个指标」，而是双向失效：数据堆在一个没卡片的名字下，
// 字典里那条卡片永远空着（血氧当年就是这样）。
func TestUpstreamMetricNamesAreAllRegistered(t *testing.T) {
	reg := metric.New(metric.Defaults())
	var names []string
	for _, m := range section(t, loadShapes(t), "metrics") {
		mo := m.(map[string]any)
		raw, _ := mo["name"].(string)
		name := metric.Canonical(raw)
		if raw == "blood_pressure" {
			name = "blood_pressure_systolic" // 血压在入库时拆成两条
		}
		if !reg.IsKnown(name) {
			t.Errorf("上游指标 %q（规范名 %q）不在字典里", raw, name)
		}
		names = append(names, name)
	}
	if len(names) != 40 {
		t.Fatalf("样例应覆盖真实导出的全部 40 类指标，实际 %d", len(names))
	}
	// 别名收敛必须真的生效，否则上面那句「在字典里」是靠字典同时登记两个名字撑起来的，
	// 那会让趋势页出现两个同名 chip、其中一个永远无数据。
	for _, al := range []struct{ from, to string }{
		{"blood_oxygen_saturation", "oxygen_saturation"},
		{"apple_stand_hour", "stand_hour"},
		{"apple_stand_time", "stand_time"},
		{"apple_exercise_time", "exercise_time"},
		{"weight_body_mass", "body_mass"},
		{"apple_sleeping_wrist_temperature", "sleeping_wrist_temperature"},
	} {
		if metric.Canonical(al.from) != al.to {
			t.Errorf("别名 %s 没收敛到 %s", al.from, al.to)
		}
	}
}

// labelEquivalents 登记「写法不同、数值等价」的单位：字典的 unit 是界面上徽标的
// 字面量，上游用另一个写法时 ConvertUnit 原样返回来源单位，这不算换算、也不该换算
// （改写它等于篡改原文）。
//
// 表里没有的组合意味着上游换了口径（iOS 换区域就会换 kJ/kcal、mi/km 这类），
// 此时测试先红，而不是把一个没把握的系数乘上去。
var labelEquivalents = map[string][]string{
	"bpm":         {"count/min"}, // 心率类在 HealthKit 里的标识就是 count/min
	"breaths/min": {"count/min"}, // 呼吸频率同一写法，只是界面口径换成「次/分」
	"mL/min·kg":   {"ml/(kg·min)"},
}

// 上游单位与字典口径的关系必须逐指标成立：相同、能换算、或登记为等价写法。
func TestUpstreamUnitsResolve(t *testing.T) {
	reg := metric.New(metric.Defaults())
	for _, m := range section(t, loadShapes(t), "metrics") {
		mo := m.(map[string]any)
		raw, _ := mo["name"].(string)
		name := metric.Canonical(raw)
		if raw == "blood_pressure" {
			name = "blood_pressure_systolic"
		}
		def, ok := reg.Get(name)
		if !ok {
			continue
		}
		from, _ := mo["units"].(string)
		if _, got := metric.ConvertUnit(100, from, def.Unit); got == def.Unit {
			continue // 相同或已换算
		}
		known := false
		for _, a := range labelEquivalents[def.Unit] {
			if strings.EqualFold(a, from) {
				known = true
			}
		}
		if !known {
			t.Errorf("%s：上游单位 %q 既不在换算表里也没登记为等价写法（字典口径 %q）——"+
				"值会带着上游单位进库、按字典单位渲染", name, from, def.Unit)
		}
	}
}

// 上游发来的每一个运动键，要么被结构体收走，要么在这里写明「知道它、选择不收」。
// 没有这份清单时，新增或改变形态的上游字段是**静默**消失的：
// 一年没人发现基础能量数组与内嵌轨迹根本没入库。
func TestUpstreamWorkoutKeysAreAccountedFor(t *testing.T) {
	ignored := map[string]string{
		// 多段运动的分段口径。每段带的 metrics 与运动级字段是同一批量的另一种切法，
		// 收下来要先回答「界面按哪一层显示」，那是另一个决定；先写明知道它存在。
		"activities": "多段运动的每段指标，未接入展示",
		"events":     "暂停/恢复等事件（一年 715 个），会影响时长口径，未接入展示",
		"segments":   "分段（一年 678 段），与 splits 重叠，splits 已按原文留存",
		"metadata":   "实测恒为空对象",
	}
	parsed := jsonTags(reflect.TypeOf(Workout{}))
	seen := map[string]bool{}
	var keys []string
	for _, w := range section(t, loadShapes(t), "workouts") {
		for k := range w.(map[string]any) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		if !parsed[k] {
			if _, ok := ignored[k]; !ok {
				t.Errorf("上游运动字段 %q 既没被 Workout 解析，也没登记为「知道但不收」", k)
			}
		}
	}
	// 登记了、上游却不再发的条目也要报出来：这份清单的意义是把「静默丢弃」变成显式决定，
	// 上游删掉字段后还留着条目，会让人以为它还在发。
	for k := range ignored {
		if !seen[k] {
			t.Errorf("忽略清单里的 %q 在样例里不存在，条目该删或样例该补", k)
		}
	}
}

// 心电同理：逐点 date 是故意不解析的（时轴由 start + 1/samplingFrequency 推），
// 其余键必须有归宿。
func TestUpstreamECGKeysAreAccountedFor(t *testing.T) {
	parsed := jsonTags(reflect.TypeOf(ECG{}))
	vmParsed := jsonTags(reflect.TypeOf(VoltageMeasurement{}))
	for _, e := range section(t, loadShapes(t), "ecg") {
		em, _ := e.(map[string]any)
		for k := range em {
			if !parsed[k] {
				t.Errorf("上游心电字段 %q 没被 ECG 解析", k)
			}
		}
		vm, _ := em["voltageMeasurements"].([]any)
		for k := range vm[0].(map[string]any) {
			if !vmParsed[k] && k != "date" {
				t.Errorf("心电逐点字段 %q 没被解析（date 是唯一有意不解析的那个）", k)
			}
		}
	}
}

// 整份样例跑一遍归一化：不许有未知指标、不许有丢弃，且每一类都要有归宿。
//
// 「解析没报错」和「数据进了库」是两件事，这里守的是后者——归一化产出 0 条样本、
// 0 段轨迹，跟字段名拼错的效果一样安静。
func TestUpstreamShapesNormalizeCompletely(t *testing.T) {
	f, err := os.Open(shapesFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := ParsePayload(f)
	if err != nil {
		t.Fatalf("样例解析失败: %v", err)
	}
	home, _ := time.LoadLocation("Asia/Shanghai")
	res := Normalize(p, metric.New(metric.Defaults()), home)

	if len(res.Unknown) != 0 {
		t.Fatalf("出现未登记指标: %v", res.Unknown)
	}
	if res.Skipped != 0 {
		t.Fatalf("有记录被丢弃: skipped=%d", res.Skipped)
	}
	if len(res.Sleep) != 1 {
		t.Fatalf("睡眠应产出 1 条会话，实际 %d", len(res.Sleep))
	}
	// 40 类里 sleep_analysis 走会话表、blood_pressure 拆两条，其余各 1 条样本。
	if got := len(res.Samples); got != 40 {
		t.Fatalf("样本条数应为 40（38 类各 1 + 血压 2），实际 %d", got)
	}

	values := map[string]float64{}
	metas := map[string]map[string]any{}
	for _, s := range res.Samples {
		values[s.Metric] = s.Value
		metas[s.Metric] = s.Meta
	}
	// 类别型指标没有 qty，值写在子类型键里：合并成总数（0+1+2），明细留在 meta。
	if values["sexual_activity"] != 3 {
		t.Fatalf("性生活应按子类型求和，实际 %v", values["sexual_activity"])
	}
	sub, _ := metas["sexual_activity"]["subtypes"].(map[string]float64)
	if len(sub) != 3 {
		t.Fatalf("子类型明细应留在 meta: %v", metas["sexual_activity"])
	}
	// kJ 必须已换成字典口径 kcal：带上游单位进库、按字典单位渲染会虚高 4.18 倍。
	if d := values["active_energy"] - 12.5/4.184; d > 1e-9 || d < -1e-9 {
		t.Fatalf("活动能量 12.5 kJ 应换算成 %.6f kcal，实际 %v", 12.5/4.184, values["active_energy"])
	}

	// 卧床时长：上游恒给 0，必须由起止时刻补算。
	if hr := res.Sleep[0].InBedHr; hr == nil || *hr <= 0 {
		t.Fatalf("inBed=0 时应按 inBedStart/End 补算，实际 %v", hr)
	}

	w := res.Workouts[0]
	if w.Route == nil || len(w.Route.Points) != 3 {
		t.Fatalf("轨迹应留 3 个点（(0,0) 那个丢弃），实际 %+v", w.Route)
	}
	series := map[string]int{}
	for _, s := range w.Series {
		series[s.Metric] = len(s.Points)
	}
	for name, want := range map[string]int{
		"step_count": 2, "active_energy": 1, "basal_energy_burned": 2,
		"walking_running_distance": 1, "cycling_distance": 1,
	} {
		if series[name] != want {
			t.Errorf("逐分钟数组 %s 应留 %d 个点，实际 %d（缺 qty 的点单独丢，不压实时间轴）",
				name, want, series[name])
		}
	}
	// 早于运动起点的点保留成负秒数（实测上游第一点常比运动开始早几十秒）。
	if len(w.HR) != 2 || w.HR[1].AtSec != -20 {
		t.Fatalf("早于起点的心率点应保留为负秒数: %+v", w.HR)
	}
	// 运动级标量进 meta：界面上「这次运动最低心率是多少」只能从这里回答。
	for _, key := range []string{"min_heart_rate", "speed", "step_cadence", "intensity",
		"estimated_effort_score", "upstream_id", "basal_energy", "step_count"} {
		if _, ok := w.Meta[key]; !ok {
			t.Errorf("运动字段 %q 没有落到 workouts.meta", key)
		}
	}
}

// 硬编码的序列口径不许与字典漂移：seriesTargetUnits 的名字与单位都是抄字典的，
// 抄错了不报错，只会让 workout_metric_series 里的行按一个对不上的名字或单位存着。
func TestSeriesTargetUnitsMatchRegistry(t *testing.T) {
	reg := metric.New(metric.Defaults())
	for name, unit := range seriesTargetUnits {
		def, ok := reg.Get(name)
		if !ok {
			t.Errorf("seriesTargetUnits 里的 %q 不在字典中", name)
			continue
		}
		if def.Unit != unit {
			t.Errorf("%s：字典单位是 %q，序列口径写成 %q", name, def.Unit, unit)
		}
	}
}

// jsonTags 反射出结构体认识的 JSON 键。
//
// 手写这份清单迟早会与字段漂移（payload.go 里 pointKnownKeys 同一条理由），
// 而漂移的后果是「已经解析的字段被当成没收」或反过来，两边都不报错。
func jsonTags(tp reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < tp.NumField(); i++ {
		tag := tp.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		out[strings.Split(tag, ",")[0]] = true
	}
	return out
}
