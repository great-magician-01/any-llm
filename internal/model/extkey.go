package model

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
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
	var allowed string
	err := d.QueryRow(db.Rebind(d, `SELECT id, key, label, enabled, daily_token_limit, monthly_token_limit, allowed_models, created_at, last_used_at FROM ext_keys WHERE key=? AND is_active = 1`), key).
		Scan(&k.ID, &k.Key, &k.Label, &enabled, &k.DailyTokenLimit, &k.MonthlyTokenLimit, &allowed, &k.CreatedAt, &lastUsed)
	if err != nil {
		return nil, fmt.Errorf("get ext key: %w", err)
	}
	k.Enabled = enabled != 0
	k.AllowedModels, err = parseAllowedModels(allowed)
	if err != nil {
		return nil, fmt.Errorf("get ext key: %w", err)
	}
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
	var allowed string
	err := d.QueryRow(db.Rebind(d, `SELECT id, key, label, enabled, daily_token_limit, monthly_token_limit, allowed_models, created_at, last_used_at FROM ext_keys WHERE id=? AND is_active = 1`), id).
		Scan(&k.ID, &k.Key, &k.Label, &enabled, &k.DailyTokenLimit, &k.MonthlyTokenLimit, &allowed, &k.CreatedAt, &lastUsed)
	if err != nil {
		return nil, fmt.Errorf("get ext key by id %d: %w", id, err)
	}
	k.Enabled = enabled != 0
	k.AllowedModels, err = parseAllowedModels(allowed)
	if err != nil {
		return nil, fmt.Errorf("get ext key by id %d: %w", id, err)
	}
	if lastUsed.Valid {
		t := lastUsed.Time
		k.LastUsedAt = &t
	}
	return k, nil
}

func ListExtKeys(d *sql.DB) ([]ExtKey, error) {
	rows, err := d.Query(`SELECT id, key, label, enabled, daily_token_limit, monthly_token_limit, allowed_models, created_at, last_used_at FROM ext_keys WHERE is_active = 1 ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list ext keys: %w", err)
	}
	defer rows.Close()
	out := make([]ExtKey, 0)
	for rows.Next() {
		var k ExtKey
		var enabled int
		var lastUsed sql.NullTime
		var allowed string
		if err := rows.Scan(&k.ID, &k.Key, &k.Label, &enabled, &k.DailyTokenLimit, &k.MonthlyTokenLimit, &allowed, &k.CreatedAt, &lastUsed); err != nil {
			return nil, err
		}
		k.Enabled = enabled != 0
		if k.AllowedModels, err = parseAllowedModels(allowed); err != nil {
			return nil, err
		}
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

// AllowsModel 报告该 key 是否可使用对外模型名 name（别名或 upstream/model，
// 与 /v1/models 列出的 id 一致）。白名单为空 = 不限制；仅精确匹配。
func (k *ExtKey) AllowsModel(name string) bool {
	if len(k.AllowedModels) == 0 {
		return true
	}
	for _, m := range k.AllowedModels {
		if m == name {
			return true
		}
	}
	return false
}

// parseAllowedModels 解析 ext_keys.allowed_models 列：空串/空数组 = 不限
// （返回 nil）；否则要求合法 JSON 字符串数组。解析失败返回错误——白名单
// 是权限数据，宁可响亮失败也不静默放行。
func parseAllowedModels(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var models []string
	if err := json.Unmarshal([]byte(s), &models); err != nil {
		return nil, fmt.Errorf("parse allowed_models %q: %w", s, err)
	}
	if len(models) == 0 {
		return nil, nil
	}
	return models, nil
}
