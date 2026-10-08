package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/logger"
)

// marshalExtraEndpoints 序列化附加端点列为 JSON 文本；空列表落成空串（与列默认值
// 一致），避免无附加端点的行在库里存两种写法（空串与 "[]"）。
func marshalExtraEndpoints(eps []UpstreamEndpoint) string {
	if len(eps) == 0 {
		return ""
	}
	b, err := json.Marshal(eps)
	if err != nil {
		// 两个 string 字段不可能 marshal 失败；兜底存空，别让脏配置写进库。
		return ""
	}
	return string(b)
}

// parseExtraEndpoints 反序列化附加端点列。脏数据（非法 JSON）按「无附加端点」
// 处理并 warn：该列在网关热路径（GetUpstreamByName / 别名解析）上被读，一条
// 坏数据不能把整个上游读挂——此时该上游退化为单格式主端点，行为与旧版本一致。
func parseExtraEndpoints(raw string) []UpstreamEndpoint {
	if raw == "" {
		return nil
	}
	var eps []UpstreamEndpoint
	if err := json.Unmarshal([]byte(raw), &eps); err != nil {
		logger.Warn("store: invalid extra_endpoints JSON, treating as none", "err", err)
		return nil
	}
	return eps
}

// timePtr 把可空时间列转成 *time.Time（无效 → nil）。与 extkey.go 里
// LastUsedAt 的写法等价，这里抽成函数是因为上游有四个读取站点都要用。
func timePtr(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	t := nt.Time
	return &t
}

// nullTime 把可空到期时间转成 sql.NullTime 再绑定（nil → SQL NULL）。不直接
// 传 *time.Time：pgx 的 stdlib driver 对 driver.Valuer 是直通的
// （CheckNamedValue 直接 return nil），nil 指针的 Valuer 行为两个驱动不一致。
func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// upstreamCols 是 upstreams 表读取用的列清单，顺序与 upstreamRow.targets 一一
// 对应。四个读取站点（三个直查 + alias.go 的 JOIN，后者用 upstreamColsPrefixed
// 加表前缀）共用这一份，加列只改这里与 upstreamRow，不再多处手写漂移。
const upstreamCols = "id, name, base_url, api_key, format, remark, tag, enabled, daily_token_limit, monthly_token_limit, max_concurrent, created_at, updated_at, expires_at, extra_endpoints"

// upstreamColsPrefixed 渲染带表前缀的列清单（别名候选链的 JOIN 查询用）。
func upstreamColsPrefixed(prefix string) string {
	parts := strings.Split(upstreamCols, ", ")
	for i := range parts {
		parts[i] = prefix + "." + parts[i]
	}
	return strings.Join(parts, ", ")
}

// upstreamRow 是 upstreamCols 一行的扫描载体：targets 给出 Scan 目标，finish
// 把整型布尔、可空时间与 JSON 文本列落到 Upstream 字段上。
type upstreamRow struct {
	u         *Upstream
	enabled   int
	tag       string
	expiresAt sql.NullTime
	extraRaw  string
}

func (r *upstreamRow) targets() []any {
	return []any{&r.u.ID, &r.u.Name, &r.u.BaseURL, &r.u.APIKey, &r.u.Format, &r.u.Remark, &r.tag,
		&r.enabled, &r.u.DailyTokenLimit, &r.u.MonthlyTokenLimit, &r.u.MaxConcurrent,
		&r.u.CreatedAt, &r.u.UpdatedAt, &r.expiresAt, &r.extraRaw}
}

func (r *upstreamRow) finish() {
	r.u.Enabled = r.enabled != 0
	r.u.Tag = readTag(r.tag)
	r.u.ExpiresAt = timePtr(r.expiresAt)
	r.u.ExtraEndpoints = parseExtraEndpoints(r.extraRaw)
}

