// Package config 加载知常服务的运行时配置（全部来自环境变量）。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 是服务运行所需的全部配置。
type Config struct {
	HTTPAddr      string        `env:"HTTP_ADDR"`
	DBDSN         string        `env:"DB_DSN"`
	HomeTZ        string        `env:"HOME_TZ"`
	IngestToken   string        `env:"INGEST_TOKEN"`
	DedupMode     string        `env:"DEDUP_MODE"`
	RawArchiveDir string        `env:"RAW_ARCHIVE_DIR"`
	MaxBodyBytes  int64         `env:"MAX_BODY_BYTES"`
	MigrateOnBoot bool          `env:"MIGRATE_ON_BOOT"`
	LogLevel      string        `env:"LOG_LEVEL"`
	RollupWorkers int           `env:"ROLLUP_WORKERS"`
	RollupIdle    time.Duration `env:"ROLLUP_IDLE"`
	Retention     Retention     `env:"RETENTION_"`
	MQTT          MQTT          `env:"MQTT_"`
	MCP           MCP           `env:"MCP_"`
}

// Retention 控制压缩与保留策略。
type Retention struct {
	CompressAfter string `env:"COMPRESS_AFTER"`
}

// MQTT 为可选旁路接入。
type MQTT struct {
	Enabled  bool   `env:"ENABLED"`
	Broker   string `env:"BROKER"`
	Topic    string `env:"TOPIC"`
	ClientID string `env:"CLIENT_ID"`
	Username string `env:"USERNAME"`
	Password string `env:"PASSWORD"`
}

// MCP 控制内置 MCP 服务器。
type MCP struct {
	Enabled bool   `env:"ENABLED"`
	Token   string `env:"TOKEN"`
	Path    string `env:"PATH"`
}

const envPrefix = "ZHICHANG_"

// Load 从环境变量加载配置，缺失项使用默认值。
func Load() (Config, error) {
	c := Config{
		HTTPAddr:      ":8787",
		DBDSN:         "postgres://postgres:postgres@127.0.0.1:5432/zhichang?sslmode=disable",
		HomeTZ:        "Asia/Shanghai",
		DedupMode:     "strict",
		MaxBodyBytes:  512 << 20,
		MigrateOnBoot: true,
		LogLevel:      "info",
		RollupWorkers: 2,
		RollupIdle:    2 * time.Second,
		Retention:     Retention{CompressAfter: "30 days"},
		MCP:           MCP{Enabled: true, Path: "/mcp"},
	}

	if v := get("HTTP_ADDR"); v != "" {
		c.HTTPAddr = v
	}
	if v := get("DB_DSN"); v != "" {
		c.DBDSN = v
	}
	if v := get("HOME_TZ"); v != "" {
		c.HomeTZ = v
	}
	c.IngestToken = get("INGEST_TOKEN")
	if v := get("DEDUP_MODE"); v != "" {
		c.DedupMode = v
	}
	c.RawArchiveDir = get("RAW_ARCHIVE_DIR")
	if v := get("MAX_BODY_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return c, fmt.Errorf("ZHICHANG_MAX_BODY_BYTES 非法: %w", err)
		}
		c.MaxBodyBytes = n
	}
	if v := get("MIGRATE_ON_BOOT"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("ZHICHANG_MIGRATE_ON_BOOT 非法: %w", err)
		}
		c.MigrateOnBoot = b
	}
	if v := get("LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
	if v := get("ROLLUP_WORKERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("ZHICHANG_ROLLUP_WORKERS 非法: %w", err)
		}
		c.RollupWorkers = n
	}
	if v := get("ROLLUP_IDLE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("ZHICHANG_ROLLUP_IDLE 非法: %w", err)
		}
		c.RollupIdle = d
	}
	if v := get("RETENTION_COMPRESS_AFTER"); v != "" {
		c.Retention.CompressAfter = v
	}

	c.MQTT.Enabled = boolEnv("MQTT_ENABLED", false)
	c.MQTT.Broker = get("MQTT_BROKER")
	c.MQTT.Topic = get("MQTT_TOPIC")
	if c.MQTT.Topic == "" {
		c.MQTT.Topic = "zhichang/hae/#"
	}
	c.MQTT.ClientID = get("MQTT_CLIENT_ID")
	if c.MQTT.ClientID == "" {
		c.MQTT.ClientID = "zhichangd"
	}
	c.MQTT.Username = get("MQTT_USERNAME")
	c.MQTT.Password = get("MQTT_PASSWORD")

	c.MCP.Enabled = boolEnv("MCP_ENABLED", true)
	c.MCP.Token = get("MCP_TOKEN")
	if v := get("MCP_PATH"); v != "" {
		c.MCP.Path = v
	}

	if c.DedupMode != "strict" && c.DedupMode != "latest" {
		return c, fmt.Errorf("ZHICHANG_DEDUP_MODE 必须是 strict 或 latest，当前: %s", c.DedupMode)
	}
	if _, err := time.LoadLocation(c.HomeTZ); err != nil {
		return c, fmt.Errorf("ZHICHANG_HOME_TZ 非法: %w", err)
	}
	if c.IngestToken == "" {
		return c, fmt.Errorf("必须设置 ZHICHANG_INGEST_TOKEN（Health Auto Export 通过该 token 写入数据）")
	}
	return c, nil
}

// LoadForCLI 加载用于运维命令的配置（不强制要求 token）。
func LoadForCLI() (Config, error) {
	c, err := Load()
	if err != nil && strings.Contains(err.Error(), "INGEST_TOKEN") {
		return Config{
			HTTPAddr:      c.HTTPAddr,
			DBDSN:         c.DBDSN,
			HomeTZ:        c.HomeTZ,
			DedupMode:     c.DedupMode,
			RawArchiveDir: c.RawArchiveDir,
			MigrateOnBoot: c.MigrateOnBoot,
			LogLevel:      c.LogLevel,
			Retention:     c.Retention,
		}, nil
	}
	return c, err
}

func get(key string) string {
	return strings.TrimSpace(os.Getenv(envPrefix + key))
}

func boolEnv(key string, def bool) bool {
	v := get(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
