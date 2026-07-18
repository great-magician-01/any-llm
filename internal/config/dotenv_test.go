package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := []byte(`# comment
KEY1=value1
KEY2="quoted value"
KEY3='single quoted'
KEY4=with comment  # inline
EMPTY=
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"KEY1", "KEY2", "KEY3", "KEY4", "EMPTY"} {
		os.Unsetenv(k)
	}
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("KEY1"); v != "value1" {
		t.Fatalf("KEY1=%q", v)
	}
	if v := os.Getenv("KEY2"); v != "quoted value" {
		t.Fatalf("KEY2=%q", v)
	}
	if v := os.Getenv("KEY3"); v != "single quoted" {
		t.Fatalf("KEY3=%q", v)
	}
	if v := os.Getenv("KEY4"); v != "with comment" {
		t.Fatalf("KEY4=%q", v)
	}
	if v := os.Getenv("EMPTY"); v != "" {
		t.Fatalf("EMPTY=%q", v)
	}
}

func TestLoadDotEnvDoesNotOverrideExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("FOO=fromfile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Setenv("FOO", "fromenv")
	defer os.Unsetenv("FOO")
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("FOO"); v != "fromenv" {
		t.Fatalf("expected existing env to win, got %q", v)
	}
}

func TestLoadDotEnvMissingFile(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
}