// readTag 把列里的标记归一成两个合法值之一。写库路径都过 NormalizeTag，正常不会
// 有脏值；手改库/框架外写入除外。此时 warn 后按默认官方处理（与
// parseExtraEndpoints 容忍坏 JSON 同款）：一行坏数据不能让管理端列表读不出来。
func readTag(raw string) string {
	switch strings.TrimSpace(raw) {
	case "", TagOfficial:
		return TagOfficial
	case TagRelay:
		return TagRelay
	}
	logger.Warn("store: invalid upstream tag, treating as official", "tag", raw)
	return TagOfficial
}

func CreateUpstream(d *sql.DB, u *Upstream) (int64, error) {
	// 附加端点与主格式的撞车校验收敛到写库边界：adminapi 各入口的提前校验
	// 只是体验优化，这里才是最后防线——配置导入「文件改主格式 + 缺省保留
	// 库中附加端点」的合并结果只有在这一层才能被无条件兜住。
	if err := ValidateExtraEndpoints(u.Format, u.ExtraEndpoints); err != nil {
		return 0, err
	}
	// 标记同样在写库边界归一：空串（调用方没填）落默认官方，非法值响亮拒绝。
	// UPDATE ... SET 里的 tag 由 UpdateUpstream 用同一个函数算，两侧口径一致。
	tag, err := NormalizeTag(u.Tag)
	if err != nil {
		return 0, err
	}
	// 成功后要按名称逐出缓存条目：上游名在活跃行里唯一，但「删掉再建同名」是常见
	// 操作，旧条目不逐出就会把新行整个遮蔽掉（缓存以名称为键，与 ID 无关）。
	// 新 ID 不可能被现有绑定引用，故顺带刷新别名缓存只是防御，本可省。
	now := time.Now()
	id, err := db.InsertReturningID(d, `INSERT INTO upstreams (name, base_url, api_key, format, remark, tag, daily_token_limit, monthly_token_limit, max_concurrent, created_at, updated_at, expires_at, extra_endpoints) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id`,
		u.Name, u.BaseURL, u.APIKey, u.Format, u.Remark, tag, u.DailyTokenLimit, u.MonthlyTokenLimit, u.MaxConcurrent, now, now, nullTime(u.ExpiresAt), marshalExtraEndpoints(u.ExtraEndpoints))
	if err != nil {
		return 0, fmt.Errorf("create upstream: %w", err)
	}
	evictUpstream(id, u.Name)
	return id, nil
}

func GetUpstreamByID(d *sql.DB, id int64) (*Upstream, error) {
	u := &Upstream{}
	row := &upstreamRow{u: u}
	err := d.QueryRow(db.Rebind(d, `SELECT `+upstreamCols+` FROM upstreams WHERE id=? AND is_active = 1`), id).Scan(row.targets()...)
	if err != nil {
		return nil, fmt.Errorf("get upstream %d: %w", id, err)
	}
	row.finish()
	return u, nil
}

// GetUpstreamByName 不过滤 enabled（软删除仍过滤）：网关直连路径需要拿到
// 禁用的上游行以返回明确的「已禁用」错误，而非笼统的 not found。
func GetUpstreamByName(d *sql.DB, name string) (*Upstream, error) {
	u := &Upstream{}
	row := &upstreamRow{u: u}
	err := d.QueryRow(db.Rebind(d, `SELECT `+upstreamCols+` FROM upstreams WHERE name=? AND is_active = 1`), name).Scan(row.targets()...)
	if err != nil {
		return nil, fmt.Errorf("get upstream by name %q: %w", name, err)
	}
	row.finish()
	return u, nil
}

