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

func CreateExtKey(d *sql.DB, label string, dailyLimit, monthlyLimit int) (*ExtKey, error) {
	key, err := generateKey()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var id int64
	err = d.QueryRow(db.Rebind(d, `INSERT INTO ext_keys (key, label, daily_token_limit, monthly_token_limit, created_at) VALUES (?,?,?,?,?) RETURNING id`), key, label, dailyLimit, monthlyLimit, now).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("create ext key: %w", err)
	}
	return &ExtKey{ID: id, Key: key, Label: label, Enabled: true, DailyTokenLimit: dailyLimit, MonthlyTokenLimit: monthlyLimit, CreatedAt: now}, nil
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
	err := d.QueryRow(db.Rebind(d, `SELECT id, key, label, enabled, daily_token_limit, monthly_token_limit, created_at, last_used_at FROM ext_keys WHERE key=? AND is_active = 1`), key).
		Scan(&k.ID, &k.Key, &k.Label, &enabled, &k.DailyTokenLimit, &k.MonthlyTokenLimit, &k.CreatedAt, &lastUsed)
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

func GetExtKeyByID(d *sql.DB, id int64) (*ExtKey, error) {
	k := &ExtKey{}
	var enabled int
	var lastUsed sql.NullTime
	err := d.QueryRow(db.Rebind(d, `SELECT id, key, label, enabled, daily_token_limit, monthly_token_limit, created_at, last_used_at FROM ext_keys WHERE id=? AND is_active = 1`), id).
		Scan(&k.ID, &k.Key, &k.Label, &enabled, &k.DailyTokenLimit, &k.MonthlyTokenLimit, &k.CreatedAt, &lastUsed)
	if err != nil {
		return nil, fmt.Errorf("get ext key by id %d: %w", id, err)
	}
	k.Enabled = enabled != 0
	if lastUsed.Valid {
		t := lastUsed.Time
		k.LastUsedAt = &t
	}
	return k, nil
}

func ListExtKeys(d *sql.DB) ([]ExtKey, error) {
	rows, err := d.Query(`SELECT id, key, label, enabled, daily_token_limit, monthly_token_limit, created_at, last_used_at FROM ext_keys WHERE is_active = 1 ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list ext keys: %w", err)
	}
	defer rows.Close()
	out := make([]ExtKey, 0)
	for rows.Next() {
		var k ExtKey
		var enabled int
		var lastUsed sql.NullTime
		if err := rows.Scan(&k.ID, &k.Key, &k.Label, &enabled, &k.DailyTokenLimit, &k.MonthlyTokenLimit, &k.CreatedAt, &lastUsed); err != nil {
			return nil, err
		}
		k.Enabled = enabled != 0
		if lastUsed.Valid {
			t := lastUsed.Time
			k.LastUsedAt = &t
		}
		out = append(out, k)
	}
	return out, nil
}

// DeleteExtKey 软删除：置 is_active=0 后该 key 立即失效（认证查询过滤），
// 行保留供用量/归档历史关联。同名 key 值不会被重新生成，无需重建名额。
func DeleteExtKey(d *sql.DB, id int64) error {
	_, err := d.Exec(db.Rebind(d, `UPDATE ext_keys SET is_active = 0 WHERE id=? AND is_active = 1`), id)
	if err != nil {
		return fmt.Errorf("delete ext key: %w", err)
	}
	return nil
}

func UpdateExtKey(d *sql.DB, id int64, label string, enabled bool, dailyLimit, monthlyLimit int) error {
	en := 0
	if enabled {
		en = 1
	}
	_, err := d.Exec(db.Rebind(d, `UPDATE ext_keys SET label=?, enabled=?, daily_token_limit=?, monthly_token_limit=? WHERE id=? AND is_active = 1`),
		label, en, dailyLimit, monthlyLimit, id)
	if err != nil {
		return fmt.Errorf("update ext key %d: %w", id, err)
	}
	return nil
}

func TouchExtKey(d *sql.DB, id int64) error {
	_, err := d.Exec(db.Rebind(d, `UPDATE ext_keys SET last_used_at=? WHERE id=? AND is_active = 1`), time.Now(), id)
	if err != nil {
		return fmt.Errorf("touch ext key: %w", err)
	}
	return nil
}

func IsValidKeyFormat(key string) bool {
	return strings.HasPrefix(key, keyPrefix)
}
