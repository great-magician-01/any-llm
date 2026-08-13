package model

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
)

func CreateUpstream(d *sql.DB, u *Upstream) (int64, error) {
	var id int64
	now := time.Now()
	err := d.QueryRow(db.Rebind(d, `INSERT INTO upstreams (name, base_url, api_key, format, daily_token_limit, monthly_token_limit, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?) RETURNING id`),
		u.Name, u.BaseURL, u.APIKey, u.Format, u.DailyTokenLimit, u.MonthlyTokenLimit, now, now).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create upstream: %w", err)
	}
	return id, nil
}

func GetUpstreamByID(d *sql.DB, id int64) (*Upstream, error) {
	u := &Upstream{}
	err := d.QueryRow(db.Rebind(d, `SELECT id, name, base_url, api_key, format, daily_token_limit, monthly_token_limit, created_at, updated_at FROM upstreams WHERE id=? AND is_active = 1`), id).
		Scan(&u.ID, &u.Name, &u.BaseURL, &u.APIKey, &u.Format, &u.DailyTokenLimit, &u.MonthlyTokenLimit, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get upstream %d: %w", id, err)
	}
	return u, nil
}

func GetUpstreamByName(d *sql.DB, name string) (*Upstream, error) {
	u := &Upstream{}
	err := d.QueryRow(db.Rebind(d, `SELECT id, name, base_url, api_key, format, daily_token_limit, monthly_token_limit, created_at, updated_at FROM upstreams WHERE name=? AND is_active = 1`), name).
		Scan(&u.ID, &u.Name, &u.BaseURL, &u.APIKey, &u.Format, &u.DailyTokenLimit, &u.MonthlyTokenLimit, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get upstream by name %q: %w", name, err)
	}
	return u, nil
}