// ListUpstreams 返回全部未软删上游；enabled 非 nil 时按启用状态过滤（仅供管理端
// 列表接口的 ?status 参数）。网关 /v1/models、余额轮询、配置导出等调用方传 nil
// 拿全量再自行过滤（禁用跳过、到期隐藏），全量口径不能动。
func ListUpstreams(d *sql.DB, enabled *bool) ([]Upstream, error) {
	q := `SELECT ` + upstreamColsPrefixed("u") + `,
		(SELECT COUNT(*) FROM upstream_models WHERE upstream_id = u.id AND is_active = 1) AS model_count
		FROM upstreams u WHERE u.is_active = 1`
	var args []any
	if enabled != nil {
		q += ` AND u.enabled = ?`
		args = append(args, b2i(*enabled))
	}
	q += ` ORDER BY u.id`
	rows, err := d.Query(db.Rebind(d, q), args...)
	if err != nil {
		return nil, fmt.Errorf("list upstreams: %w", err)
	}
	defer rows.Close()
	out := make([]Upstream, 0)
	for rows.Next() {
		var u Upstream
		row := &upstreamRow{u: &u}
		if err := rows.Scan(append(row.targets(), &u.ModelCount)...); err != nil {
			return nil, err
		}
		row.finish()
		out = append(out, u)
	}
	return out, nil
}

// UpdateUpstream 全量覆盖行内字段。u 必须先 Get 再改再存——不要用字面量构造
// （enabled 等未赋值字段会把已有值清掉）。
func UpdateUpstream(d *sql.DB, u *Upstream) error {
	// 与 CreateUpstream 同理：合并后的（主格式, 附加端点）撞车在写库边界拒绝。
	if err := ValidateExtraEndpoints(u.Format, u.ExtraEndpoints); err != nil {
		return err
	}
	// 标记与 CreateUpstream 同一个归一函数：手拼字面量调本函数的调用方（Tag 为
	// 空串）落到默认官方，非法值被拒。
	tag, err := NormalizeTag(u.Tag)
	if err != nil {
		return err
	}
	_, err = d.Exec(db.Rebind(d, `UPDATE upstreams SET name=?, base_url=?, api_key=?, format=?, remark=?, tag=?, enabled=?, daily_token_limit=?, monthly_token_limit=?, max_concurrent=?, updated_at=?, expires_at=?, extra_endpoints=? WHERE id=? AND is_active = 1`),
		u.Name, u.BaseURL, u.APIKey, u.Format, u.Remark, tag, b2i(u.Enabled), u.DailyTokenLimit, u.MonthlyTokenLimit, u.MaxConcurrent, time.Now(), nullTime(u.ExpiresAt), marshalExtraEndpoints(u.ExtraEndpoints), u.ID)
	if err != nil {
		return fmt.Errorf("update upstream %d: %w", u.ID, err)
	}
	// 别名候选里内嵌的就是这一行，上游任何字段变了绑定链都得重解析，故逐出上游
	// 缓存 + 整份刷新别名缓存。
	evictUpstream(u.ID, u.Name)
	return nil
}

// DeleteUpstream 软删除上游及其模型：置 is_active=0 后网关按名称解析即失败，
// 行保留供用量/归档历史关联。部分唯一索引不占名额，同名可重建。
// 上游的别名绑定一并软删（网关解析别名时本就会跳过不活跃上游的绑定，这里
// 顺手清掉，避免管理端列表残留指向死上游的绑定）。
// 三条 UPDATE 包在一个事务里，避免上游已删而模型/绑定残留活跃孤儿行。
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
	if _, err := tx.Exec(db.Rebind(d, `UPDATE model_alias_bindings SET is_active = 0 WHERE upstream_id=? AND is_active = 1`), id); err != nil {
		return fmt.Errorf("delete upstream %d alias bindings: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete upstream %d: %w", id, err)
	}
	// 删除会级联软删模型与别名绑定，已解析的候选链同样作废：整份刷新别名缓存。
	// 这里只有 ID（上游名未知），按 ID 扫即可定位缓存条目。
	evictUpstream(id, "")
	return nil
}

const DefaultModelContextLength = 1000000
const DefaultModelMaxOutputLength = 200000

// DefaultMaxConcurrent 新建上游未显式给并发上限时的默认值（webapi 创建/配置
// 导入缺省时应用；DB 列默认值同）。0 表示不限。
const DefaultMaxConcurrent = 100

