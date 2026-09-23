package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// 本文件是 any-llm 唯一的 schema 定义。所有方言的 CREATE TABLE / CREATE INDEX /
// ALTER TABLE ADD COLUMN 都由这里的一份描述渲染而来 —— 加第四个数据库时只需在
// sqlType 与索引渲染里补一个分支，不再复制整段 DDL。
//
// 历史上这份 schema 被写过三遍（migrationSQLite、migrationPG，以及
// sqliteSoftDeleteSpecs 里的重建 DDL），代码里留着「两处必须同步」的警告注释。
// 现在那些副本都删了：重建 DDL 与补列 DDL 同样从这里的定义渲染。
//
// 表设计原则（沿用原注释）：不使用外键约束，删除一律应用层软删除（is_active=0），
// 唯一性用「仅活跃行」的部分唯一索引实现 —— 软删除的行不占唯一名额，同名资源
// 删除后可重建。MySQL 没有部分索引，见 partialUniqueColumns 的降级说明。

// ColType 是列的语义类型，与方言无关。
type ColType int

const (
	TypeInt      ColType = iota // 整数（0/1 布尔也用它）
	TypeBigInt                  // 64 位整数；AutoID 时渲染成各方言的自增主键
	TypeText                    // 变长文本
	TypeLongText                // 长文本：MySQL 上 TEXT 只有 64 KiB，长内容必须用 LONGTEXT
	TypeJSON                    // SQLite TEXT / PG JSONB / MySQL JSON
	TypeBytes                   // SQLite BLOB / PG BYTEA / MySQL LONGBLOB
	TypeTime                    // SQLite DATETIME / PG TIMESTAMP(0) / MySQL DATETIME(0)
)

// Column 是一列的规范定义。Nullable/Default 三种方言共用，具体类型由渲染器给出。
type Column struct {
	Name     string
	Type     ColType
	Nullable bool
	// Default 是 SQL 字面量（"0"、"'ok'"）或 CURRENT_TIMESTAMP 这类关键字；
	// 空串 = 无默认值。
	Default string
	// LateAdd 表示这列是初始 schema 之后才加入的：老库升级后合理缺失它，
	// migrateExtraCols 启动时按这份定义回填（幂等）。给已有的表加列时置上它
	// 即完成登记——没有第二处需要同步的清单。初始 schema 就有的列永远不要置它：
	// 同名表缺初始列说明那库不是本项目建的，应在查询时响亮报错、人工修库，
	// 而不是静默补一个语义不明的空壳列。
	LateAdd bool
	// AutoID 表示自增主键，每表最多一个，与 PrimaryKey 互斥。
	AutoID bool
	// PrimaryKey 用于非自增主键（如 response_sessions.id）。
	PrimaryKey bool
	// Len 只对 MySQL 有意义，见下方 validateMySQL。0 = 保持 TEXT。
	Len int
}

// Index 是一个索引。
type Index struct {
	Name    string
	Columns []string
	Unique  bool
	// Where 非空 = 「仅活跃行」部分唯一索引的谓词，PG/SQLite 原生支持。
	// MySQL 没有部分索引，降级为「生成列 + 普通唯一键」，见 partialUniqueColumns。
	Where string
	// Tolerant 表示 PG/SQLite 上建这个索引允许失败，由 ensureExtKeyLabelIndex
	// 单独容错处理（打出重名明细、不阻断启动）。MySQL 上它随 CREATE TABLE 内联建，
	// 且不存在需要宽容的存量库，故不受此标记影响。
	Tolerant bool
}

// Table 是一张表的规范定义。
type Table struct {
	Name string
	Cols []Column
	Idx  []Index
	// Only 非空表示该表只在这些方言建（id_sequences 仅 MySQL 需要）。
	Only Dialect
}

// DDLConfig 控制一次渲染的行为。
type DDLConfig struct {
	IfNotExists bool
	// IndexSuffix 把索引名里的 "{}" 替换成它，用于「同一份索引定义要渲染出多个
	// 带区分后缀的名字」（索引名 schema 级唯一）。注意：对话归档月分表**不走这条路**
	// —— 它的索引名由 convShardIndexes(suffix) 直接拼好，故当前没有调用方设它。
	// 保留是为了让渲染器自身保持自洽：三处取索引名的地方（SQLite/PG 索引语句、
	// MySQL 内联 KEY、生成列名）共用 resolveIndexName，后缀要么都应用、要么都不应用。
	IndexSuffix string
	// ColumnDefaults 覆盖单列的默认值表达式（键为列名）。分表的 id 列在 PG 上
	// 是 DEFAULT nextval('<共享序列>')，MySQL 上交给计数器表显式分配、无默认值。
	ColumnDefaults map[string]string
}

