package config

import (
	"strings"
	"testing"
	"time"
)

// 所有环境变量键，测试前一律清空，避免宿主机的 ZHICHANG_* 污染断言结果。
var allKeys = []string{
	"HTTP_ADDR", "DB_DSN", "HOME_TZ", "INGEST_TOKEN", "SETTINGS_TOKEN", "DEDUP_MODE",
	"RAW_ARCHIVE_DIR", "MAX_BODY_BYTES", "MIGRATE_ON_BOOT", "LOG_LEVEL",
	"ROLLUP_WORKERS", "ROLLUP_IDLE", "RETENTION_COMPRESS_AFTER",
	"MQTT_ENABLED", "MQTT_BROKER", "MQTT_TOPIC", "MQTT_CLIENT_ID", "MQTT_USERNAME", "MQTT_PASSWORD",
	"MCP_ENABLED", "MCP_TOKEN", "MCP_PATH",
}

func resetEnv(t *testing.T) {
	t.Helper()
	for _, k := range allKeys {
		t.Setenv("ZHICHANG_"+k, "")
	}
}

// setOn 只打开关心的变量：token 缺失会让 Load 直接失败，所以每个用例默认带上。
func setOn(t *testing.T, kv map[string]string) {
	t.Helper()
	resetEnv(t)
	t.Setenv("ZHICHANG_INGEST_TOKEN", "secret")
	for k, v := range kv {
		t.Setenv("ZHICHANG_"+k, v)
	}
}

func TestLoadDefaults(t *testing.T) {
	resetEnv(t)
	t.Setenv("ZHICHANG_INGEST_TOKEN", "secret")

	c, err := Load()
	if err != nil {
		t.Fatalf("默认配置应加载成功: %v", err)
	}
	// 这些默认值就是部署文档里对外的承诺，任何改动都应当让这里变红。
	wants := []struct {
		name string
		got  any
		want any
	}{
		{"HTTPAddr", c.HTTPAddr, ":8787"},
		{"DBDSN", c.DBDSN, "postgres://postgres:postgres@127.0.0.1:5432/zhichang?sslmode=disable"},
		{"HomeTZ", c.HomeTZ, "Asia/Shanghai"},
		{"IngestToken", c.IngestToken, "secret"},
		{"DedupMode", c.DedupMode, "strict"},
		{"RawArchiveDir", c.RawArchiveDir, ""},
		{"MaxBodyBytes", c.MaxBodyBytes, int64(512 << 20)},
		{"MigrateOnBoot", c.MigrateOnBoot, true},
		{"SettingsToken", c.SettingsToken, ""},
		{"LogLevel", c.LogLevel, "info"},
		{"RollupWorkers", c.RollupWorkers, 2},
		{"RollupIdle", c.RollupIdle, 2 * time.Second},
		{"Retention.CompressAfter", c.Retention.CompressAfter, "30 days"},
		{"MQTT.Enabled", c.MQTT.Enabled, false},
		{"MQTT.Broker", c.MQTT.Broker, ""},
		{"MQTT.Topic", c.MQTT.Topic, "zhichang/hae/#"},
		{"MQTT.ClientID", c.MQTT.ClientID, "zhichangd"},
		{"MQTT.Username", c.MQTT.Username, ""},
		{"MQTT.Password", c.MQTT.Password, ""},
		{"MCP.Enabled", c.MCP.Enabled, true},
		{"MCP.Token", c.MCP.Token, ""},
		{"MCP.Path", c.MCP.Path, "/mcp"},
	}
	for _, w := range wants {
		if w.got != w.want {
			t.Errorf("%s 默认值 = %v，期望 %v", w.name, w.got, w.want)
		}
	}
	if c.MaxBodyBytes != 536870912 {
		t.Errorf("MaxBodyBytes 应为 512MiB，实际 %d", c.MaxBodyBytes)
	}
}