func ListModels(d *sql.DB, upstreamID int64) ([]UpstreamModel, error) {
	rows, err := d.Query(db.Rebind(d, `SELECT id, upstream_id, model_name, manual, context_length, max_output_length, multimodal FROM upstream_models WHERE upstream_id=? AND is_active = 1 ORDER BY model_name`), upstreamID)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()
	out := make([]UpstreamModel, 0)
	for rows.Next() {
		var m UpstreamModel
		var manual, multimodal int
		if err := rows.Scan(&m.ID, &m.UpstreamID, &m.ModelName, &manual, &m.ContextLength, &m.MaxOutputLength, &multimodal); err != nil {
			return nil, err
		}
		m.Manual = manual != 0
		m.Multimodal = multimodal != 0
		out = append(out, m)
	}
	return out, nil
}

// ErrModelExists 表示要添加的模型在该上游已有活跃同名行。AddModel 用它拒绝
// 假成功：管理端「添加」若对已存在模型静默 200，管理员会以为刚配的字段
// （长度、多模态）生效了，其实什么都没改——已存在的模型请走 UpdateModel。
var ErrModelExists = errors.New("model already exists")

// AddModel 添加一个模型。参数打包成 UpstreamModel 而非一串位置参数：manual 与
// multimodal 两个相邻布尔在位置参数下交换了也能编译过，缺参/错序只有运行时才
// 暴露（PR #30 修的就是漏参）。用 m.ModelName / m.Manual / m.ContextLength /
// m.MaxOutputLength / m.Multimodal；长度为 0 时归一为默认值。
func AddModel(d *sql.DB, upstreamID int64, m UpstreamModel) error {
	cl, ml := m.ContextLength, m.MaxOutputLength
	if cl <= 0 {
		cl = DefaultModelContextLength
	}
	if ml <= 0 {
		ml = DefaultModelMaxOutputLength
	}
	// 已有活跃同名行：响亮拒绝而不是静默 200（见 ErrModelExists）。提前判断也
	// 避免了误复活同名的软删除死行造成唯一索引冲突。
	var active int
	if err := d.QueryRow(db.Rebind(d, `SELECT COUNT(*) FROM upstream_models WHERE upstream_id=? AND model_name=? AND is_active = 1`), upstreamID, m.ModelName).Scan(&active); err != nil {
		return fmt.Errorf("check model: %w", err)
	}
	if active > 0 {
		return fmt.Errorf("add model %q: %w", m.ModelName, ErrModelExists)
	}
	// 优先复活同名的软删除行（删除后重加是常见路径；直接插入会累积同名
	// 死行，还会在 ReplaceModels 复活时撞部分唯一索引）。只复活最早一行，
	// 防止历史库里极端情况下存在多条同名死行。
	// 子查询用派生表包一层：MySQL 不允许在 UPDATE 的子查询里引用正在更新的表
	// （错误 1093）。SQLite/PG 接受这种写法，故三种方言共用、无需分支。
	res, err := d.Exec(db.Rebind(d, `UPDATE upstream_models SET is_active = 1, manual=?, context_length=?, max_output_length=?, multimodal=? WHERE id = (SELECT id FROM (SELECT MIN(id) AS id FROM upstream_models WHERE upstream_id=? AND model_name=? AND is_active = 0) AS cand)`),
		b2i(m.Manual), cl, ml, b2i(m.Multimodal), upstreamID, m.ModelName)
	if err != nil {
		return fmt.Errorf("revive model: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	// 唯一性由「仅活跃行」的部分唯一索引保证；冲突时忽略。
	_, err = d.Exec(db.Rebind(d, `INSERT INTO upstream_models (upstream_id, model_name, manual, context_length, max_output_length, multimodal) VALUES (?,?,?,?,?,?)`+db.ConflictIgnoreSuffix(d, "upstream_id, model_name")),
		upstreamID, m.ModelName, b2i(m.Manual), cl, ml, b2i(m.Multimodal))
	if err != nil {
		return fmt.Errorf("add model: %w", err)
	}
	return nil
}

// UpdateModel 更新模型的长度与多模态标记（m.ID 定位行）。WHERE 同时限定
// upstream_id：路径里的上游 ID 与模型 ID 不匹配（或模型不存在/已软删）时
// 更新 0 行，返回包装过的 sql.ErrNoRows，调用方据此回 404 而非假成功。
// 先查后写而非看 RowsAffected：MySQL 驱动默认返回「值有变化」的行数，原值
// 重写在它那里是 0，会误报 404。全部写都经 db.Writer 串行化，查与写之间
// 不会有别的写插入。长度为 0 时归一为默认值。
func UpdateModel(d *sql.DB, upstreamID int64, m UpstreamModel) error {
	cl, ml := m.ContextLength, m.MaxOutputLength
	if cl <= 0 {
		cl = DefaultModelContextLength
	}
	if ml <= 0 {
		ml = DefaultModelMaxOutputLength
	}
	var exists int
	if err := d.QueryRow(db.Rebind(d, `SELECT COUNT(*) FROM upstream_models WHERE id=? AND upstream_id=? AND is_active = 1`), m.ID, upstreamID).Scan(&exists); err != nil {
		return fmt.Errorf("check model %d: %w", m.ID, err)
	}
	if exists == 0 {
		return fmt.Errorf("update model %d: %w", m.ID, sql.ErrNoRows)
	}
	_, err := d.Exec(db.Rebind(d, `UPDATE upstream_models SET context_length=?, max_output_length=?, multimodal=? WHERE id=? AND upstream_id=? AND is_active = 1`),
		cl, ml, b2i(m.Multimodal), m.ID, upstreamID)
	if err != nil {
		return fmt.Errorf("update model: %w", err)
	}
	return nil
}

// DeleteModel 软删除：模型立即从列表与网关路由中消失，行保留供历史关联。
// WHERE 同时限定 upstream_id：路径里的上游 ID 与模型 ID 不匹配（或模型不存在/
// 已软删）时删除 0 行，返回包装过的 sql.ErrNoRows，调用方据此回 404——不能凭
// 模型 ID 跨上游裸删。与 UpdateModel 的「先查后写」不同，这里看 RowsAffected
// 即可：删除必然把 is_active 从 1 改成 0，命中行一定变化，MySQL 的「按值变化
// 计数」口径不影响这个判断。
func DeleteModel(d *sql.DB, upstreamID, id int64) error {
	res, err := d.Exec(db.Rebind(d, `UPDATE upstream_models SET is_active = 0 WHERE id=? AND upstream_id=? AND is_active = 1`), id, upstreamID)
	if err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("delete model %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// ReplaceModelsExact 把上游的活跃模型列表精确替换为 models（配置导入按名
// 覆盖用）：现有活跃行全部软删，再逐名复活最早的软删行或插入新行——不累积
// 重复行、不撞「仅活跃行」部分唯一索引。与 ReplaceModels 的区别：manual 与
// 长度均以传入为准（0 长度归一为默认值），且手动模型不做特殊保留。
// 整个替换在一个事务里。
func ReplaceModelsExact(d *sql.DB, upstreamID int64, models []UpstreamModel) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin replace models exact: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(db.Rebind(d, `UPDATE upstream_models SET is_active = 0 WHERE upstream_id=? AND is_active = 1`), upstreamID); err != nil {
		return fmt.Errorf("clear models: %w", err)
	}
	for _, m := range models {
		cl, ml := m.ContextLength, m.MaxOutputLength
		if cl <= 0 {
			cl = DefaultModelContextLength
		}
		if ml <= 0 {
			ml = DefaultModelMaxOutputLength
		}
		// 优先复活同名软删行，防历史库同名死行累积（与 AddModel 同策略）。
		// 派生表包一层绕开 MySQL 错误 1093（不能引用正在更新的表）。
		res, err := tx.Exec(db.Rebind(d, `UPDATE upstream_models SET is_active = 1, manual=?, context_length=?, max_output_length=?, multimodal=? WHERE id = (SELECT id FROM (SELECT MIN(id) AS id FROM upstream_models WHERE upstream_id=? AND model_name=? AND is_active = 0) AS cand)`),
			b2i(m.Manual), cl, ml, b2i(m.Multimodal), upstreamID, m.ModelName)
		if err != nil {
			return fmt.Errorf("revive model %s: %w", m.ModelName, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			continue
		}
		if _, err := tx.Exec(db.Rebind(d, `INSERT INTO upstream_models (upstream_id, model_name, manual, context_length, max_output_length, multimodal) VALUES (?,?,?,?,?,?)`),
			upstreamID, m.ModelName, b2i(m.Manual), cl, ml, b2i(m.Multimodal)); err != nil {
			return fmt.Errorf("insert model %s: %w", m.ModelName, err)
		}
	}
	return tx.Commit()
}

func ReplaceModels(d *sql.DB, upstreamID int64, names []string) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	// Preserve user-configured lengths across re-fetch: snapshot the current
	// rows before soft-deleting them. 快照连软删行一起读：模型暂时从上游列表
	// 消失（被软删）再回来时，复活要恢复它最后的配置，而不是落回默认值——
	// 管理端手配的长度与 multimodal 都会丢。ORDER BY is_active, id 保证同名
	// 多行时「活跃行优先、否则最新死行」留在 prev 里。
	// activeManual 一并记录：软删除只动 manual=0，所以复活时还活跃的同名行
	// 只可能是手动模型——此时不能复活旧的自动行，否则两行同活跃在部分唯一
	// 索引上冲突（整个同步事务失败）。
	type snapshot struct {
		cl, ml       int
		multimodal   bool
		activeManual bool // 同名活跃手动行存在
	}
	prev := make(map[string]snapshot)
	rows, err := tx.Query(db.Rebind(d, `SELECT model_name, context_length, max_output_length, manual, multimodal, is_active FROM upstream_models WHERE upstream_id=? ORDER BY is_active, id`), upstreamID)
	if err != nil {
		return fmt.Errorf("snapshot models: %w", err)
	}
	for rows.Next() {
		var name string
		var cl, ml, manual, mm, active int
		if err := rows.Scan(&name, &cl, &ml, &manual, &mm, &active); err != nil {
			rows.Close()
			return fmt.Errorf("snapshot models: %w", err)
		}
		prev[name] = snapshot{cl, ml, mm != 0, manual != 0 && active != 0}
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
		mm := 0
		if ok {
			cl, ml = p.cl, p.ml
			mm = b2i(p.multimodal)
		}
		if ok && p.activeManual {
			// 同名手动模型仍活跃：跳过复活与插入，保留手动行。
			continue
		}
		// 复活已软删除的同名自动模型（保留历史 id），再按需插入新行。
		if _, err := tx.Exec(db.Rebind(d, `UPDATE upstream_models SET is_active = 1, context_length=?, max_output_length=?, multimodal=? WHERE upstream_id=? AND model_name=? AND manual=0 AND is_active = 0`), cl, ml, mm, upstreamID, n); err != nil {
			return fmt.Errorf("revive model %s: %w", n, err)
		}
		if _, err := tx.Exec(db.Rebind(d, `INSERT INTO upstream_models (upstream_id, model_name, manual, context_length, max_output_length, multimodal) VALUES (?,?,0,?,?,?)`+db.ConflictIgnoreSuffix(d, "upstream_id, model_name")), upstreamID, n, cl, ml, mm); err != nil {
			return fmt.Errorf("insert model %s: %w", n, err)
		}
	}
	return tx.Commit()
}
