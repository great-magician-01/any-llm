package config

// Package config loads application configuration from environment variables.
// YAML config file support (e.g. config.yaml) is not yet implemented; all
// settings are sourced from env vars only for now.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
)

type Config struct {
	Host              string
	Port              int
	DBType            string
	DBPath            string
	DBHost            string
	DBPort            int
	DBUser            string
	DBPassword        string
	DBName            string
	DBSchema          string
	MasterPassword    string
	SessionSecret     string
	SessionSecretFile string
	// SessionTTL is how long admin login sessions live; 0 means never expire.
	SessionTTL time.Duration
	LogFile    string
	LogLevel   logger.Level
}

func Load() (*Config, error) {
	_ = loadDotEnv(".env")
	logLevel, err := logger.LevelFromString(envStr("ANY_LLM_LOG_LEVEL", "info"))
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		Host:              envStr("ANY_LLM_HOST", "0.0.0.0"),
		Port:              envInt("ANY_LLM_PORT", 6718),
		DBType:            envStr("DB_TYPE", "sqlite"),
		DBPath:            envStr("ANY_LLM_DB_PATH", "./any-llm.db"),
		DBHost:            envStr("DB_HOST", "localhost"),
		DBPort:            envInt("DB_PORT", 5432),
		DBUser:            envStr("DB_USER", "postgres"),
		DBPassword:        envStr("DB_PASSWORD", ""),
		DBName:            envStr("DB_NAME", "amanuensis"),
		DBSchema:          envStr("DB_SCHEMA", "public"),
		MasterPassword:    envStr("ANY_LLM_MASTER_PASSWORD", "admin"),
		SessionSecret:     envStr("ANY_LLM_SESSION_SECRET", ""),
		SessionSecretFile: envStr("ANY_LLM_SESSION_SECRET_FILE", "./.session-secret"),
		SessionTTL:        envDuration("ANY_LLM_SESSION_TTL", 24*time.Hour),
		LogFile:           envStr("ANY_LLM_LOG_FILE", "./logs/any-llm.log"),
		LogLevel:          logLevel,
	}
	cfg.DBType = normalizeDBType(cfg.DBType)
	if cfg.MasterPassword == "admin" {
		fmt.Fprintln(os.Stderr, "WARNING: using default master password 'admin'. Set ANY_LLM_MASTER_PASSWORD to change it.")
	}
	if cfg.SessionSecret == "" {
		secret, err := loadOrCreateSecret(cfg.SessionSecretFile)
		if err != nil {
			return nil, fmt.Errorf("session secret: %w", err)
		}
		cfg.SessionSecret = secret
	}
	return cfg, nil
}

// loadOrCreateSecret reads the session secret from path, generating and
// persisting a new one (mode 0600) when the file is missing or empty.
// Persisting the secret lets admin sessions survive restarts without
// requiring ANY_LLM_SESSION_SECRET to be set explicitly. If the file cannot
// be read or written, a fresh ephemeral secret is returned with a warning so
// the service stays available (sessions just won't survive restart).
func loadOrCreateSecret(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "WARNING: cannot read session secret file %s: %v\n", path, err)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session secret: %w", err)
	}
	s := hex.EncodeToString(b)
	if err := os.WriteFile(path, []byte(s+"\n"), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: ANY_LLM_SESSION_SECRET not set and cannot persist generated secret to %s (%v); sessions will not survive restart.\n", path, err)
		return s, nil
	}
	fmt.Fprintf(os.Stderr, "WARNING: ANY_LLM_SESSION_SECRET not set; generated random secret saved to %s\n", path)
	return s, nil
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// envDuration parses a duration like "24h" (time.ParseDuration); a bare
// integer is treated as hours ("24" == 24h). On parse failure it warns and
// falls back to def. 0 means "never expires" — callers decide.
func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Hour
	}
	fmt.Fprintf(os.Stderr, "WARNING: invalid %s=%q (want e.g. 24h or hours); using default %s\n", key, v, def)
	return def
}

func normalizeDBType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "postgres", "postgresql", "pg":
		return "postgres"
	default:
		return "sqlite"
	}
}
