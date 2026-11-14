package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/great-magician-01/any-llm/internal/db"
)

// DefaultAliasProfile 是未指定配置名时的默认配置。存量别名（升级前建的）
// 由 LateAdd 默认值落到这里，行为与「没有多配置」时完全一致。
const DefaultAliasProfile = "default"

// maxAliasProfileLen 与 DB 列宽（MySQL VARCHAR(64)）对齐，按字符数计。
const maxAliasProfileLen = 64

// NormalizeAliasProfile 归一配置名：裁剪空白后空串 → DefaultAliasProfile，
// 超长（按字符数）报错。配置名允许任意字符，管理端与配置导入共用这一口径。
func NormalizeAliasProfile(profile string) (string, error) {
	p := strings.TrimSpace(profile)
	if p == "" {
		return DefaultAliasProfile, nil
	}
	if utf8.RuneCountInString(p) > maxAliasProfileLen {
		return "", fmt.Errorf("alias profile name too long (max %d chars)", maxAliasProfileLen)
	}
	return p, nil
}

// ModelAlias 固定对外模型名：客户端用别名请求，网关按绑定优先级依次尝试
// 「上游+真实模型」，对外名称不变而内里可自由切换、自动故障转移。
//
// 一个别名可存多套命名配置（profile）：每套配置是一条独立的绑定链，
// ActiveProfile 指定当前生效的那套；管理端手动切换，网关只解析当前配置。
type ModelAlias struct {
	ID            int64          `json:"id"`
	Name          string         `json:"name"`
	ActiveProfile string         `json:"active_profile"`
	Bindings      []AliasBinding `json:"bindings"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// AliasBinding 别名的一条候选绑定。Priority 升序即尝试顺序（0 最先）。
// Profile 是所属配置名；网关只取与该别名 ActiveProfile 相同的绑定。
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
	Profile         string `json:"profile"`
}

// ErrAliasProfileNotFound 表示要切换到的配置在该别名下没有任何活跃绑定。
var ErrAliasProfileNotFound = errors.New("alias profile not found")

// normalizeAliasProfiles 归一别名及其绑定的配置名，并补全 ActiveProfile：
// 未给（空串）时取第一条绑定所属配置；显式给出的非空配置必须存在于绑定中，
// 否则报错（切换/更新的目标配置不能是空配置）。
func normalizeAliasProfiles(a *ModelAlias) error {
	for i := range a.Bindings {
		p, err := NormalizeAliasProfile(a.Bindings[i].Profile)
		if err != nil {
			return fmt.Errorf("binding %d: %w", i, err)
		}
		a.Bindings[i].Profile = p
	}
	if strings.TrimSpace(a.ActiveProfile) == "" {
		if len(a.Bindings) > 0 {
			a.ActiveProfile = a.Bindings[0].Profile
		} else {
			a.ActiveProfile = DefaultAliasProfile
		}
		return nil
	}
	p, err := NormalizeAliasProfile(a.ActiveProfile)
	if err != nil {
		return err
	}
	a.ActiveProfile = p
	if len(a.Bindings) > 0 && !hasAliasProfile(a.Bindings, p) {
		return fmt.Errorf("active profile %q has no bindings", p)
	}
	return nil
}

func hasAliasProfile(bindings []AliasBinding, profile string) bool {
	for _, b := range bindings {
		if b.Profile == profile {
			return true
		}
	}
	return false
}

// readAliasProfile 归一从库中读到的配置名：写路径保证非空且已裁剪，这里
// 只兜住手改库/框架外写入的空白值，避免 ActiveProfile 与绑定 profile 因
// 空白差异匹配不上。
func readAliasProfile(profile string) string {
	p := strings.TrimSpace(profile)
	if p == "" {
		return DefaultAliasProfile
	}
	return p
}

// AliasTarget 是网关故障转移循环的一个候选：解析后的上游与其上的真实模型。
type AliasTarget struct {
	Upstream  *Upstream
	ModelName string
}

// CreateAlias 创建别名及其绑定。bindings 的数组顺序即优先级（重写为 0..n-1）。
// 配置名按 normalizeAliasProfiles 归一；ActiveProfile 未给时取第一条绑定的配置。
func CreateAlias(d *sql.DB, a *ModelAlias) (int64, error) {
	if err := normalizeAliasProfiles(a); err != nil {
		return 0, fmt.Errorf("create alias %q: %w", a.Name, err)
	}
	tx, err := d.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin create alias: %w", err)
	}
	defer tx.Rollback()
	now := time.Now()
	id, err := db.InsertReturningIDTx(tx, d, `INSERT INTO model_aliases (name, active_profile, created_at, updated_at) VALUES (?,?,?,?) RETURNING id`,
		a.Name, a.ActiveProfile, now, now)
	if err != nil {
		return 0, fmt.Errorf("create alias %q: %w", a.Name, err)
	}
	if err := insertBindings(tx, d, id, a.Bindings); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit create alias %q: %w", a.Name, err)
	}
	// 逐出同名条目（软删后重建的场景：DeleteAlias 已逐出，这里是防御），
	// 下一个请求按新绑定重新解析。
	evictAliasByName(a.Name)
	return id, nil
}

// insertBindings 按数组顺序写入绑定，priority 归一化为 0..n-1（与「仅活跃行」
// 部分唯一索引 (alias_id, priority) 对应）。
func insertBindings(tx *sql.Tx, d *sql.DB, aliasID int64, bindings []AliasBinding) error {
	for i, b := range bindings {
		_, err := tx.Exec(db.Rebind(d, `INSERT INTO model_alias_bindings (alias_id, upstream_id, model_name, priority, profile) VALUES (?,?,?,?,?)`),
			aliasID, b.UpstreamID, b.ModelName, i, readAliasProfile(b.Profile))
		if err != nil {
			return fmt.Errorf("insert binding %d (upstream_id=%d model=%q profile=%q): %w", i, b.UpstreamID, b.ModelName, b.Profile, err)
		}
	}
	return nil
}

// GetAliasByName 按名称取活跃别名及其全部绑定（配置导入按名覆盖时查重用）。
func GetAliasByName(d *sql.DB, name string) (*ModelAlias, error) {
	a := &ModelAlias{}
	err := d.QueryRow(db.Rebind(d, `SELECT id, name, active_profile, created_at, updated_at FROM model_aliases WHERE name=? AND is_active = 1`), name).
		Scan(&a.ID, &a.Name, &a.ActiveProfile, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get alias by name %q: %w", name, err)
	}
	a.ActiveProfile = readAliasProfile(a.ActiveProfile)
	bindings, err := listBindings(d, `WHERE b.alias_id = ? AND b.is_active = 1`, a.ID)
	if err != nil {
		return nil, err
	}
	a.Bindings = bindings
	return a, nil
}

// GetAliasByID 取单个别名及其全部活跃绑定（含上游名，按优先级排序）。
func GetAliasByID(d *sql.DB, id int64) (*ModelAlias, error) {
	a := &ModelAlias{}
	err := d.QueryRow(db.Rebind(d, `SELECT id, name, active_profile, created_at, updated_at FROM model_aliases WHERE id=? AND is_active = 1`), id).
		Scan(&a.ID, &a.Name, &a.ActiveProfile, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get alias %d: %w", id, err)
	}
	a.ActiveProfile = readAliasProfile(a.ActiveProfile)
	bindings, err := listBindings(d, `WHERE b.alias_id = ? AND b.is_active = 1`, id)
	if err != nil {
		return nil, err
	}
	a.Bindings = bindings
	return a, nil
}

// ListAliases 列出全部活跃别名及其绑定（含上游名）。
func ListAliases(d *sql.DB) ([]ModelAlias, error) {
	rows, err := d.Query(`SELECT id, name, active_profile, created_at, updated_at FROM model_aliases WHERE is_active = 1 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list aliases: %w", err)
	}
	defer rows.Close()
	out := make([]ModelAlias, 0)
	for rows.Next() {
		var a ModelAlias
		if err := rows.Scan(&a.ID, &a.Name, &a.ActiveProfile, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.ActiveProfile = readAliasProfile(a.ActiveProfile)
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
// 网关解析用 resolveAliasCandidates（经 CachedAliasTargets 读缓存），会跳过上游
// 不活跃或已禁用的绑定。
func listBindings(d *sql.DB, where string, args ...any) ([]AliasBinding, error) {
	rows, err := d.Query(db.Rebind(d, `SELECT b.id, b.alias_id, b.upstream_id, b.model_name, b.priority, b.profile,
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
		if err := rows.Scan(&b.ID, &b.AliasID, &b.UpstreamID, &b.ModelName, &b.Priority, &b.Profile, &b.UpstreamName, &upstreamEnabled); err != nil {
			return nil, err
		}
		b.Profile = readAliasProfile(b.Profile)
		b.UpstreamEnabled = upstreamEnabled != 0
		out = append(out, b)
	}
	return out, rows.Err()
}

// ResolveAliasTargets 按对外名称解析别名当前生效配置（active_profile）的候选链，
// 返回按优先级排序、且未过有效期的候选。found=false 表示别名不存在（调用方回落
// 到 name/model 直连拆分）；found=true 但 targets 为空表示别名存在但当前配置
// 无可用绑定。网关热路径请用 CachedAliasTargets（同一口径，只是走读缓存）。
//
// 过期判定放在 Go 侧（比较 now 与行上的 ExpiresAt）而不是 SQL 的 JOIN 条件：
// SQLite 的 DATETIME 文本与 PG 的 timestamp 在驱动层的格式/时区处理不一致，
// 跨方言写比较条件容易踩坑；而候选行本就是完整读出来的，Go 侧判断零成本。
func ResolveAliasTargets(d *sql.DB, name string, now time.Time) (found bool, targets []AliasTarget, err error) {
	found, _, candidates, err := resolveAliasCandidates(d, name)
	if err != nil || !found {
		return found, nil, err
	}
	return true, liveTargets(candidates, now), nil
}

// resolveAliasCandidates 按对外名称精确匹配活跃别名，返回当前生效配置
// （active_profile）的原始候选链（不过滤有效期，按优先级排序）。found=false
// 表示别名不存在。缓存路径与未缓存路径共用这一份解析逻辑：缓存存原始候选，
// 过期判定留给读时按 now 做，这样「时间走过 expires_at」不需要失效任何条目。
// 切换 active_profile 由 SetAliasActiveProfile 写库后逐出缓存，因此缓存条目
// 只对应解析时的生效配置。
func resolveAliasCandidates(d *sql.DB, name string) (found bool, id int64, candidates []AliasTarget, err error) {
	var activeProfile string
	err = d.QueryRow(db.Rebind(d, `SELECT id, active_profile FROM model_aliases WHERE name=? AND is_active = 1`), name).Scan(&id, &activeProfile)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil, nil
	}
	if err != nil {
		return false, 0, nil, fmt.Errorf("resolve alias %q: %w", name, err)
	}
	rows, err := d.Query(db.Rebind(d, `SELECT `+upstreamColsPrefixed("u")+`, b.model_name
		FROM model_alias_bindings b JOIN upstreams u ON u.id = b.upstream_id AND u.is_active = 1 AND u.enabled = 1
		WHERE b.alias_id = ? AND b.is_active = 1 AND b.profile = ? ORDER BY b.priority, b.id`), id, readAliasProfile(activeProfile))
	if err != nil {
		return true, 0, nil, fmt.Errorf("resolve alias %q bindings: %w", name, err)
	}
	defer rows.Close()
	for rows.Next() {
		t := AliasTarget{Upstream: &Upstream{}}
		row := &upstreamRow{u: t.Upstream}
		if err := rows.Scan(append(row.targets(), &t.ModelName)...); err != nil {
			return true, 0, nil, err
		}
		row.finish()
		candidates = append(candidates, t)
	}
	return true, id, candidates, rows.Err()
}

// UpdateAlias 改名、切换生效配置并整体替换绑定：旧绑定全软删、按新顺序插入
// （绑定无外部引用，不像 upstream_models 需要复活逻辑），同事务保证不半更新。
func UpdateAlias(d *sql.DB, a *ModelAlias) error {
	if err := normalizeAliasProfiles(a); err != nil {
		return fmt.Errorf("update alias %d: %w", a.ID, err)
	}
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin update alias %d: %w", a.ID, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(db.Rebind(d, `UPDATE model_aliases SET name=?, active_profile=?, updated_at=? WHERE id=? AND is_active = 1`),
		a.Name, a.ActiveProfile, time.Now(), a.ID); err != nil {
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
	// 按 ID 逐出（覆盖改名前的旧名——缓存以名称为键，旧名只有按 ID 扫才找得到），
	// 再按新名逐出，下一个请求重新解析。
	evictAliasByID(a.ID)
	evictAliasByName(a.Name)
	return nil
}

// SetAliasActiveProfile 切换别名的生效配置：只改 active_profile，不动绑定。
// 目标配置必须在该别名下至少有一条活跃绑定，否则返回 ErrAliasProfileNotFound。
// 写成功后逐出该别名的读缓存（按行 ID 扫，覆盖改名前的旧名），下一个请求
// 立即按新配置解析——与其它配置写走同一套写时失效语义。
func SetAliasActiveProfile(d *sql.DB, id int64, profile string) error {
	p, err := NormalizeAliasProfile(profile)
	if err != nil {
		return err
	}
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin activate alias %d: %w", id, err)
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRow(db.Rebind(d, `SELECT is_active FROM model_aliases WHERE id=?`), id).Scan(&active); err != nil {
		return fmt.Errorf("activate alias %d: %w", id, err)
	}
	if active == 0 {
		return fmt.Errorf("activate alias %d: %w", id, sql.ErrNoRows)
	}
	var n int
	if err := tx.QueryRow(db.Rebind(d, `SELECT COUNT(*) FROM model_alias_bindings WHERE alias_id=? AND profile=? AND is_active=1`), id, p).Scan(&n); err != nil {
		return fmt.Errorf("activate alias %d profile %q: %w", id, p, err)
	}
	if n == 0 {
		return fmt.Errorf("activate alias %d: %w", id, ErrAliasProfileNotFound)
	}
	res, err := tx.Exec(db.Rebind(d, `UPDATE model_aliases SET active_profile=?, updated_at=? WHERE id=? AND is_active=1`),
		p, time.Now(), id)
	if err != nil {
		return fmt.Errorf("activate alias %d profile %q: %w", id, p, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		// 读检查之后、写之前被并发软删（多实例共享库时才可能）：按不存在处理
		return fmt.Errorf("activate alias %d: %w", id, sql.ErrNoRows)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit activate alias %d: %w", id, err)
	}
	evictAliasByID(id)
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
	evictAliasByID(id)
	return nil
}
