package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
)

// TestEnvDuration 覆盖 duration 环境变量的三种写法与回退语义：
// "24h" 走 time.ParseDuration、"24" 按小时、非法值回退默认值并告警。
// 这条规则直接决定 admin 会话多久过期、余额多久轮询一次，此前零覆盖。
func TestEnvDuration(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"24h", 24 * time.Hour},
		{"168h", 168 * time.Hour},
		{"30m", 30 * time.Minute},
		{"24", 24 * time.Hour}, // 纯数字 = 小时
		{"0", 0},               // 0 = 永不过期 / 关闭轮询
		{"", 7 * time.Hour},    // 未设置 = 默认值
		{"bogus", 7 * time.Hour},
		{"-1", -1 * time.Hour}, // 负值按小时解析（由调用方决定含义）
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			if c.raw != "" {
				t.Setenv("TEST_ENV_DURATION", c.raw)
			}
			got := envDuration("TEST_ENV_DURATION", 7*time.Hour)
			if got != c.want {
				t.Fatalf("envDuration(%q)=%s want %s", c.raw, got, c.want)
			}
		})
	}
}

// TestEnvInt 覆盖端口一类整数变量的解析与非法值回退。
func TestEnvInt(t *testing.T) {
	t.Setenv("TEST_ENV_INT", "9090")
	if got := envInt("TEST_ENV_INT", 6718); got != 9090 {
		t.Fatalf("envInt=%d want 9090", got)
	}
	t.Setenv("TEST_ENV_INT", "not-a-number")
	if got := envInt("TEST_ENV_INT", 6718); got != 6718 {
		t.Fatalf("invalid envInt=%d want default 6718", got)
	}
	t.Setenv("TEST_ENV_INT", "")
	if got := envInt("TEST_ENV_INT", 6718); got != 6718 {
		t.Fatalf("empty envInt=%d want default 6718", got)
	}
}

// TestLoad_SessionAndBalanceDurations 断言两个 duration 设置真的接到 Config 上：
// ANY_LLM_SESSION_TTL=0（永不过期）与 ANY_LLM_BALANCE_INTERVAL=0（关闭自动抓取）
// 是有独立语义的取值，不能被"默认值回退"悄悄吃掉。
func TestLoad_SessionAndBalanceDurations(t *testing.T) {
	t.Setenv("ANY_LLM_SESSION_SECRET", "test-secret")

	t.Setenv("ANY_LLM_SESSION_TTL", "168")
	t.Setenv("ANY_LLM_BALANCE_INTERVAL", "30m")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionTTL != 168*time.Hour {
		t.Errorf("SessionTTL=%s want 168h", cfg.SessionTTL)
	}
	if cfg.BalanceInterval != 30*time.Minute {
		t.Errorf("BalanceInterval=%s want 30m", cfg.BalanceInterval)
	}

	t.Setenv("ANY_LLM_SESSION_TTL", "0")
	t.Setenv("ANY_LLM_BALANCE_INTERVAL", "0")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionTTL != 0 {
		t.Errorf("SessionTTL=%s want 0 (never expire)", cfg.SessionTTL)
	}
	if cfg.BalanceInterval != 0 {
		t.Errorf("BalanceInterval=%s want 0 (disable polling)", cfg.BalanceInterval)
	}
}

// TestLoad_InvalidLogLevelFails 断言非法日志级别是启动错误而不是静默回退：
// 配置错了要立刻知道，否则日志会以意料之外的级别运行。
func TestLoad_InvalidLogLevelFails(t *testing.T) {
	t.Setenv("ANY_LLM_SESSION_SECRET", "test-secret")
	t.Setenv("ANY_LLM_LOG_LEVEL", "verbose")
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to fail on invalid ANY_LLM_LOG_LEVEL")
	}
}

// TestLoad_LogLevelParsed 断言合法级别被解析进 Config。
func TestLoad_LogLevelParsed(t *testing.T) {
	t.Setenv("ANY_LLM_SESSION_SECRET", "test-secret")
	t.Setenv("ANY_LLM_LOG_LEVEL", "debug")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != logger.LevelDebug {
		t.Fatalf("LogLevel=%v want debug", cfg.LogLevel)
	}
}

// TestLoad_EmptyLogFileDisablesFileLogging 是文档与实现的一致性回归位：
// AGENTS.md 写着 "ANY_LLM_LOG_FILE 空字符串关闭文件日志"，但 envStr 把空串
// 当成未设置并回落到默认路径，导致这个行为根本不可达。
func TestLoad_EmptyLogFileDisablesFileLogging(t *testing.T) {
	t.Setenv("ANY_LLM_SESSION_SECRET", "test-secret")
	t.Setenv("ANY_LLM_LOG_FILE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogFile != "" {
		t.Fatalf("LogFile=%q want empty (empty must disable file logging, not fall back to the default)", cfg.LogFile)
	}
}

// TestLoad_LogFileDefault 断言未设置时仍是默认路径（别把关闭语义做成默认）。
func TestLoad_LogFileDefault(t *testing.T) {
	t.Setenv("ANY_LLM_SESSION_SECRET", "test-secret")
	os.Unsetenv("ANY_LLM_LOG_FILE")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogFile != "./logs/any-llm.log" {
		t.Fatalf("LogFile=%q want ./logs/any-llm.log", cfg.LogFile)
	}
}

// TestLoad_EmptyLogFileFromDotEnv 覆盖真实路径：.env 里写 ANY_LLM_LOG_FILE=
// （loadDotEnv 会把空值 setenv）也必须关闭文件日志。dotenv 的写入 + 解析
// 两段此前各自被测过，但"空值语义"这条链路没有。
func TestLoad_EmptyLogFileFromDotEnv(t *testing.T) {
	t.Setenv("ANY_LLM_SESSION_SECRET", "test-secret")
	os.Unsetenv("ANY_LLM_LOG_FILE")

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("ANY_LLM_LOG_FILE=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if v, ok := os.LookupEnv("ANY_LLM_LOG_FILE"); !ok || v != "" {
		t.Fatalf("loadDotEnv should set an empty ANY_LLM_LOG_FILE, got %q (set=%v)", v, ok)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogFile != "" {
		t.Fatalf("LogFile=%q want empty after .env sets it empty", cfg.LogFile)
	}
}
