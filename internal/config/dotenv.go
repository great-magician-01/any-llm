package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadDotEnv reads a .env file and sets missing env vars.
// Existing process env vars take precedence (do not override).
// Lines starting with '#' and blank lines are ignored. Supports
// KEY=VALUE, KEY="VALUE", and KEY='VALUE' forms. Inline comments
// after a quoted value are allowed.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open .env: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := parseEnvLine(line)
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
	return sc.Err()
}

func parseEnvLine(line string) (key, val string, ok bool) {
	idx := strings.Index(line, "=")
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	if key == "" {
		return "", "", false
	}
	raw := strings.TrimSpace(line[idx+1:])

	if len(raw) >= 2 {
		if (raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'') {
			return key, raw[1 : len(raw)-1], true
		}
		if raw[0] == '"' || raw[0] == '\'' {
			quote := raw[0]
			end := strings.IndexByte(raw[1:], quote)
			if end >= 0 {
				return key, raw[1 : 1+end], true
			}
		}
	}
	if i := strings.Index(raw, " #"); i >= 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	return key, raw, true
}
