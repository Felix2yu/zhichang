package sample

import "testing"

// CleanSource 是「空白等价」变换，不是语义归一：这一组锁定它能做什么、不能做什么。
func TestCleanSourceNormalizesInvisibleChars(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// 真实导出里 249 个点位的形态：NBSP 而不是普通空格。
		{"真实导出的 NBSP", "小明的Apple\u00a0Watch", "小明的Apple Watch"},
		{"普通空格不变", "小明的Apple Watch", "小明的Apple Watch"},
		{"表意空格收成普通空格", "小明的Apple\u3000Watch", "小明的Apple Watch"},
		{"窄空格", "Apple\u2009Watch", "Apple Watch"},
		{"无间隔空格", "Apple\u202fWatch", "Apple Watch"},
		{"数字空格", "Apple\u2007Watch", "Apple Watch"},
		{"制表与换行", "Apple\t\nWatch", "Apple Watch"},
		{"连续空白压缩", "Apple   Watch", "Apple Watch"},
		{"首尾空白去掉", "  Apple Watch  ", "Apple Watch"},
		{"混合空白", "\u00a0小明的 \t Apple\u3000Watch\u00a0", "小明的 Apple Watch"},

		// 零宽字符不是空白，必须整个删掉——否则 "Apple Watch" 会分裂成 "AppleWatch"。
		{"零宽空格删除", "Apple\u200bWatch", "AppleWatch"},
		{"零宽连接符删除", "Apple\u200dWatch", "AppleWatch"},
		{"零宽非连接符删除", "Apple\u200cWatch", "AppleWatch"},
		{"词连接符删除", "Apple\u2060Watch", "AppleWatch"},
		{"BOM 删除", "\ufeffApple Watch", "Apple Watch"},
		{"零宽字符夹在空白中", "Apple \u200b Watch", "Apple Watch"},

		// 多来源分隔符：HAE 把这组来源拼成一个串，且管两侧空格不固定。
		{"管两侧无空格", "小明的Apple Watch|小明的iPhone", "小明的Apple Watch | 小明的iPhone"},
		{"管左侧有空格", "小明的Apple Watch |小明的iPhone", "小明的Apple Watch | 小明的iPhone"},
		{"管两侧都有空格（含 NBSP）", "血氧\u00a0| 小明的Apple\u00a0Watch", "血氧 | 小明的Apple Watch"},
		{"空段丢弃", "a||b", "a | b"},
		{"首尾管丢弃", "|a|", "a"},

		{"空串", "", ""},
		{"全空白", "\u00a0\u3000", ""},
		{"全零宽字符", "\u200b\ufeff", ""},

		// 可见字符一律不碰：不折叠大小写、不删标点、不改语义。
		{"大小写不折叠", "apple watch", "apple watch"},
		{"标点保留", "某某健康App（备用）", "某某健康App（备用）"},
		{"中文正常", "移动心电图房颤提示软件", "移动心电图房颤提示软件"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CleanSource(c.in); got != c.want {
				t.Fatalf("CleanSource(%q) = %q，期望 %q", c.in, got, c.want)
			}
		})
	}
}

// 幂等：归一后的值再归一必须不变，否则「同一来源写两次」会持续产生新值。
func TestCleanSourceIsIdempotent(t *testing.T) {
	inputs := []string{
		"小明的Apple\u00a0Watch",
		"Apple\u200bWatch",
		"  a | b  ",
		"小明的Apple\u00a0Watch|小明的iPhone",
		"",
	}
	for _, in := range inputs {
		once := CleanSource(in)
		if twice := CleanSource(once); twice != once {
			t.Fatalf("不幂等：%q → %q → %q", in, once, twice)
		}
	}
}

// 真实导出里出现的全部形态都应落到同一个规范名上，避免它们在 sources 页
// 显示成多条「看起来一样」的记录。
func TestCleanSourceCollapsesRealExportVariants(t *testing.T) {
	groups := map[string][]string{
		"小明的Apple Watch": {
			"小明的Apple\u00a0Watch",
			"小明的Apple Watch",
			"小明的Apple\u3000Watch",
			"小明的Apple \u200bWatch",
		},
		"小明的Apple Watch | 小明的iPhone": {
			"小明的Apple\u00a0Watch|小明的iPhone",
			"小明的Apple Watch | 小明的iPhone",
			"小明的Apple Watch\u00a0|\u00a0小明的iPhone",
		},
		"血氧 | 小明的Apple Watch": {
			"血氧 | 小明的Apple\u00a0Watch",
			"血氧|小明的Apple Watch",
		},
	}
	for want, variants := range groups {
		for _, v := range variants {
			if got := CleanSource(v); got != want {
				t.Errorf("CleanSource(%q) = %q，期望收敛到 %q", v, got, want)
			}
		}
	}
}