var schemaTables = []Table{
	{
		Name: "upstreams",
		Cols: []Column{
			{Name: "id", Type: TypeBigInt, AutoID: true},
			{Name: "name", Type: TypeText, Len: 255},
			{Name: "base_url", Type: TypeText},
			{Name: "api_key", Type: TypeText},
			{Name: "format", Type: TypeText, Len: 32},
			{Name: "enabled", Type: TypeInt, Default: "1", LateAdd: true},
			{Name: "daily_token_limit", Type: TypeInt, Default: "0", LateAdd: true},
			{Name: "monthly_token_limit", Type: TypeInt, Default: "0", LateAdd: true},
			// 每上游并发上限（默认 100，0 = 不限）
			{Name: "max_concurrent", Type: TypeInt, Default: "100", LateAdd: true},
			{Name: "created_at", Type: TypeTime, Default: "CURRENT_TIMESTAMP"},
			{Name: "updated_at", Type: TypeTime, Default: "CURRENT_TIMESTAMP"},
			// 有效期截止时刻；可空，NULL = 永久有效。到点后网关侧等同禁用（见
			// model.Upstream.Expired）。
			{Name: "expires_at", Type: TypeTime, Nullable: true, LateAdd: true},
			{Name: "is_active", Type: TypeInt, Default: "1", LateAdd: true},
		},
		Idx: []Index{
			{Name: "idx_upstreams_name", Columns: []string{"name"}, Unique: true, Where: "is_active = 1"},
		},
	},
	{
		Name: "upstream_models",
		Cols: []Column{
			{Name: "id", Type: TypeBigInt, AutoID: true},
			{Name: "upstream_id", Type: TypeBigInt},
			{Name: "model_name", Type: TypeText, Len: 255},
			{Name: "manual", Type: TypeInt, Default: "0"},
			{Name: "context_length", Type: TypeInt, Default: "1000000", LateAdd: true},
			{Name: "max_output_length", Type: TypeInt, Default: "200000", LateAdd: true},
			// 是否多模态（可接受图片等非文本输入）：0/1，默认否。管理端配置与
			// 展示用，网关转发路径不读它。
			{Name: "multimodal", Type: TypeInt, Default: "0", LateAdd: true},
			{Name: "is_active", Type: TypeInt, Default: "1", LateAdd: true},
		},
		Idx: []Index{
			{Name: "idx_upstream_models_uid_name", Columns: []string{"upstream_id", "model_name"}, Unique: true, Where: "is_active = 1"},
		},
	},
	{
		Name: "ext_keys",
		Cols: []Column{
			{Name: "id", Type: TypeBigInt, AutoID: true},
			{Name: "key", Type: TypeText, Len: 191},
			{Name: "label", Type: TypeText, Len: 191, Default: "''"},
			// remark / allowed_models 故意不给 Len：两者都不建索引，MySQL 上保持
			// TEXT（不引入任意长度上限）。TEXT 的默认值在 MySQL 上写成表达式形式即可
			//（DEFAULT ('')，见 effectiveDefault），所以「无长度上限」与「有默认值」
			// 可以兼得，不需要把写入路径逼成必填。
			{Name: "remark", Type: TypeText, Default: "''", LateAdd: true},
			{Name: "enabled", Type: TypeInt, Default: "1", LateAdd: true},
			{Name: "daily_token_limit", Type: TypeInt, Default: "0", LateAdd: true},
			{Name: "monthly_token_limit", Type: TypeInt, Default: "0", LateAdd: true},
			// 按 key 的模型白名单：'' = 不限；否则 JSON 数组文本（对外模型名）
			{Name: "allowed_models", Type: TypeText, Default: "''", LateAdd: true},
			{Name: "created_at", Type: TypeTime, Default: "CURRENT_TIMESTAMP"},
			{Name: "last_used_at", Type: TypeTime, Nullable: true},
			{Name: "is_active", Type: TypeInt, Default: "1", LateAdd: true},
		},
		Idx: []Index{
			{Name: "idx_ext_keys_key", Columns: []string{"key"}, Unique: true, Where: "is_active = 1"},
			{Name: "idx_ext_keys_label", Columns: []string{"label"}, Unique: true, Where: "is_active = 1 AND label <> ''", Tolerant: true},
		},
	},
	{
		Name: "usage_records",
		Cols: []Column{
			{Name: "id", Type: TypeBigInt, AutoID: true},
			{Name: "ext_key_id", Type: TypeBigInt, Nullable: true},
			{Name: "upstream_id", Type: TypeBigInt, Nullable: true},
			{Name: "upstream_name", Type: TypeText, Len: 255},
			{Name: "model", Type: TypeText, Len: 512},
			{Name: "in_format", Type: TypeText, Len: 32},
			{Name: "up_format", Type: TypeText, Len: 32},
			{Name: "prompt_tokens", Type: TypeInt, Default: "0"},
			{Name: "completion_tokens", Type: TypeInt, Default: "0"},
			{Name: "total_tokens", Type: TypeInt, Default: "0"},
			{Name: "cache_read_tokens", Type: TypeInt, Default: "0", LateAdd: true},
			{Name: "cache_creation_tokens", Type: TypeInt, Default: "0", LateAdd: true},
			{Name: "reasoning_tokens", Type: TypeInt, Default: "0", LateAdd: true},
			{Name: "duration_ms", Type: TypeInt, Default: "0", LateAdd: true},
			{Name: "stream", Type: TypeInt, Default: "0"},
			{Name: "status", Type: TypeText, Len: 32, Default: "'ok'"},
			{Name: "created_at", Type: TypeTime, Default: "CURRENT_TIMESTAMP"},
		},
		Idx: []Index{
			{Name: "idx_usage_created", Columns: []string{"created_at"}},
			{Name: "idx_usage_ext_key", Columns: []string{"ext_key_id"}},
			{Name: "idx_usage_upstream", Columns: []string{"upstream_id"}},
		},
	},
	{
		Name: "response_sessions",
		Cols: []Column{
			// id 是 "resp_" + 32 位十六进制，37 字符。MySQL 上 TEXT 不能作主键，
			// 故给 64 的 Len（SQLite/PG 仍渲染成 TEXT）。
			{Name: "id", Type: TypeText, Len: 64, PrimaryKey: true},
			// messages 存的是累积的整段会话历史：PG/SQLite 的 TEXT 无上限，但
			// MySQL 的 TEXT 只有 64 KiB，长会话会写不进去。必须 LONGTEXT。
			{Name: "messages", Type: TypeLongText},
			{Name: "created_at", Type: TypeTime, Default: "CURRENT_TIMESTAMP"},
			{Name: "last_used_at", Type: TypeTime, Default: "CURRENT_TIMESTAMP"},
		},
		Idx: []Index{
			{Name: "idx_resp_sessions_used", Columns: []string{"last_used_at"}},
		},
	},
	{
		Name: "model_aliases",
		Cols: []Column{
			{Name: "id", Type: TypeBigInt, AutoID: true},
			{Name: "name", Type: TypeText, Len: 255},
			{Name: "created_at", Type: TypeTime, Default: "CURRENT_TIMESTAMP"},
			{Name: "updated_at", Type: TypeTime, Default: "CURRENT_TIMESTAMP"},
			{Name: "is_active", Type: TypeInt, Default: "1"},
		},
		Idx: []Index{
			{Name: "idx_model_aliases_name", Columns: []string{"name"}, Unique: true, Where: "is_active = 1"},
		},
	},
	{
		Name: "model_alias_bindings",
		Cols: []Column{
			{Name: "id", Type: TypeBigInt, AutoID: true},
			{Name: "alias_id", Type: TypeBigInt},
			{Name: "upstream_id", Type: TypeBigInt},
			{Name: "model_name", Type: TypeText, Len: 255},
			{Name: "priority", Type: TypeInt, Default: "0"},
			{Name: "is_active", Type: TypeInt, Default: "1"},
		},
		Idx: []Index{
			{Name: "idx_alias_bindings_order", Columns: []string{"alias_id", "priority"}, Unique: true, Where: "is_active = 1"},
		},
	},
	{
		Name: "balance_snapshots",
		Cols: []Column{
			{Name: "id", Type: TypeBigInt, AutoID: true},
			{Name: "upstream_id", Type: TypeBigInt},
			{Name: "upstream_name", Type: TypeText, Len: 255},
			{Name: "vendor", Type: TypeText, Len: 64},
			{Name: "payload", Type: TypeLongText},
			{Name: "created_at", Type: TypeTime},
		},
		Idx: []Index{
			{Name: "idx_balance_snapshots_upstream", Columns: []string{"upstream_id", "id DESC"}},
		},
	},
	{
		// 仅 MySQL：分表共享 id 的计数器，等价于 PG 的 conversation_records_id_seq。
		Name: "id_sequences",
		Only: DialectMySQL,
		Cols: []Column{
			{Name: "name", Type: TypeText, Len: 64, PrimaryKey: true},
			{Name: "next_id", Type: TypeBigInt, Default: "0"},
		},
	},
}

