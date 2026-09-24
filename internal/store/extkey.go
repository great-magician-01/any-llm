package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
)

const keyPrefix = "all-sk-"
const keyRandomLen = 32
const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// ErrExtKeyLabelTaken 报告名称与其它活跃密钥重复。唯一性在应用层判定（友好 400），
// 并由部分唯一索引 idx_ext_keys_label 在 DB 层兜底，两者口径一致：允许历史/接口
// 直建的密钥没有名称（空名不参与），且软删除的行不占名称名额，删掉后同名可重建。
var ErrExtKeyLabelTaken = errors.New("key label already exists")

// CreateExtKey 新建密钥。读取+写入都在调用方的 writeSync 闭包里完成时，db.Writer
// 会把它们串行化，两次并发创建同名不会同时通过检查。名称按 TrimSpace 归一后存储，
// 与 UI 的 trim 行为一致（避免仅靠前后空白区分的「视觉重名」）。
func CreateExtKey(d *sql.DB, label, remark string, dailyLimit, monthlyLimit int, allowedModels []string) (*ExtKey, error) {
	label = strings.TrimSpace(label)
	taken, err := ExtKeyLabelTaken(d, label, 0)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, ErrExtKeyLabelTaken
	}
	key, err := generateKey()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	// key 列要按方言引用：MySQL 的 key 是保留字，不包反引号会语法错误。
	keyCol := db.QuoteIdent(d, "key")
	id, err := db.InsertReturningID(d, `INSERT INTO ext_keys (`+keyCol+`, label, remark, daily_token_limit, monthly_token_limit, allowed_models, created_at) VALUES (?,?,?,?,?,?,?) RETURNING id`,
		key, label, remark, dailyLimit, monthlyLimit, marshalAllowedModels(allowedModels), now)
	if err != nil {
		return nil, fmt.Errorf("create ext key: %w", err)
	}
	k := &ExtKey{ID: id, Key: key, Label: label, Remark: remark, Enabled: true, DailyTokenLimit: dailyLimit, MonthlyTokenLimit: monthlyLimit, AllowedModels: allowedModels, CreatedAt: now}
	// 落库即入缓存：新建的 key 下一个请求就能命中，不必再查一次。
	putExtKey(k)
	return k, nil
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
	keyCol := db.QuoteIdent(d, "key")
	k := &ExtKey{}
	var enabled int
	var lastUsed sql.NullTime
	var allowed string
	err := d.QueryRow(db.Rebind(d, `SELECT id, `+keyCol+`, label, remark, enabled, daily_token_limit, monthly_token_limit, allowed_models, created_at, last_used_at FROM ext_keys WHERE `+keyCol+`=? AND is_active = 1`), key).
		Scan(&k.ID, &k.Key, &k.Label, &k.Remark, &enabled, &k.DailyTokenLimit, &k.MonthlyTokenLimit, &allowed, &k.CreatedAt, &lastUsed)
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
	keyCol := db.QuoteIdent(d, "key")
	k := &ExtKey{}
	var enabled int
	var lastUsed sql.NullTime
	var allowed string
	err := d.QueryRow(db.Rebind(d, `SELECT id, `+keyCol+`, label, remark, enabled, daily_token_limit, monthly_token_limit, allowed_models, created_at, last_used_at FROM ext_keys WHERE id=? AND is_active = 1`), id).
		Scan(&k.ID, &k.Key, &k.Label, &k.Remark, &enabled, &k.DailyTokenLimit, &k.MonthlyTokenLimit, &allowed, &k.CreatedAt, &lastUsed)
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
	keyCol := db.QuoteIdent(d, "key")
	rows, err := d.Query(`SELECT id, ` + keyCol + `, label, remark, enabled, daily_token_limit, monthly_token_limit, allowed_models, created_at, last_used_at FROM ext_keys WHERE is_active = 1 ORDER BY id DESC`)
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
		if err := rows.Scan(&k.ID, &k.Key, &k.Label, &k.Remark, &enabled, &k.DailyTokenLimit, &k.MonthlyTokenLimit, &allowed, &k.CreatedAt, &lastUsed); err != nil {
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
	// 缓存里按 key 字符串索引，删除时只有 ID，逐出即止（下个请求回库自然落空）。
	evictExtKeyByID(id)
	return nil
}

func UpdateExtKey(d *sql.DB, id int64, label, remark string, enabled bool, dailyLimit, monthlyLimit int, allowedModels []string) error {
	label = strings.TrimSpace(label)
	// 当前名称在本函数内读（通常已处于 writeSync 闭包，随写入一起串行化）。
	var curLabel string
	if err := d.QueryRow(db.Rebind(d, `SELECT label FROM ext_keys WHERE id=? AND is_active = 1`), id).Scan(&curLabel); err != nil {
		return fmt.Errorf("load ext key %d label: %w", id, err)
	}
	// 只在真正改名时查重：唯一性约束是后加的，老数据可能本就重名——不改名的
	// 更新（启停/限额/备注）不该被别人的重名锁死。
	if label != curLabel {
		taken, err := ExtKeyLabelTaken(d, label, id)
		if err != nil {
			return err
		}
		if taken {
			return ErrExtKeyLabelTaken
		}
	}
	_, err := d.Exec(db.Rebind(d, `UPDATE ext_keys SET label=?, remark=?, enabled=?, daily_token_limit=?, monthly_token_limit=?, allowed_models=? WHERE id=? AND is_active = 1`),
		label, remark, b2i(enabled), dailyLimit, monthlyLimit, marshalAllowedModels(allowedModels), id)
	if err != nil {
		return fmt.Errorf("update ext key %d: %w", id, err)
	}
	// 读回整行回填缓存：与 UpdateUpstream 的「先 Get 再存」不同，这里参数里没有
	// key 字符串与 created_at，拼不出完整行，故多一次 SELECT（仅管理端 PATCH 路径）。
	refreshExtKey(d, id)
	return nil
}

// ExtKeyLabelTaken 报告活跃密钥里是否已有同名（精确匹配，不做大小写/空白归一）。
// excludeID 用于更新时排除自己（0 = 不排除）。空名不参与唯一性：接口直建或历史
// 数据可能没有名称，随便一个空名不该把后来者也挡在门外。
func ExtKeyLabelTaken(d *sql.DB, label string, excludeID int64) (bool, error) {
	if label == "" {
		return false, nil
	}
	var n int
	if err := d.QueryRow(db.Rebind(d, `SELECT COUNT(*) FROM ext_keys WHERE label=? AND is_active = 1 AND id<>?`), label, excludeID).Scan(&n); err != nil {
		return false, fmt.Errorf("check ext key label %q: %w", label, err)
	}
	return n > 0, nil
}

// TouchExtKey 只更新 last_used_at：该列不参与热路径任何判定（鉴权看
// key/enabled，路由看 allowed_models），为它失效缓存等于每请求都失效，
// 故这里故意不碰缓存——库里是最新的，缓存里保持读取时的值即可。
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

// marshalAllowedModels 把白名单写成 ext_keys.allowed_models 列文本：空 = 不限。
func marshalAllowedModels(models []string) string {
	if len(models) == 0 {
		return ""
	}
	b, err := json.Marshal(models)
	if err != nil { // []string 不会失败，防御分支
		return ""
	}
	return string(b)
}
