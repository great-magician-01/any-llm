package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	for _, k := range []string{"ANY_LLM_HOST", "ANY_LLM_PORT", "ANY_LLM_DB_PATH", "ANY_LLM_MASTER_PASSWORD", "ANY_LLM_SESSION_SECRET", "ANY_LLM_SESSION_SECRET_FILE", "ANY_LLM_LOG_FILE", "ANY_LLM_LOG_LEVEL", "DB_TYPE", "DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SCHEMA"} {
		os.Unsetenv(k)
	}
	t.Setenv("ANY_LLM_SESSION_SECRET_FILE", filepath.Join(t.TempDir(), "session-secret"))
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
	if cfg.DBType != "sqlite" {
		t.Fatalf("dbtype=%q", cfg.DBType)
	}
	if cfg.DBPath != "./any-llm.db" {
		t.Fatalf("dbpath=%q", cfg.DBPath)
	}
	if cfg.DBHost != "localhost" || cfg.DBPort != 5432 || cfg.DBUser != "postgres" || cfg.DBName != "amanuensis" || cfg.DBSchema != "public" {
		t.Fatalf("pg defaults wrong: %+v", cfg)
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
	os.Setenv("DB_TYPE", "pg")
	os.Setenv("DB_HOST", "db.example.com")
	os.Setenv("DB_PORT", "6543")
	os.Setenv("DB_USER", "appuser")
	os.Setenv("DB_PASSWORD", "secret")
	os.Setenv("DB_NAME", "appdb")
	os.Setenv("DB_SCHEMA", "myschema")
	os.Setenv("ANY_LLM_MASTER_PASSWORD", "secret")
	os.Setenv("ANY_LLM_SESSION_SECRET", "abcdef0123456789")
	defer func() {
		os.Unsetenv("ANY_LLM_PORT")
		os.Unsetenv("ANY_LLM_DB_PATH")
		os.Unsetenv("DB_TYPE")
		os.Unsetenv("DB_HOST")
		os.Unsetenv("DB_PORT")
		os.Unsetenv("DB_USER")
		os.Unsetenv("DB_PASSWORD")
		os.Unsetenv("DB_NAME")
		os.Unsetenv("DB_SCHEMA")
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
	if cfg.DBType != "postgres" {
		t.Fatalf("dbtype=%q want postgres", cfg.DBType)
	}
	if cfg.DBHost != "db.example.com" || cfg.DBPort != 6543 || cfg.DBUser != "appuser" || cfg.DBPassword != "secret" || cfg.DBName != "appdb" || cfg.DBSchema != "myschema" {
		t.Fatalf("pg cfg=%+v", cfg)
	}
}

func TestLoad_SessionSecretFileGenerated(t *testing.T) {
	os.Unsetenv("ANY_LLM_SESSION_SECRET")
	path := filepath.Join(t.TempDir(), "session-secret")
	t.Setenv("ANY_LLM_SESSION_SECRET_FILE", path)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionSecret == "" {
		t.Fatal("session secret empty")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("secret file not written: %v", err)
	}
	if string(b) != cfg.SessionSecret+"\n" {
		t.Fatalf("file content %q != secret %q", string(b), cfg.SessionSecret)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm() != 0o600 {
			t.Fatalf("file mode = %o, want 600", fi.Mode().Perm())
		}
	}

	// A second Load must reuse the persisted secret (sessions survive restart).
	cfg2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.SessionSecret != cfg.SessionSecret {
		t.Fatalf("secret not stable across loads: %q vs %q", cfg.SessionSecret, cfg2.SessionSecret)
	}
}

func TestLoad_SessionSecretFileExisting(t *testing.T) {
	os.Unsetenv("ANY_LLM_SESSION_SECRET")
	path := filepath.Join(t.TempDir(), "session-secret")
	if err := os.WriteFile(path, []byte("  existing-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANY_LLM_SESSION_SECRET_FILE", path)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionSecret != "existing-secret" {
		t.Fatalf("secret=%q", cfg.SessionSecret)
	}
}

func TestLoad_SessionSecretEnvWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-secret")
	if err := os.WriteFile(path, []byte("file-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANY_LLM_SESSION_SECRET_FILE", path)
	t.Setenv("ANY_LLM_SESSION_SECRET", "env-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionSecret != "env-secret" {
		t.Fatalf("secret=%q", cfg.SessionSecret)
	}
}

func TestLoad_SessionSecretFileUnwritable(t *testing.T) {
	os.Unsetenv("ANY_LLM_SESSION_SECRET")
	// Parent directory does not exist → write fails → ephemeral fallback.
	t.Setenv("ANY_LLM_SESSION_SECRET_FILE", filepath.Join(t.TempDir(), "no-such-dir", "session-secret"))

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionSecret == "" {
		t.Fatal("session secret empty")
	}
}
