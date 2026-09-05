package model

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
)

// ModelAlias 固定对外模型名：客户端用别名请求，网关按绑定优先级依次尝试
// 「上游+真实模型」，对外名称不变而内里可自由切换、自动故障转移。
type ModelAlias struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Bindings  []AliasBinding `json:"bindings"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// AliasBinding 别名的一条候选绑定。Priority 升序即尝试顺序（0 最先）。
// UpstreamName / UpstreamEnabled 由联查填充，仅用于展示：指向的上游被禁用
// （而非删除）时 UpstreamName 仍在，UpstreamEnabled=false 供 /v1/models
// 判断别名可用性与管理端提示。
type AliasBinding struct {
	ID              int64  `json:"id"`
	AliasID         int64  `json:"alias_id"`
	UpstreamID      int64  `json:"upstream_id"`
	UpstreamName    string `json:"upstream_name,omitempty"`
	UpstreamEnabled bool   `json:"upstream_enabled"`
	ModelName       string `json:"model_name"`
	Priority        int    `json:"priority"`
}

// AliasTarget 是网关故障转移循环的一个候选：解析后的上游与其上的真实模型。
type AliasTarget struct {
	Upstream  *Upstream
	ModelName string
}

// CreateAlias 创建别名及其绑定。bindings 的数组顺序即优先级（重写为 0..n-1）。
func CreateAlias(d *sql.DB, a *ModelAlias) (int64, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin create alias: %w", err)
	}
	defer tx.Rollback()
	now := time.Now()
	var id int64
	err = tx.QueryRow(db.Rebind(d, `INSERT INTO model_aliases (name, created_at, updated_at) VALUES (?,?,?) RETURNING id`),
		a.Name, now, now).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create alias %q: %w", a.Name, err)
	}
	if err := insertBindings(tx, d, id, a.Bindings); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit create alias %q: %w", a.Name, err)
	}
	return id, nil
}

// insertBindings 按数组顺序写入绑定，priority 归一化为 0..n-1（与「仅活跃行」
// 部分唯一索引 (alias_id, priority) 对应）。
func insertBindings(tx *sql.Tx, d *sql.DB, aliasID int64, bindings []AliasBinding) error {
	for i, b := range bindings {
		_, err := tx.Exec(db.Rebind(d, `INSERT INTO model_alias_bindings (alias_id, upstream_id, model_name, priority) VALUES (?,?,?,?)`),
			aliasID, b.UpstreamID, b.ModelName, i)
		if err != nil {
			return fmt.Errorf("insert binding %d (upstream_id=%d model=%q): %w", i, b.UpstreamID, b.ModelName, err)
		}
	}
	return nil
}

// GetAliasByID 取单个别名及其全部活跃绑定（含上游名，按优先级排序）。
func GetAliasByID(d *sql.DB, id int64) (*ModelAlias, error) {
	a := &ModelAlias{}
	err := d.QueryRow(db.Rebind(d, `SELECT id, name, created_at, updated_at FROM model_aliases WHERE id=? AND is_active = 1`), id).
		Scan(&a.ID, &a.Name, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get alias %d: %w", id, err)
	}
	bindings, err := listBindings(d, `WHERE b.alias_id = ? AND b.is_active = 1`, id)
	if err != nil {
		return nil, err
	}
	a.Bindings = bindings
	return a, nil
}

// ListAliases 列出全部活跃别名及其绑定（含上游名）。
func ListAliases(d *sql.DB) ([]ModelAlias, error) {
	rows, err := d.Query(`SELECT id, name, created_at, updated_at FROM model_aliases WHERE is_active = 1 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list aliases: %w", err)
	}
	defer rows.Close()
	out := make([]ModelAlias, 0)
	for rows.Next() {
		var a ModelAlias
		if err := rows.Scan(&a.ID, &a.Name, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		bindings, err := listBindings(d, `WHERE b.alias_id = ? AND b.is_active = 1`, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Bindings = bindings
	}
	return out, nil
}

// listBindings 取绑定并联查上游名（含指向上游已软删除的绑定，供管理端展示）；
// 网关解析用 ResolveAliasTargets，会跳过上游不活跃或已禁用的绑定。
func listBindings(d *sql.DB, where string, args ...any) ([]AliasBinding, error) {
	rows, err := d.Query(db.Rebind(d, `SELECT b.id, b.alias_id, b.upstream_id, b.model_name, b.priority,
		COALESCE((SELECT u.name FROM upstreams u WHERE u.id = b.upstream_id AND u.is_active = 1), '') AS upstream_name,
		COALESCE((SELECT u.enabled FROM upstreams u WHERE u.id = b.upstream_id AND u.is_active = 1), 0) AS upstream_enabled
		FROM model_alias_bindings b `+where+` ORDER BY b.priority, b.id`), args...)
	if err != nil {
		return nil, fmt.Errorf("list alias bindings: %w", err)
	}
	defer rows.Close()
	out := make([]AliasBinding, 0)
	for rows.Next() {
		var b AliasBinding
		var upstreamEnabled int
		if err := rows.Scan(&b.ID, &b.AliasID, &b.UpstreamID, &b.ModelName, &b.Priority, &b.UpstreamName, &upstreamEnabled); err != nil {
			return nil, err
		}
		b.UpstreamEnabled = upstreamEnabled != 0
		out = append(out, b)
	}
	return out, rows.Err()
}

// ResolveAliasTargets 网关热路径：按对外名称精确匹配活跃别名，返回按优先级
// 排序的候选链；绑定指向的上游若已删除/不活跃/已禁用则跳过（禁用的候选直接
// 从链中消失，故障转移自然落到下一候选）。found=false 表示别名不存在（调用
// 方回落到 name/model 直连拆分）；found=true 但 targets 为空表示别名存在但
// 无可用绑定。
func ResolveAliasTargets(d *sql.DB, name string) (found bool, targets []AliasTarget, err error) {
	var aliasID int64
	err = d.QueryRow(db.Rebind(d, `SELECT id FROM model_aliases WHERE name=? AND is_active = 1`), name).Scan(&aliasID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("resolve alias %q: %w", name, err)
	}
	rows, err := d.Query(db.Rebind(d, `SELECT u.id, u.name, u.base_url, u.api_key, u.format, u.enabled, u.daily_token_limit, u.monthly_token_limit, u.created_at, u.updated_at, b.model_name
		FROM model_alias_bindings b JOIN upstreams u ON u.id = b.upstream_id AND u.is_active = 1 AND u.enabled = 1
		WHERE b.alias_id = ? AND b.is_active = 1 ORDER BY b.priority, b.id`), aliasID)
	if err != nil {
		return true, nil, fmt.Errorf("resolve alias %q bindings: %w", name, err)
	}
	defer rows.Close()
	for rows.Next() {
		t := AliasTarget{Upstream: &Upstream{}}
		var enabled int
		if err := rows.Scan(&t.Upstream.ID, &t.Upstream.Name, &t.Upstream.BaseURL, &t.Upstream.APIKey, &t.Upstream.Format,
			&enabled, &t.Upstream.DailyTokenLimit, &t.Upstream.MonthlyTokenLimit, &t.Upstream.CreatedAt, &t.Upstream.UpdatedAt,
			&t.ModelName); err != nil {
			return true, nil, err
		}
		t.Upstream.Enabled = enabled != 0
		targets = append(targets, t)
	}
	return true, targets, rows.Err()
}

// UpdateAlias 改名并整体替换绑定：旧绑定全软删、按新顺序插入（绑定无外部
// 引用，不像 upstream_models 需要复活逻辑），同事务保证不半更新。
func UpdateAlias(d *sql.DB, a *ModelAlias) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin update alias %d: %w", a.ID, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(db.Rebind(d, `UPDATE model_aliases SET name=?, updated_at=? WHERE id=? AND is_active = 1`),
		a.Name, time.Now(), a.ID); err != nil {
		return fmt.Errorf("update alias %d: %w", a.ID, err)
	}
	if _, err := tx.Exec(db.Rebind(d, `UPDATE model_alias_bindings SET is_active = 0 WHERE alias_id=? AND is_active = 1`), a.ID); err != nil {
		return fmt.Errorf("clear alias %d bindings: %w", a.ID, err)
	}
	if err := insertBindings(tx, d, a.ID, a.Bindings); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update alias %d: %w", a.ID, err)
	}
	return nil
}

// DeleteAlias 软删除别名及其绑定：网关解析立即失败，行保留供历史关联；
// 部分唯一索引不占名额，同名别名可重建。
func DeleteAlias(d *sql.DB, id int64) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin delete alias %d: %w", id, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(db.Rebind(d, `UPDATE model_aliases SET is_active = 0, updated_at=? WHERE id=? AND is_active = 1`), time.Now(), id); err != nil {
		return fmt.Errorf("delete alias %d: %w", id, err)
	}
	if _, err := tx.Exec(db.Rebind(d, `UPDATE model_alias_bindings SET is_active = 0 WHERE alias_id=? AND is_active = 1`), id); err != nil {
		return fmt.Errorf("delete alias %d bindings: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete alias %d: %w", id, err)
	}
	return nil
}
