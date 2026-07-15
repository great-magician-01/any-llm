package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port           int
	DBPath         string
	MasterPassword string
	SessionSecret  string
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:           envInt("ANY_LLM_PORT", 8080),
		DBPath:         envStr("ANY_LLM_DB_PATH", "./any-llm.db"),
		MasterPassword: envStr("ANY_LLM_MASTER_PASSWORD", "admin"),
		SessionSecret:  envStr("ANY_LLM_SESSION_SECRET", ""),
	}
	if cfg.MasterPassword == "admin" {
		fmt.Fprintln(os.Stderr, "WARNING: using default master password 'admin'. Set ANY_LLM_MASTER_PASSWORD to change it.")
	}
	if cfg.SessionSecret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("generate session secret: %w", err)
		}
		cfg.SessionSecret = hex.EncodeToString(b)
		fmt.Fprintln(os.Stderr, "WARNING: ANY_LLM_SESSION_SECRET not set; generated random secret (will not survive restart).")
	}
	return cfg, nil
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