// conversationRecordsCols 是对话归档表（conversation_records 及其月分表）的列定义。
// 主迁移不建这张表：PG 由应用层按月分表（见 conversation_shard.go），存量库里的
// 旧 conversation_records 原地保留为历史分表。这里单独定义，让分表 DDL 也从同一份
// schema 渲染。
var conversationRecordsCols = []Column{
	{Name: "id", Type: TypeBigInt, PrimaryKey: true},
	{Name: "ext_key_id", Type: TypeBigInt, Nullable: true},
	{Name: "upstream_id", Type: TypeBigInt, Nullable: true},
	{Name: "upstream_name", Type: TypeText, Len: 255},
	{Name: "model", Type: TypeText, Len: 512},
	{Name: "in_format", Type: TypeText, Len: 32},
	{Name: "up_format", Type: TypeText, Len: 32},
	{Name: "harness", Type: TypeText, Len: 64},
	{Name: "user_agent", Type: TypeText},
	{Name: "stream", Type: TypeInt, Default: "0"},
	{Name: "status", Type: TypeText, Len: 32, Default: "'ok'"},
	{Name: "prompt_tokens", Type: TypeInt, Default: "0"},
	{Name: "completion_tokens", Type: TypeInt, Default: "0"},
	{Name: "total_tokens", Type: TypeInt, Default: "0"},
	{Name: "cache_read_tokens", Type: TypeInt, Default: "0"},
	{Name: "cache_creation_tokens", Type: TypeInt, Default: "0"},
	{Name: "reasoning_tokens", Type: TypeInt, Default: "0"},
	// MySQL 的 JSON 列要表达式默认值（DEFAULT ('{}')，见 effectiveDefault）；
	// 写入方（insertConversationInto）恒给值，这里是与其他方言对齐的兜底。
	{Name: "request_ir", Type: TypeJSON, Default: "'{}'"},
	{Name: "response_ir", Type: TypeJSON, Default: "'{}'"},
	{Name: "request_raw", Type: TypeBytes},
	{Name: "response_raw", Type: TypeBytes},
	{Name: "created_at", Type: TypeTime, Default: "CURRENT_TIMESTAMP"},
}

