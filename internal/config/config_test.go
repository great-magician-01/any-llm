package config

import (
	"os"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	for _, k := range []string{"ANY_LLM_HOST", "ANY_LLM_PORT", "ANY_LLM_DB_PATH", "ANY_LLM_MASTER_PASSWORD", "ANY_LLM_SESSION_SECRET", "ANY_LLM_LOG_FILE", "ANY_LLM_LOG_LEVEL"} {
		os.Unsetenv(k)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "0.0.0.0" {
		t.Fatalf("host=%q", cfg.Host)
	}
	if cfg.Port != 6718 {
		t.Fatalf("port=%d", cfg.Port)
	}
	if cfg.DBPath != "./any-llm.db" {
		t.Fatalf("dbpath=%q", cfg.DBPath)
	}
	if cfg.MasterPassword != "admin" {
		t.Fatalf("master=%q", cfg.MasterPassword)
	}
	if cfg.SessionSecret == "" || len(cfg.SessionSecret) < 16 {
		t.Fatalf("session secret too short: %q", cfg.SessionSecret)
	}
	if cfg.LogFile != "./logs/any-llm.log" {
		t.Fatalf("logfile=%q", cfg.LogFile)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	os.Setenv("ANY_LLM_PORT", "9090")
	os.Setenv("ANY_LLM_DB_PATH", "/tmp/test.db")
	os.Setenv("ANY_LLM_MASTER_PASSWORD", "secret")
	os.Setenv("ANY_LLM_SESSION_SECRET", "abcdef0123456789")
	defer func() {
		os.Unsetenv("ANY_LLM_PORT")
		os.Unsetenv("ANY_LLM_DB_PATH")
		os.Unsetenv("ANY_LLM_MASTER_PASSWORD")
		os.Unsetenv("ANY_LLM_SESSION_SECRET")
	}()
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9090 || cfg.DBPath != "/tmp/test.db" || cfg.MasterPassword != "secret" || cfg.SessionSecret != "abcdef0123456789" {
		t.Fatalf("cfg=%+v", cfg)
	}
}
