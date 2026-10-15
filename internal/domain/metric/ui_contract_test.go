package metric

import (
	"os"
	"regexp"
	"testing"
)

// metricGroupsPath 是趋势页的指标分组配置（前端）。
const metricGroupsPath = "../../../web/src/lib/metric-groups.ts"

// metricDirectionPath 是概览页的涨跌方向表（前端）。
const metricDirectionPath = "../../../web/src/lib/metric-direction.ts"

// TestDefaultsMetricsAreListedInTrendsUI 是字典与趋势页入口名单之间的一致性契约。
//
// 为什么需要这条测试：趋势页的选择器是
//
//	defs.filter((d) => group.metrics.includes(d.metric) && d.enabled)
//
// 也就是「以 metric-groups.ts 为准入名单」。没写进任何分组的指标**在界面上完全没有入口**：
// 数据在库里、接口也返回，但用户既画不出图也搜不到指标名——表现为「同步成功但找不到数据」，
// 排查时很容易误判成入库问题。
//
// 前后端各维护一份名单必然漂移（这次就是：字典补齐了 15 个指标，
// 前端名单没跟上，等于白补），所以在这里做交叉校验。
func TestDefaultsMetricsAreListedInTrendsUI(t *testing.T) {
	src, err := os.ReadFile(metricGroupsPath)
	if err != nil {
		t.Skipf("读不到前端分组配置（%v），跳过跨语言校验", err)
	}
	text := string(src)

	inUI := map[string]bool{}
	for _, m := range regexp.MustCompile(`metrics:\s*\[([^\]]*)\]`).FindAllStringSubmatch(text, -1) {
		for _, name := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(m[1], -1) {
			inUI[name[1]] = true
		}
	}
	if len(inUI) == 0 {
		t.Fatalf("没能从 %s 解析出任何指标名，解析规则需要更新", metricGroupsPath)
	}

	inDict := map[string]bool{}
	for _, d := range Defaults() {
		inDict[d.Metric] = true
		if !inUI[d.Metric] {
			t.Errorf("字典里的 %q 没写进 metric-groups.ts，趋势页上不会有入口（等于白登记）", d.Metric)
		}
	}
	// 反向：前端名单里不该有字典外的名字。留着只会让选择器出现一个永远无数据的 chip
	// （别名的旧名就属于这一类，应该由 alias.go 收敛掉）。
	var stale []string
	for name := range inUI {
		if !inDict[name] {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		t.Errorf("metric-groups.ts 里有字典外的指标名（选择器会出现永远无数据的 chip）: %v", stale)
	}
}

// TestDefaultsUnitsHaveDisplayDigits 锁住「字典的单位写法 = 前端认得的单位写法」。
//
// 前端按 preferred_unit 查小数位表，查不到就按 0 位显示——
// 于是 0.2811 m/s 会显示成「0」，看起来像数据坏了。单位写法必须两边一致。
func TestDefaultsUnitsHaveDisplayDigits(t *testing.T) {
	src, err := os.ReadFile(metricGroupsPath)
	if err != nil {
		t.Skipf("读不到前端分组配置（%v），跳过跨语言校验", err)
	}
	text := string(src)

	body := regexp.MustCompile(`(?s)UNIT_DIGITS[^=]*=\s*\{(.*?)\n\};`).FindStringSubmatch(text)
	if body == nil {
		t.Fatalf("没能从 %s 解析出 UNIT_DIGITS，解析规则需要更新", metricGroupsPath)
	}
	known := map[string]bool{}
	// 键有两种写法：带引号（含 % · / 等特殊字符）与裸标识符
	for _, m := range regexp.MustCompile(`(?:'([^']+)'|([A-Za-z_][\w·/]*))\s*:`).FindAllStringSubmatch(body[1], -1) {
		if m[1] != "" {
			known[m[1]] = true
		} else {
			known[m[2]] = true
		}
	}
	if len(known) == 0 {
		t.Fatal("UNIT_DIGITS 里一个键都没解析出来")
	}

	seen := map[string]bool{}
	for _, d := range Defaults() {
		if seen[d.PreferredUnit] {
			continue
		}
		seen[d.PreferredUnit] = true
		if !known[d.PreferredUnit] {
			t.Errorf("单位 %q（%s）没写进 UNIT_DIGITS，图表会按 0 位小数显示", d.PreferredUnit, d.Metric)
		}
	}
}

// TestMetricDirectionKeysAreKnown 校验方向表与字典的一致性。
//
// 为什么需要这条：方向表里写错一个字母（stepcount 而不是 step_count），
// 那个指标会静默掉进「中性」分支——卡片依然有数字、有百分比，只是永远不上色。
// 这种错误在开发机上几乎不可能被发现（多数指标长期落在 normal 区间里不上色也很正常），
// 等用户发现「步数涨了怎么没变绿」时，已经没人想过要去查这张表了。
func TestMetricDirectionKeysAreKnown(t *testing.T) {
	src, err := os.ReadFile(metricDirectionPath)
	if err != nil {
		t.Skipf("读不到前端方向表（%v），跳过跨语言校验", err)
	}
	inDict := map[string]bool{}
	for _, d := range Defaults() {
		inDict[d.Metric] = true
	}

	// 只认「整行就是一个带引号的指标名加逗号」，注释段里的 'up-good' 之类不会命中
	re := regexp.MustCompile(`(?m)^\s*'([a-z][a-z0-9_]*)',$`)
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		name := m[1]
		if seen[name] {
			t.Errorf("方向表里 %q 重复登记（后写的会覆盖前面的）", name)
		}
		seen[name] = true
		if !inDict[name] {
			t.Errorf("方向表里的 %q 不在指标字典里（多半是笔误，会让该指标永远显示成中性色）", name)
		}
	}
	if len(seen) == 0 {
		t.Fatalf("没能从 %s 解析出任何指标名，解析规则需要更新", metricDirectionPath)
	}
}

// TestDisplayDigitsHasNoStaleUnits 前端单位表里不该有字典根本不用的写法——那通常意味着单位被改过而另一边没跟上。
func TestDisplayDigitsHasNoStaleUnits(t *testing.T) {
	src, err := os.ReadFile(metricGroupsPath)
	if err != nil {
		t.Skipf("读不到前端分组配置（%v），跳过跨语言校验", err)
	}
	text := string(src)
	body := regexp.MustCompile(`(?s)UNIT_DIGITS[^=]*=\s*\{(.*?)\n\};`).FindStringSubmatch(text)
	if body == nil {
		t.Fatalf("没能从 %s 解析出 UNIT_DIGITS", metricGroupsPath)
	}
	used := map[string]bool{}
	for _, d := range Defaults() {
		used[d.PreferredUnit] = true
	}
	for _, m := range regexp.MustCompile(`(?:'([^']+)'|([A-Za-z_][\w·/]*))\s*:`).FindAllStringSubmatch(body[1], -1) {
		key := m[1]
		if key == "" {
			key = m[2]
		}
		if !used[key] {
			t.Errorf("UNIT_DIGITS 里的 %q 字典里已没有指标使用，考虑删掉", key)
		}
	}
}
