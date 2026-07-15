package model

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const keyPrefix = "all-sk-"
const keyRandomLen = 32
const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func CreateExtKey(db *sql.DB, label string) (*ExtKey, error) {
	key, err := generateKey()
	if err != nil {
		return nil, err
	}
	res, err := db.Exec(`INSERT INTO ext_keys (key, label) VALUES (?,?)`, key, label)
	if err != nil {
		return nil, fmt.Errorf("create ext key: %w", err)
	}
	id, _ := res.LastInsertId()
	return &ExtKey{ID: id, Key: key, Label: label, Enabled: true, CreatedAt: time.Now()}, nil
}

func generateKey() (string, error) {
	b := make([]byte, keyRandomLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(base62Chars))))
		if err != nil {
			return "", fmt.Errorf("generate random: %w", err)
		}
		b[i] = base62Chars[n.Int64()]
	}
	return keyPrefix + string(b), nil
}

func GetExtKey(db *sql.DB, key string) (*ExtKey, error) {
	k := &ExtKey{}
	var enabled int
	var createdAt string
	var lastUsed sql.NullString
	err := db.QueryRow(`SELECT id, key, label, enabled, created_at, last_used_at FROM ext_keys WHERE key=?`, key).
		Scan(&k.ID, &k.Key, &k.Label, &enabled, &createdAt, &lastUsed)
	if err != nil {
		return nil, fmt.Errorf("get ext key: %w", err)
	}
	k.Enabled = enabled != 0
	k.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	if lastUsed.Valid {
		t, _ := time.Parse("2006-01-02 15:04:05", lastUsed.String)
		k.LastUsedAt = &t
	}
	return k, nil
}

func ListExtKeys(db *sql.DB) ([]ExtKey, error) {
	rows, err := db.Query(`SELECT id, key, label, enabled, created_at, last_used_at FROM ext_keys ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list ext keys: %w", err)
	}
	defer rows.Close()
	var out []ExtKey
	for rows.Next() {
		var k ExtKey
		var enabled int
		var createdAt string
		var lastUsed sql.NullString
		if err := rows.Scan(&k.ID, &k.Key, &k.Label, &enabled, &createdAt, &lastUsed); err != nil {
			return nil, err
		}
		k.Enabled = enabled != 0
		k.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		if lastUsed.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", lastUsed.String)
			k.LastUsedAt = &t
		}
		k.Key = MaskKey(k.Key)
		out = append(out, k)
	}
	return out, nil
}

func DeleteExtKey(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM ext_keys WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete ext key: %w", err)
	}
	return nil
}

func MaskKey(key string) string {
	if len(key) < 16 {
		return key + "****"
	}
	return key[:12] + "****" + key[len(key)-4:]
}

func TouchExtKey(db *sql.DB, id int64) error {
	_, err := db.Exec(`UPDATE ext_keys SET last_used_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("touch ext key: %w", err)
	}
	return nil
}

func IsValidKeyFormat(key string) bool {
	return strings.HasPrefix(key, keyPrefix)
}