func TestLoadOverrides(t *testing.T) {
	cases := []struct {
		key, val string
		check    func(t *testing.T, c Config)
	}{
		{"HTTP_ADDR", ":9000", func(t *testing.T, c Config) {
			if c.HTTPAddr != ":9000" {
				t.Errorf("HTTPAddr = %q", c.HTTPAddr)
			}
		}},
		{"DB_DSN", "postgres://x", func(t *testing.T, c Config) {
			if c.DBDSN != "postgres://x" {
				t.Errorf("DBDSN = %q", c.DBDSN)
			}
		}},
		{"HOME_TZ", "UTC", func(t *testing.T, c Config) {
			if c.HomeTZ != "UTC" {
				t.Errorf("HomeTZ = %q", c.HomeTZ)
			}
		}},
		{"DEDUP_MODE", "latest", func(t *testing.T, c Config) {
			if c.DedupMode != "latest" {
				t.Errorf("DedupMode = %q", c.DedupMode)
			}
		}},
		{"RAW_ARCHIVE_DIR", "/var/archive", func(t *testing.T, c Config) {
			if c.RawArchiveDir != "/var/archive" {
				t.Errorf("RawArchiveDir = %q", c.RawArchiveDir)
			}
		}},
		{"SETTINGS_TOKEN", "settings-secret", func(t *testing.T, c Config) {
			if c.SettingsToken != "settings-secret" {
				t.Errorf("SettingsToken = %q", c.SettingsToken)
			}
		}},
		{"MAX_BODY_BYTES", "1048576", func(t *testing.T, c Config) {
			if c.MaxBodyBytes != 1<<20 {
				t.Errorf("MaxBodyBytes = %d", c.MaxBodyBytes)
			}
		}},
		{"MIGRATE_ON_BOOT", "false", func(t *testing.T, c Config) {
			if c.MigrateOnBoot {
				t.Error("MigrateOnBoot 应为 false")
			}
		}},
		{"LOG_LEVEL", "debug", func(t *testing.T, c Config) {
			if c.LogLevel != "debug" {
				t.Errorf("LogLevel = %q", c.LogLevel)
			}
		}},
		{"ROLLUP_WORKERS", "8", func(t *testing.T, c Config) {
			if c.RollupWorkers != 8 {
				t.Errorf("RollupWorkers = %d", c.RollupWorkers)
			}
		}},
		{"ROLLUP_IDLE", "500ms", func(t *testing.T, c Config) {
			if c.RollupIdle != 500*time.Millisecond {
				t.Errorf("RollupIdle = %s", c.RollupIdle)
			}
		}},
		{"RETENTION_COMPRESS_AFTER", "7 days", func(t *testing.T, c Config) {
			if c.Retention.CompressAfter != "7 days" {
				t.Errorf("CompressAfter = %q", c.Retention.CompressAfter)
			}
		}},
		{"MQTT_ENABLED", "true", func(t *testing.T, c Config) {
			if !c.MQTT.Enabled {
				t.Error("MQTT.Enabled 应为 true")
			}
		}},
		{"MQTT_BROKER", "tcp://127.0.0.1:1883", func(t *testing.T, c Config) {
			if c.MQTT.Broker != "tcp://127.0.0.1:1883" {
				t.Errorf("MQTT.Broker = %q", c.MQTT.Broker)
			}
		}},
		{"MQTT_TOPIC", "custom/#", func(t *testing.T, c Config) {
			if c.MQTT.Topic != "custom/#" {
				t.Errorf("MQTT.Topic = %q", c.MQTT.Topic)
			}
		}},
		{"MQTT_CLIENT_ID", "otherd", func(t *testing.T, c Config) {
			if c.MQTT.ClientID != "otherd" {
				t.Errorf("MQTT.ClientID = %q", c.MQTT.ClientID)
			}
		}},
		{"MQTT_USERNAME", "u", func(t *testing.T, c Config) {
			if c.MQTT.Username != "u" {
				t.Errorf("MQTT.Username = %q", c.MQTT.Username)
			}
		}},
		{"MQTT_PASSWORD", "p", func(t *testing.T, c Config) {
			if c.MQTT.Password != "p" {
				t.Errorf("MQTT.Password = %q", c.MQTT.Password)
			}
		}},
		{"MCP_ENABLED", "false", func(t *testing.T, c Config) {
			if c.MCP.Enabled {
				t.Error("MCP.Enabled 应为 false")
			}
		}},
		{"MCP_TOKEN", "mcp-secret", func(t *testing.T, c Config) {
			if c.MCP.Token != "mcp-secret" {
				t.Errorf("MCP.Token = %q", c.MCP.Token)
			}
		}},
		{"MCP_PATH", "/mcp/v2", func(t *testing.T, c Config) {
			if c.MCP.Path != "/mcp/v2" {
				t.Errorf("MCP.Path = %q", c.MCP.Path)
			}
		}},
		// boolEnv 解析失败时静默回落默认值：布尔开关写错不该让服务起不来。
		{"MQTT_ENABLED", "notabool", func(t *testing.T, c Config) {
			if c.MQTT.Enabled {
				t.Error("MQTT_ENABLED 非法值应回落 false")
			}
		}},
		{"MCP_ENABLED", "notabool", func(t *testing.T, c Config) {
			if !c.MCP.Enabled {
				t.Error("MCP_ENABLED 非法值应回落 true")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.val, func(t *testing.T) {
			setOn(t, map[string]string{tc.key: tc.val})
			c, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			tc.check(t, c)
		})
	}
}

// get() 会 TrimSpace：docker-compose / systemd 里带空格的值是常见写法。
func TestLoadTrimsWhitespace(t *testing.T) {
	setOn(t, map[string]string{
		"HTTP_ADDR":      "  :9100  ",
		"MAX_BODY_BYTES": "  4096 ",
		"MCP_PATH":       "  /mcp  ",
		"HOME_TZ":        " UTC ",
	})
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HTTPAddr != ":9100" || c.MCP.Path != "/mcp" || c.HomeTZ != "UTC" || c.MaxBodyBytes != 4096 {
		t.Fatalf("未去掉首尾空格: %+v", c)
	}
}

// INGEST_TOKEN 只有空格时等同于缺失——否则空 token 会打开一个无鉴权写入口。
func TestLoadBlankTokenIsMissing(t *testing.T) {
	resetEnv(t)
	t.Setenv("ZHICHANG_INGEST_TOKEN", "   ")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "必须设置 ZHICHANG_INGEST_TOKEN") {
		t.Fatalf("纯空格 token 应视为缺失，实际: %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name    string
		kv      map[string]string
		wantSub string
	}{
		{"MaxBodyBytes 非数字", map[string]string{"MAX_BODY_BYTES": "512MB"}, "ZHICHANG_MAX_BODY_BYTES 非法"},
		{"MaxBodyBytes 负数格式错误", map[string]string{"MAX_BODY_BYTES": "1.5"}, "ZHICHANG_MAX_BODY_BYTES 非法"},
		{"MigrateOnBoot 非布尔", map[string]string{"MIGRATE_ON_BOOT": "yes-ish"}, "ZHICHANG_MIGRATE_ON_BOOT 非法"},
		{"RollupWorkers 非数字", map[string]string{"ROLLUP_WORKERS": "auto"}, "ZHICHANG_ROLLUP_WORKERS 非法"},
		{"RollupIdle 非法时长", map[string]string{"ROLLUP_IDLE": "2 seconds"}, "ZHICHANG_ROLLUP_IDLE 非法"},
		{"DedupMode 非白名单", map[string]string{"DEDUP_MODE": "smart"}, "ZHICHANG_DEDUP_MODE 必须是 strict 或 latest"},
		{"HomeTZ 不存在", map[string]string{"HOME_TZ": "Mars/Olympus_Mons"}, "ZHICHANG_HOME_TZ 非法"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setOn(t, tc.kv)
			_, err := Load()
			if err == nil {
				t.Fatalf("应报错，期望包含 %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("错误信息 %q 应包含 %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// 报错时 Load 返回的是"已解析到一半"的配置：字段仍带默认值，方便调用方打日志。
func TestLoadErrorStillReturnsDefaults(t *testing.T) {
	setOn(t, map[string]string{"MAX_BODY_BYTES": "oops"})
	c, err := Load()
	if err == nil {
		t.Fatal("应报错")
	}
	if c.HTTPAddr != ":8787" || c.HomeTZ != "Asia/Shanghai" {
		t.Fatalf("出错路径应保留默认值: %+v", c)
	}
}

// LoadForCLI 的语义要钉住：它唯一放过的是"只缺 INGEST_TOKEN"，
// 因为运维子命令不写入数据；其他配置错误必须照报，否则 migrate 会带错配置跑。
func TestLoadForCLI(t *testing.T) {
	t.Run("缺 token 时放行并保留公共字段", func(t *testing.T) {
		resetEnv(t)
		t.Setenv("ZHICHANG_HTTP_ADDR", ":9999")
		t.Setenv("ZHICHANG_HOME_TZ", "UTC")
		t.Setenv("ZHICHANG_RAW_ARCHIVE_DIR", "/tmp/raw")
		t.Setenv("ZHICHANG_LOG_LEVEL", "warn")
		t.Setenv("ZHICHANG_RETENTION_COMPRESS_AFTER", "3 days")
		t.Setenv("ZHICHANG_MIGRATE_ON_BOOT", "false")

		c, err := LoadForCLI()
		if err != nil {
			t.Fatalf("只缺 INGEST_TOKEN 不应报错: %v", err)
		}
		if c.HTTPAddr != ":9999" || c.HomeTZ != "UTC" || c.RawArchiveDir != "/tmp/raw" ||
			c.LogLevel != "warn" || c.Retention.CompressAfter != "3 days" || c.MigrateOnBoot {
			t.Fatalf("CLI 配置丢字段: %+v", c)
		}
		if c.DedupMode != "strict" || c.DBDSN == "" {
			t.Fatalf("CLI 配置应保留默认口径: %+v", c)
		}
		if c.IngestToken != "" {
			t.Fatalf("CLI 不应携带写入 token: %q", c.IngestToken)
		}
	})

	t.Run("其他错误照报", func(t *testing.T) {
		cases := []struct{ key, val, wantSub string }{
			{"MAX_BODY_BYTES", "nope", "ZHICHANG_MAX_BODY_BYTES 非法"},
			{"DEDUP_MODE", "smart", "ZHICHANG_DEDUP_MODE 必须是 strict 或 latest"},
			{"HOME_TZ", "Mars/Olympus_Mons", "ZHICHANG_HOME_TZ 非法"},
			{"ROLLUP_IDLE", "2 seconds", "ZHICHANG_ROLLUP_IDLE 非法"},
		}
		for _, tc := range cases {
			t.Run(tc.key, func(t *testing.T) {
				resetEnv(t)
				// 同时缺 token：错误必须来自配置本身，而不是被 token 分支吞掉。
				t.Setenv("ZHICHANG_"+tc.key, tc.val)
				_, err := LoadForCLI()
				if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
					t.Fatalf("应报 %q，实际: %v", tc.wantSub, err)
				}
			})
		}
	})

	t.Run("配置完整时等价于 Load", func(t *testing.T) {
		setOn(t, map[string]string{"ROLLUP_WORKERS": "4"})
		c, err := LoadForCLI()
		if err != nil {
			t.Fatalf("LoadForCLI: %v", err)
		}
		if c.RollupWorkers != 4 || c.IngestToken != "secret" {
			t.Fatalf("完整配置应原样返回: %+v", c)
		}
	})
}