func ListUpstreams(d *sql.DB) ([]Upstream, error) {
	rows, err := d.Query(`SELECT u.id, u.name, u.base_url, u.api_key, u.format, u.daily_token_limit, u.monthly_token_limit, u.created_at, u.updated_at,
		(SELECT COUNT(*) FROM upstream_models WHERE upstream_id = u.id AND is_active = 1) AS model_count
		FROM upstreams u WHERE u.is_active = 1 ORDER BY u.id`)
	if err != nil {
		return nil, fmt.Errorf("list upstreams: %w", err)
	}
	defer rows.Close()
	out := make([]Upstream, 0)
	for rows.Next() {
		var u Upstream
		if err := rows.Scan(&u.ID, &u.Name, &u.BaseURL, &u.APIKey, &u.Format, &u.DailyTokenLimit, &u.MonthlyTokenLimit, &u.CreatedAt, &u.UpdatedAt, &u.ModelCount); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func UpdateUpstream(d *sql.DB, u *Upstream) error {
	_, err := d.Exec(db.Rebind(d, `UPDATE upstreams SET name=?, base_url=?, api_key=?, format=?, daily_token_limit=?, monthly_token_limit=?, updated_at=? WHERE id=? AND is_active = 1`),
		u.Name, u.BaseURL, u.APIKey, u.Format, u.DailyTokenLimit, u.MonthlyTokenLimit, time.Now(), u.ID)
	if err != nil {
		return fmt.Errorf("update upstream %d: %w", u.ID, err)
	}
	return nil
}

// DeleteUpstream 软删除上游及其模型：置 is_active=0 后网关按名称解析即失败，
// 行保留供用量/归档历史关联。部分唯一索引不占名额，同名可重建。
// 两条 UPDATE 包在一个事务里，避免上游已删而模型残留活跃孤儿行。
func DeleteUpstream(d *sql.DB, id int64) error {
	now := time.Now()
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin delete upstream %d: %w", id, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(db.Rebind(d, `UPDATE upstreams SET is_active = 0, updated_at=? WHERE id=? AND is_active = 1`), now, id); err != nil {
		return fmt.Errorf("delete upstream %d: %w", id, err)
	}
	if _, err := tx.Exec(db.Rebind(d, `UPDATE upstream_models SET is_active = 0 WHERE upstream_id=? AND is_active = 1`), id); err != nil {
		return fmt.Errorf("delete upstream %d models: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete upstream %d: %w", id, err)
	}
	return nil
}

const DefaultModelContextLength = 200000
const DefaultModelMaxOutputLength = 200000

func ListModels(d *sql.DB, upstreamID int64) ([]UpstreamModel, error) {
	rows, err := d.Query(db.Rebind(d, `SELECT id, upstream_id, model_name, manual, context_length, max_output_length FROM upstream_models WHERE upstream_id=? AND is_active = 1 ORDER BY model_name`), upstreamID)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()
	out := make([]UpstreamModel, 0)
	for rows.Next() {
		var m UpstreamModel
		var manual int
		if err := rows.Scan(&m.ID, &m.UpstreamID, &m.ModelName, &manual, &m.ContextLength, &m.MaxOutputLength); err != nil {
			return nil, err
		}
		m.Manual = manual != 0
		out = append(out, m)
	}
	return out, nil
}

func AddModel(d *sql.DB, upstreamID int64, modelName string, manual bool, contextLength, maxOutputLength int) error {
	m := 0
	if manual {
		m = 1
	}
	if contextLength <= 0 {
		contextLength = DefaultModelContextLength
	}
	if maxOutputLength <= 0 {
		maxOutputLength = DefaultModelMaxOutputLength
	}
	// 已有活跃同名行则无需动作（等效于下面的 ON CONFLICT DO NOTHING，
	// 提前判断避免误复活同名的软删除死行造成唯一索引冲突）。
	var active int
	if err := d.QueryRow(db.Rebind(d, `SELECT COUNT(*) FROM upstream_models WHERE upstream_id=? AND model_name=? AND is_active = 1`), upstreamID, modelName).Scan(&active); err != nil {
		return fmt.Errorf("check model: %w", err)
	}
	if active > 0 {
		return nil
	}
	// 优先复活同名的软删除行（删除后重加是常见路径；直接插入会累积同名
	// 死行，还会在 ReplaceModels 复活时撞部分唯一索引）。只复活最早一行，
	// 防止历史库里极端情况下存在多条同名死行。
	res, err := d.Exec(db.Rebind(d, `UPDATE upstream_models SET is_active = 1, manual=?, context_length=?, max_output_length=? WHERE id = (SELECT MIN(id) FROM upstream_models WHERE upstream_id=? AND model_name=? AND is_active = 0)`),
		m, contextLength, maxOutputLength, upstreamID, modelName)
	if err != nil {
		return fmt.Errorf("revive model: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	// 唯一性由「仅活跃行」的部分唯一索引保证；WHERE 子句与索引谓词对应。
	_, err = d.Exec(db.Rebind(d, `INSERT INTO upstream_models (upstream_id, model_name, manual, context_length, max_output_length) VALUES (?,?,?,?,?) ON CONFLICT (upstream_id, model_name) WHERE is_active = 1 DO NOTHING`),
		upstreamID, modelName, m, contextLength, maxOutputLength)
	if err != nil {
		return fmt.Errorf("add model: %w", err)
	}
	return nil
}

func UpdateModel(d *sql.DB, id int64, contextLength, maxOutputLength int) error {
	if contextLength <= 0 {
		contextLength = DefaultModelContextLength
	}
	if maxOutputLength <= 0 {
		maxOutputLength = DefaultModelMaxOutputLength
	}
	_, err := d.Exec(db.Rebind(d, `UPDATE upstream_models SET context_length=?, max_output_length=? WHERE id=? AND is_active = 1`),
		contextLength, maxOutputLength, id)
	if err != nil {
		return fmt.Errorf("update model: %w", err)
	}
	return nil
}

// DeleteModel 软删除：模型立即从列表与网关路由中消失，行保留供历史关联。
func DeleteModel(d *sql.DB, id int64) error {
	_, err := d.Exec(db.Rebind(d, `UPDATE upstream_models SET is_active = 0 WHERE id=? AND is_active = 1`), id)
	if err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	return nil
}

func ReplaceModels(d *sql.DB, upstreamID int64, names []string) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	// Preserve user-configured lengths across re-fetch: snapshot the current
	// rows before soft-deleting them. manual 一并记录：软删除只动 manual=0，
	// 所以复活时还活跃的同名行只可能是手动模型——此时不能复活旧的自动行，
	// 否则两行同活跃在部分唯一索引上冲突（整个同步事务失败）。
	type snapshot struct {
		cl, ml int
		manual bool
	}
	prev := make(map[string]snapshot)
	rows, err := tx.Query(db.Rebind(d, `SELECT model_name, context_length, max_output_length, manual FROM upstream_models WHERE upstream_id=? AND is_active = 1`), upstreamID)
	if err != nil {
		return fmt.Errorf("snapshot models: %w", err)
	}
	for rows.Next() {
		var name string
		var cl, ml, manual int
		if err := rows.Scan(&name, &cl, &ml, &manual); err != nil {
			rows.Close()
			return fmt.Errorf("snapshot models: %w", err)
		}
		prev[name] = snapshot{cl, ml, manual != 0}
	}
	rows.Close()
	// 同步删除改为软删除：行保留，若模型随后重新出现在上游列表里可复活，
	// 避免重复行累积。
	if _, err := tx.Exec(db.Rebind(d, `UPDATE upstream_models SET is_active = 0 WHERE upstream_id=? AND manual=0 AND is_active = 1`), upstreamID); err != nil {
		return fmt.Errorf("delete non-manual: %w", err)
	}
	for _, n := range names {
		p, ok := prev[n]
		cl, ml := DefaultModelContextLength, DefaultModelMaxOutputLength
		if ok {
			cl, ml = p.cl, p.ml
		}
		if ok && p.manual {
			// 同名手动模型仍活跃：跳过复活与插入，保留手动行。
			continue
		}
		// 复活已软删除的同名自动模型（保留历史 id），再按需插入新行。
		if _, err := tx.Exec(db.Rebind(d, `UPDATE upstream_models SET is_active = 1, context_length=?, max_output_length=? WHERE upstream_id=? AND model_name=? AND manual=0 AND is_active = 0`), cl, ml, upstreamID, n); err != nil {
			return fmt.Errorf("revive model %s: %w", n, err)
		}
		if _, err := tx.Exec(db.Rebind(d, `INSERT INTO upstream_models (upstream_id, model_name, manual, context_length, max_output_length) VALUES (?,?,0,?,?) ON CONFLICT (upstream_id, model_name) WHERE is_active = 1 DO NOTHING`), upstreamID, n, cl, ml); err != nil {
			return fmt.Errorf("insert model %s: %w", n, err)
		}
	}
	return tx.Commit()
}
