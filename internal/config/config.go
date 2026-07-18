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

	"github.com/great-magician-01/any-llm/internal/logger"
)

type Config struct {
	Host           string
	Port           int
	DBPath         string
	MasterPassword string
	SessionSecret  string
	LogFile        string
	LogLevel       logger.Level
}

func Load() (*Config, error) {
	_ = loadDotEnv(".env")
	logLevel, err := logger.LevelFromString(envStr("ANY_LLM_LOG_LEVEL", "info"))
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		Host:           envStr("ANY_LLM_HOST", "0.0.0.0"),
		Port:           envInt("ANY_LLM_PORT", 6718),
		DBPath:         envStr("ANY_LLM_DB_PATH", "./any-llm.db"),
		MasterPassword: envStr("ANY_LLM_MASTER_PASSWORD", "admin"),
		SessionSecret:  envStr("ANY_LLM_SESSION_SECRET", ""),
		LogFile:        envStr("ANY_LLM_LOG_FILE", "./logs/any-llm.log"),
		LogLevel:       logLevel,
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
