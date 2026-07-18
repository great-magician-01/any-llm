package model

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
)

const keyPrefix = "all-sk-"
const keyRandomLen = 32
const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func CreateExtKey(d *sql.DB, label string) (*ExtKey, error) {
	key, err := generateKey()
	if err != nil {
		return nil, err
	}
	var id int64
	err = d.QueryRow(db.Rebind(d, `INSERT INTO ext_keys (key, label) VALUES (?,?) RETURNING id`), key, label).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("create ext key: %w", err)
	}
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

func GetExtKey(d *sql.DB, key string) (*ExtKey, error) {
	k := &ExtKey{}
	var enabled int
	var lastUsed sql.NullTime
	err := d.QueryRow(db.Rebind(d, `SELECT id, key, label, enabled, created_at, last_used_at FROM ext_keys WHERE key=?`), key).
		Scan(&k.ID, &k.Key, &k.Label, &enabled, &k.CreatedAt, &lastUsed)
	if err != nil {
		return nil, fmt.Errorf("get ext key: %w", err)
	}
	k.Enabled = enabled != 0
	if lastUsed.Valid {
		t := lastUsed.Time
		k.LastUsedAt = &t
	}
	return k, nil
}

func ListExtKeys(d *sql.DB) ([]ExtKey, error) {
	rows, err := d.Query(`SELECT id, key, label, enabled, created_at, last_used_at FROM ext_keys ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list ext keys: %w", err)
	}
	defer rows.Close()
	out := make([]ExtKey, 0)
	for rows.Next() {
		var k ExtKey
		var enabled int
		var lastUsed sql.NullTime
		if err := rows.Scan(&k.ID, &k.Key, &k.Label, &enabled, &k.CreatedAt, &lastUsed); err != nil {
			return nil, err
		}
		k.Enabled = enabled != 0
		if lastUsed.Valid {
			t := lastUsed.Time
			k.LastUsedAt = &t
		}
		k.Key = MaskKey(k.Key)
		out = append(out, k)
	}
	return out, nil
}

func DeleteExtKey(d *sql.DB, id int64) error {
	_, err := d.Exec(db.Rebind(d, `DELETE FROM ext_keys WHERE id=?`), id)
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

func TouchExtKey(d *sql.DB, id int64) error {
	_, err := d.Exec(db.Rebind(d, `UPDATE ext_keys SET last_used_at=CURRENT_TIMESTAMP WHERE id=?`), id)
	if err != nil {
		return fmt.Errorf("touch ext key: %w", err)
	}
	return nil
}

func IsValidKeyFormat(key string) bool {
	return strings.HasPrefix(key, keyPrefix)
}