// convShardIndexes 返回一张月分表的索引定义。索引名带月份后缀（schema 级唯一），
// 故由调用方按表名生成而非复用固定定义。
func convShardIndexes(suffix string) []Index {
	return []Index{
		{Name: "idx_conv_" + suffix + "_created", Columns: []string{"created_at"}},
		{Name: "idx_conv_" + suffix + "_ext_key", Columns: []string{"ext_key_id"}},
		{Name: "idx_conv_" + suffix + "_harness", Columns: []string{"harness"}},
	}
}

// convColumnOrder 是归档插入的列顺序（即参数顺序）。单独列出来供
// model.insertConversationInto 拼装 INSERT，避免列清单与值列表漂移。
var convColumnOrder = []string{
	"ext_key_id", "upstream_id", "upstream_name", "model", "in_format", "up_format",
	"harness", "user_agent", "stream", "status",
	"prompt_tokens", "completion_tokens", "total_tokens",
	"cache_read_tokens", "cache_creation_tokens", "reasoning_tokens",
	"request_ir", "response_ir", "request_raw", "response_raw", "created_at",
}

// ConversationShardCols 返回归档插入的列顺序（exported wrapper）。
func ConversationShardCols() []string { return append([]string(nil), convColumnOrder...) }

// lookupColumn 按名字取列定义，未找到返回 false。
func lookupColumn(cols []Column, name string) (Column, bool) {
	for _, c := range cols {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

// ColumnByName 从归档表定义里取一列（供 model 判断哪些列要走 JSON 占位符）。
func ColumnByName(name string) (Column, bool) { return lookupColumn(conversationRecordsCols, name) }

// ---------------------------------------------------------------------------
// 渲染
// ---------------------------------------------------------------------------

// DDL 渲染建表 + 建索引语句。MySQL 的索引内联在 CREATE TABLE 里（MySQL 没有
// CREATE INDEX IF NOT EXISTS），故此时只返回一条语句。
func (t Table) DDL(d Dialect, cfg DDLConfig) ([]string, error) {
	create, err := t.CreateTableDDL(d, cfg)
	if err != nil {
		return nil, err
	}
	out := []string{create}
	idx, err := t.IndexDDL(d, cfg)
	if err != nil {
		return nil, err
	}
	return append(out, idx...), nil
}

// CreateTableDDL 渲染 CREATE TABLE。
func (t Table) CreateTableDDL(d Dialect, cfg DDLConfig) (string, error) {
	if t.Only != "" && t.Only != d {
		return "", fmt.Errorf("table %s is not created on %s", t.Name, d)
	}
	if d == DialectMySQL {
		return t.createTableMySQL(cfg)
	}
	var lines []string
	for _, c := range t.Cols {
		def, err := t.columnDef(d, c, cfg)
		if err != nil {
			return "", err
		}
		lines = append(lines, def)
	}
	notExists := ""
	if cfg.IfNotExists {
		notExists = "IF NOT EXISTS "
	}
	return fmt.Sprintf("CREATE TABLE %s%s (\n    %s\n)", notExists, t.Name, strings.Join(lines, ",\n    ")), nil
}

// emitsNotNull 决定是否显式写 NOT NULL。主键列多数情况下不需要：SQLite 的
// INTEGER PRIMARY KEY 与 PG 的 BIGSERIAL/TEXT PRIMARY KEY 都隐含非空，而历史 DDL
// 正是这么写的。例外是 MySQL 的自增主键（类型串自带 NOT NULL 才符合 MySQL 惯例）
// 和普通 BIGINT 主键（分表 id，类型串不保证非空，必须显式写）。
func emitsNotNull(d Dialect, c Column) bool {
	if c.Nullable {
		return false
	}
	if c.AutoID {
		return d == DialectMySQL
	}
	if c.PrimaryKey && c.Type == TypeText {
		return false
	}
	return true
}

// columnDef 渲染 SQLite / PG 的单列定义。
func (t Table) columnDef(d Dialect, c Column, cfg DDLConfig) (string, error) {
	typ, err := c.sqlType(d)
	if err != nil {
		return "", fmt.Errorf("%s.%s: %w", t.Name, c.Name, err)
	}
	var parts []string
	parts = append(parts, c.Name, typ)
	if emitsNotNull(d, c) {
		parts = append(parts, "NOT NULL")
	}
	if def := effectiveDefault(c, d, cfg); def != "" {
		parts = append(parts, "DEFAULT "+def)
	}
	if c.PrimaryKey {
		parts = append(parts, "PRIMARY KEY")
	} else if c.AutoID {
		parts = append(parts, pkSuffix(d))
	}
	return strings.Join(parts, " "), nil
}

// effectiveDefault 取该列在本方言下真正生效的默认值。MySQL 上 BLOB/TEXT/JSON
// 列拒绝「裸字面量」默认值（错误 1101），但接受把同一个默认值包成表达式的写法
// （8.0.13+）——所以这里不是丢掉默认值，而是给它包上括号，
// 让三种方言对同一列的默认语义保持一致（空串/空对象的含义不能因方言而变）。
//
// cfg.ColumnDefaults 里的表达式（如 PG 分表的 nextval）本身就是函数调用，
// 不能再包一层，原样返回。
func effectiveDefault(c Column, d Dialect, cfg DDLConfig) string {
	if v, ok := cfg.ColumnDefaults[c.Name]; ok {
		return v
	}
	if c.Default == "" {
		return ""
	}
	if d == DialectMySQL && c.needsParenDefaultMySQL() {
		return "(" + c.Default + ")"
	}
	return c.Default
}

// needsParenDefaultMySQL 报告该列在 MySQL 上是否必须把默认值写成表达式形式。
// MySQL 错误 1101 覆盖的四类列：BLOB、TEXT（含 LONGTEXT）、GEOMETRY、JSON。
// 给了 Len 的 TypeText 在 MySQL 上是 VARCHAR，裸字面量即可。
func (c Column) needsParenDefaultMySQL() bool {
	switch c.Type {
	case TypeText:
		return c.Len == 0 // 有长度 → VARCHAR；无长度 → TEXT，必须包括号
	case TypeLongText, TypeJSON, TypeBytes:
		return true
	}
	return false
}

// pkSuffix 是自增主键在 SQLite / PG 上的内联主键子句。
func pkSuffix(d Dialect) string {
	if d == DialectPostgres {
		return "PRIMARY KEY"
	}
	return "PRIMARY KEY AUTOINCREMENT"
}

// sqlType 渲染单列类型。
func (c Column) sqlType(d Dialect) (string, error) {
	switch c.Type {
	case TypeInt:
		if d == DialectMySQL {
			return "INT", nil
		}
		return "INTEGER", nil
	case TypeBigInt:
		if c.AutoID {
			if d == DialectMySQL {
				return "BIGINT", nil
			}
			if d == DialectPostgres {
				return "BIGSERIAL", nil
			}
			return "INTEGER", nil
		}
		// PG 的 INTEGER 是 int4、BIGINT 是 int8；存量库里 ext_key_id 这类引用列
		// 用的就是 BIGINT，保持原样。
		return "BIGINT", nil
	case TypeTime:
		switch d {
		case DialectPostgres:
			return "TIMESTAMP(0)", nil
		case DialectMySQL:
			// 不用 TIMESTAMP：MySQL 的 TIMESTAMP 只到 2038 年，且带时区隐式转换。
			return "DATETIME(0)", nil
		default:
			return "DATETIME", nil
		}
	case TypeText:
		// Len 只对 MySQL 生效：PG/SQLite 仍是 TEXT，与存量库保持一致。
		if d == DialectMySQL && c.Len > 0 {
			return fmt.Sprintf("VARCHAR(%d)", c.Len), nil
		}
		return "TEXT", nil
	case TypeLongText:
		if d == DialectMySQL {
			return "LONGTEXT", nil
		}
		return "TEXT", nil
	case TypeJSON:
		switch d {
		case DialectPostgres:
			return "JSONB", nil
		case DialectMySQL:
			return "JSON", nil
		default:
			return "TEXT", nil
		}
	case TypeBytes:
		switch d {
		case DialectPostgres:
			return "BYTEA", nil
		case DialectMySQL:
			return "LONGBLOB", nil
		default:
			return "BLOB", nil
		}
	}
	return "", fmt.Errorf("unsupported column type %d", c.Type)
}

// IndexDDL 渲染独立的建索引语句。SQLite/PG 支持 CREATE ... IF NOT EXISTS，故
// 部分唯一索引与普通索引都在这里；MySQL 的索引已内联在 CREATE TABLE，返回空。
func (t Table) IndexDDL(d Dialect, cfg DDLConfig) ([]string, error) {
	if d == DialectMySQL || len(t.Idx) == 0 {
		return nil, nil
	}
	var out []string
	for _, ix := range t.Idx {
		def, err := t.indexDef(d, ix, cfg)
		if err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, nil
}

// indexDef 渲染 SQLite / PG 的一条索引语句。
func (t Table) indexDef(d Dialect, ix Index, cfg DDLConfig) (string, error) {
	if err := ix.validate(t); err != nil {
		return "", err
	}
	cols := make([]string, len(ix.Columns))
	for i, c := range ix.Columns {
		cols[i] = c
	}
	unique := ""
	if ix.Unique {
		unique = "UNIQUE "
	}
	notExists := ""
	if cfg.IfNotExists {
		notExists = "IF NOT EXISTS "
	}
	where := ""
	if ix.Where != "" {
		where = " WHERE " + ix.Where
	}
	name := resolveIndexName(ix.Name, cfg.IndexSuffix)
	return fmt.Sprintf("CREATE %sINDEX %s%s ON %s(%s)%s",
		unique, notExists, name, t.Name, strings.Join(cols, ", "), where), nil
}

// validate 校验索引引用的列都存在。
func (ix Index) validate(t Table) error {
	for _, c := range ix.Columns {
		// "id DESC" 这类带排序方向的写法只取列名部分。
		name := strings.Fields(c)[0]
		if _, ok := lookupColumn(t.Cols, name); !ok {
			return fmt.Errorf("index %s references unknown column %s.%s", ix.Name, t.Name, name)
		}
	}
	return nil
}

// AddColumnDDL 渲染 ALTER TABLE ADD COLUMN，供 migrateExtraCols 使用。
func (t Table) AddColumnDDL(d Dialect, c Column, cfg DDLConfig) (string, error) {
	def, err := t.columnDef(d, c, cfg)
	if err != nil {
		return "", fmt.Errorf("%s.%s: %w", t.Name, c.Name, err)
	}
	return fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", t.Name, def), nil
}

// ColumnNames 返回列名清单，顺序即定义顺序。SQLite 表重建的 INSERT/SELECT 两侧
// 共用这一份，避免两侧清单漂移。
func (t Table) ColumnNames() []string {
	out := make([]string, len(t.Cols))
	for i, c := range t.Cols {
		out[i] = c.Name
	}
	return out
}

// ---------------------------------------------------------------------------
// MySQL 渲染
// ---------------------------------------------------------------------------

// createTableMySQL 渲染 MySQL 的 CREATE TABLE：索引内联、标识符一律反引号
// （key 是 MySQL 保留字）、部分唯一索引降级为生成列。
func (t Table) createTableMySQL(cfg DDLConfig) (string, error) {
	if err := t.validateMySQL(); err != nil {
		return "", err
	}
	var lines []string
	for _, c := range t.Cols {
		def, err := t.columnDefMySQL(c, cfg)
		if err != nil {
			return "", err
		}
		lines = append(lines, def)
	}
	// 部分唯一索引的生成列跟在真实列后面。
	gen, err := t.partialUniqueColumns(cfg)
	if err != nil {
		return "", err
	}
	lines = append(lines, gen...)

	// 主键已内联在列定义上（columnDefMySQL），这里只加索引，避免重复声明主键。
	var keys []string
	for _, ix := range t.Idx {
		def, err := t.indexDefMySQL(ix, cfg)
		if err != nil {
			return "", err
		}
		keys = append(keys, def)
	}
	lines = append(lines, keys...)

	notExists := ""
	if cfg.IfNotExists {
		notExists = "IF NOT EXISTS "
	}
	// utf8mb4_bin：MySQL 默认的 utf8mb4_0900_ai_ci 是大小写/重音不敏感的，会让
	// name/key 的唯一性与等值比较语义与 PG/SQLite 的字节精确比较不一致。
	return fmt.Sprintf("CREATE TABLE %s`%s` (\n  %s\n) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin",
		notExists, t.Name, strings.Join(lines, ",\n  ")), nil
}

// columnDefMySQL 渲染 MySQL 的单列定义，按 MySQL 文档的属性顺序：
// data_type [NOT NULL] [DEFAULT ...] [AUTO_INCREMENT] [PRIMARY KEY]。
func (t Table) columnDefMySQL(c Column, cfg DDLConfig) (string, error) {
	typ, err := c.sqlType(DialectMySQL)
	if err != nil {
		return "", fmt.Errorf("%s.%s: %w", t.Name, c.Name, err)
	}
	var parts []string
	parts = append(parts, "`"+c.Name+"`", typ)
	if emitsNotNull(DialectMySQL, c) {
		parts = append(parts, "NOT NULL")
	}
	if def := effectiveDefault(c, DialectMySQL, cfg); def != "" {
		parts = append(parts, "DEFAULT "+def)
	}
	if c.AutoID {
		parts = append(parts, "AUTO_INCREMENT")
	}
	if c.AutoID || c.PrimaryKey {
		parts = append(parts, "PRIMARY KEY")
	}
	return strings.Join(parts, " "), nil
}

// indexDefMySQL 渲染 MySQL 的一条内联索引。
func (t Table) indexDefMySQL(ix Index, cfg DDLConfig) (string, error) {
	if err := ix.validate(t); err != nil {
		return "", err
	}
	name := resolveIndexName(ix.Name, cfg.IndexSuffix)
	kind := "  KEY"
	if ix.Unique {
		kind = "  UNIQUE KEY"
	}
	// 生成列名由「已套用后缀的索引名」派生，所以要把 name 传下去 —— 两侧用同一个
	// 名字，否则分表上一旦出现部分唯一索引，KEY 会引用一个没被定义的生成列。
	return fmt.Sprintf("%s `%s` (%s)", kind, name, t.indexColumnsMySQL(ix, name)), nil
}

// indexColumnsMySQL 渲染 MySQL 的索引列清单：部分唯一索引的最后一列换成生成列。
// name 必须是已套用 IndexSuffix 的索引名（见 indexDefMySQL）。
func (t Table) indexColumnsMySQL(ix Index, name string) string {
	cols := append([]string(nil), ix.Columns...)
	if ix.Unique && ix.Where != "" {
		cols[len(cols)-1] = generatedColName(name)
	}
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = "`" + strings.Fields(c)[0] + "`"
	}
	return strings.Join(quoted, ", ")
}

// partialUniqueColumns 渲染部分唯一索引所需的生成列。
func (t Table) partialUniqueColumns(cfg DDLConfig) ([]string, error) {
	var out []string
	for _, ix := range t.Idx {
		if !ix.Unique || ix.Where == "" {
			continue
		}
		last := ix.Columns[len(ix.Columns)-1]
		col, ok := lookupColumn(t.Cols, strings.Fields(last)[0])
		if !ok {
			return nil, fmt.Errorf("index %s references unknown column %s", ix.Name, last)
		}
		typ, err := col.sqlType(DialectMySQL)
		if err != nil {
			return nil, err
		}
		// 谓词不成立 → NULL → 不参与唯一性比较（软删除行不占名额）。
		// 谓词整段塞进 IF() 即可覆盖 "is_active = 1 AND label <> ''" 这类复合谓词。
		name := resolveIndexName(ix.Name, cfg.IndexSuffix)
		out = append(out, fmt.Sprintf("  `%s` %s GENERATED ALWAYS AS (IF(%s, `%s`, NULL)) STORED",
			generatedColName(name), typ, ix.Where, strings.Fields(last)[0]))
	}
	return out, nil
}

// resolveIndexName 把索引名里的 "{}" 换成 IndexSuffix（按月分表用）。三处渲染
// （SQLite/PG 索引语句、MySQL 内联 KEY、生成列名）必须走同一个函数，否则后缀会
// 只应用在其中一两处。
func resolveIndexName(name, suffix string) string {
	if suffix == "" {
		return name
	}
	return strings.ReplaceAll(name, "{}", suffix)
}

// generatedColName 是部分唯一索引降级用的生成列名。MySQL 标识符上限 64 字符，
// 现有索引名都很短。
func generatedColName(indexName string) string { return "g_" + indexName }

// validateMySQL 把「MySQL 上这列能不能保持 TEXT」的记账变成机器检查。不给 Len 的
// TypeText 列一旦要建索引或作主键，MySQL 根本建不出来，直接报错并说清怎么改 ——
// 而不是等建表时才吐一句没头没尾的语法错误。
//
// 带默认值不算错：那种列在 MySQL 上渲染成 TEXT 并丢掉默认值（见 effectiveDefault），
// 写入方恒给值，缺值会响亮失败。
func (t Table) validateMySQL() error {
	for _, c := range t.Cols {
		if c.Type != TypeText || c.Len > 0 {
			continue
		}
		var reasons []string
		if c.PrimaryKey {
			reasons = append(reasons, "是主键（MySQL 的 TEXT 不能作主键）")
		}
		for _, ix := range t.Idx {
			for _, ic := range ix.Columns {
				if strings.Fields(ic)[0] == c.Name {
					reasons = append(reasons, "被索引 "+ix.Name+" 引用（MySQL 的 TEXT 不能直接建索引）")
				}
			}
		}
		if len(reasons) > 0 {
			return fmt.Errorf("%s.%s: MySQL 上 TEXT 列%s，必须给 Len（渲染成 VARCHAR）",
				t.Name, c.Name, strings.Join(reasons, "、"))
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 对外入口
// ---------------------------------------------------------------------------

// tablesFor 返回该方言要建的表（过滤 Only）。
func tablesFor(d Dialect) []Table {
	var out []Table
	for _, t := range schemaTables {
		if t.Only != "" && t.Only != d {
			continue
		}
		out = append(out, t)
	}
	return out
}

// schemaTableByName 按名字取主迁移里的表定义。
func schemaTableByName(name string) (Table, bool) {
	for _, t := range schemaTables {
		if t.Name == name {
			return t, true
		}
	}
	return Table{}, false
}

// migrateMain 执行主迁移：建所有表（含该方言内联的索引）。逐条执行而非整段脚本：
// go-sql-driver 不支持一次 Exec 多条语句，而逐条执行对 PG/SQLite 同样幂等。
func migrateMain(d *sql.DB) error {
	stmts, err := createTableStmts(DialectOf(d))
	if err != nil {
		return err
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return fmt.Errorf("migrate main: %q: %w", s, err)
		}
	}
	return nil
}

// createTableStmts 渲染主迁移的建表语句。**只含 CREATE TABLE，不含索引**：
// 部分唯一索引的谓词引用 is_active，而该列要靠 migrateExtraCols /
// migrateSoftDelete 才在老库上就位，所以索引必须晚一步建（见
// migratePartialUniqueIndexes / migratePlainIndexes）。MySQL 的索引本来就内联在
// CREATE TABLE 里，对它是空操作。
func createTableStmts(d Dialect) ([]string, error) {
	cfg := DDLConfig{IfNotExists: true}
	var out []string
	for _, t := range tablesFor(d) {
		stmt, err := t.CreateTableDDL(d, cfg)
		if err != nil {
			return nil, err
		}
		out = append(out, stmt)
	}
	return out, nil
}

// ConversationShardDDL 渲染一张对话归档月分表的 DDL。MySQL 的索引内联；PG 另需
// CREATE SEQUENCE（由 ShardSequenceStatements 提供）。name 必须已过
// model.convShardNameRe 白名单。
//
// SQLite 拒绝渲染：它走 conversation_records 单表路径，没有分表机制。生成一份
// 永远不会被执行的 DDL 只会让人误以为 SQLite 也支持分表。
func ConversationShardDDL(d Dialect, tableName string, seqName string) ([]string, error) {
	if d == DialectSQLite {
		return nil, fmt.Errorf("conversation sharding is not supported on sqlite")
	}
	suffix := strings.TrimPrefix(tableName, "conversation_records_")
	t := Table{Name: tableName, Cols: conversationRecordsCols, Idx: convShardIndexes(suffix)}
	cfg := DDLConfig{IfNotExists: true}
	if d == DialectPostgres {
		// id 默认值指向共享序列（存量库沿用旧表 BIGSERIAL 自带序列）。
		cfg.ColumnDefaults = map[string]string{
			"id": fmt.Sprintf("nextval('%s')", seqName),
		}
	}
	return t.DDL(d, cfg)
}

// ShardSequenceStatements 返回建共享 id 序列的语句。PG 用 CREATE SEQUENCE；
// MySQL 用 id_sequences 计数器表（主迁移已建，无需额外语句）。
func ShardSequenceStatements(d Dialect, seqName string) []string {
	if d != DialectPostgres {
		return nil
	}
	return []string{fmt.Sprintf("CREATE SEQUENCE IF NOT EXISTS %s", seqName)}
}
