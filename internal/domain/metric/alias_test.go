package metric

import "testing"

// 别名表最重要的不变式：收敛目标必须是字典里真实存在的指标。
// 指错了方向的后果是静默的——数据改个名字进库，字典那边照样没有卡片，
// 比不做收敛还难查（旧的未知指标至少还会出现在 Unknown 列表里）。
func TestAliasTargetsExistInDefaults(t *testing.T) {
	known := map[string]bool{}
	for _, d := range Defaults() {
		known[d.Metric] = true
	}
	for alias, canonical := range Aliases() {
		if !known[canonical] {
			t.Errorf("别名 %q 收敛到 %q，但字典里没有这个指标", alias, canonical)
		}
		if known[alias] {
			t.Errorf("别名 %q 自己也在字典里，会让同一个概念出现两条定义（界面出现同名 chip）", alias)
		}
	}
}

// 收敛必须幂等且可重入：Canonical 的结果再喂回去不能再变，
// 否则「先规范化再查字典」和「查字典时规范化」会得到不同答案。
func TestCanonicalIsIdempotent(t *testing.T) {
	for alias := range Aliases() {
		once := Canonical(alias)
		if twice := Canonical(once); twice != once {
			t.Errorf("Canonical(%q) = %q，再收敛变成 %q", alias, once, twice)
		}
	}
}

// 首尾空白不该让收敛失效：来源名里混 NBSP 是常态，指标名同样要容忍手改出的空格。
func TestCanonicalTrimsSurroundingSpace(t *testing.T) {
	if got := Canonical("  blood_oxygen_saturation "); got != "oxygen_saturation" {
		t.Fatalf("带空白应照样收敛，实际 %q", got)
	}
}

func TestCanonicalPassesThroughUnknown(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"step_count", "step_count"},
		{"sleep_analysis", "sleep_analysis"},
		// 语义不同的近名指标不能被顺手合并：
		// 站立分钟数与站立小时数量级、单位都不同。
		{"apple_stand_hour", "stand_hour"},
		{"apple_stand_time", "stand_time"},
		{"stand_hour", "stand_hour"},
		{"stand_time", "stand_time"},
	}
	for _, tc := range cases {
		if got := Canonical(tc.in); got != tc.want {
			t.Errorf("Canonical(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// 站立小时数与站立时长必须是两个不同的指标，且都在字典里。
// 这条是防止有人「顺手」把 apple_stand_hour 也塞给 stand_time。
func TestStandHourIsNotStandTime(t *testing.T) {
	if Canonical("apple_stand_hour") == Canonical("apple_stand_time") {
		t.Fatal("站立小时数与站立时长是不同量（count vs min），不能收敛到同一个 metric")
	}
	name := Canonical("apple_stand_hour")
	for _, d := range Defaults() {
		if d.Metric == name {
			return
		}
	}
	t.Fatalf("站立小时数 %q 必须在字典里，否则界面上没有入口", name)
}

// Aliases 返回副本：调用方改坏了不能影响字典本身。
func TestAliasesReturnsCopy(t *testing.T) {
	got := Aliases()
	for k := range got {
		delete(got, k)
	}
	if len(Aliases()) == 0 {
		t.Fatal("Aliases 返回的必须是副本，外部清空不该影响内置表")
	}
}
