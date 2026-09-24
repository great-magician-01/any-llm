package db

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// mysqlTestDB 连接 DB_TEST_MYSQL_DSN，在独立 database 里跑迁移，测试结束 drop
// database。未配置 DSN 时跳过。与 pgTestDB 的 schema 隔离对应：MySQL 的
// database 就是 schema，所以按库隔离。
//
// 需要建库/删库权限；清理时先关闭业务连接再 drop（t.Cleanup 后进先出），否则
// mysqld 还占着连接。控制连接必须活到清理那一刻：若在 helper 里 defer 关掉它，
// DROP 会永远打在已关闭的连接上（错误被丢弃，测试库就此泄漏）。
func mysqlTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set DB_TEST_MYSQL_DSN to run mysql e2e tests")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	dbName := fmt.Sprintf("any_llm_test_%d", time.Now().UnixNano())
	// 控制连接：连到 DSN 里原有的库，负责建/删测试库。
	ctrlCfg := *cfg
	ctrlCfg.DBName = cfg.DBName
	ctrl, err := sql.Open("mysql", ctrlCfg.FormatDSN())
	if err != nil {
		t.Fatalf("open control conn: %v", err)
	}
	if err := ctrl.Ping(); err != nil {
		ctrl.Close()
		t.Fatalf("ping control conn: %v", err)
	}
	// 建库时把字符集/排序规则钉死成与表级一致，避免服务器默认值不同导致行为漂移。
	if _, err := ctrl.Exec(fmt.Sprintf(
		"CREATE DATABASE %s DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin", dbName)); err != nil {
		ctrl.Close()
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		// d 已关闭（下方 Cleanup 后注册、先执行），这里用控制连接删库。
		if _, err := ctrl.Exec(fmt.Sprintf("DROP DATABASE %s", dbName)); err != nil {
			t.Errorf("drop database %s: %v", dbName, err)
		}
		ctrl.Close()
	})

	cfg.DBName = dbName
	d, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	if err := d.Ping(); err != nil {
		d.Close()
		t.Fatalf("ping: %v", err)
	}
	if err := MigrateForTest(d); err != nil {
		d.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestMySQL_E2E_DialectAndHelpers(t *testing.T) {
	d := mysqlTestDB(t)

	if got := DialectOf(d); got != DialectMySQL {
		t.Fatalf("DialectOf=%q want mysql", got)
	}
	// MySQL 用 ? 占位，Rebind 应原样返回。
	if got := Rebind(d, "SELECT ?, ?, '?'"); got != "SELECT ?, ?, '?'" {
		t.Fatalf("rebind=%q", got)
	}
	if got := JSONPlaceholder(d); got != "?" {
		t.Fatalf("JSONPlaceholder=%q", got)
	}
	if got := QuoteIdent(d, "key"); got != "`key`" {
		t.Fatalf("QuoteIdent(key)=%q", got)
	}
	if got := DayBucketExpr(d, "created_at"); got != "DATE_FORMAT(created_at, '%Y-%m-%d')" {
		t.Fatalf("DayBucketExpr=%q", got)
	}
	// 全表建齐（含仅 MySQL 的 id_sequences）
	for _, table := range []string{
		"upstreams", "upstream_models", "ext_keys", "usage_records",
		"response_sessions", "model_aliases", "model_alias_bindings",
		"balance_snapshots", "id_sequences",
	} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s missing: n=%d err=%v", table, n, err)
		}
	}
	// conversation_records 不由主迁移建（按月分表）
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name = 'conversation_records'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("conversation_records should not be pre-created: n=%d err=%v", n, err)
	}
}

// TestMySQL_E2E_ColumnTypes 抽样核对渲染出来的列类型：MySQL 特有的 VARCHAR /
// LONGTEXT / JSON / LONGBLOB / DATETIME(0) 必须各就各位。
func TestMySQL_E2E_ColumnTypes(t *testing.T) {
	d := mysqlTestDB(t)

	want := map[string]string{
		"ext_keys.key":               "varchar",
		"ext_keys.remark":            "text",
		"ext_keys.allowed_models":    "text",
		"response_sessions.id":       "varchar",
		"response_sessions.messages": "longtext",
		"usage_records.status":       "varchar",
		"upstreams.created_at":       "datetime",
		"upstreams.expires_at":       "datetime",
		// remark 与 ext_keys 的同名列一致：不建索引 → 保持 TEXT（不是 VARCHAR），
		// 默认值走表达式形式。谁给它补上 Len，这里就会挂。
		"upstreams.remark": "text",
	}
	for tc, dt := range want {
		var got string
		err := d.QueryRow(`SELECT DATA_TYPE FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`,
			tableOf(tc), colOf(tc)).Scan(&got)
		if err != nil {
			t.Fatalf("%s: %v", tc, err)
		}
		if got != dt {
			t.Errorf("%s data_type=%q want %q", tc, got, dt)
		}
	}
	// id_sequences 只有 MySQL 有
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = 'id_sequences' AND column_name = 'next_id'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("id_sequences.next_id missing: n=%d err=%v", n, err)
	}
}

// TestMySQL_E2E_PartialUniqueIndexes 是本次 MySQL 适配最核心的语义断言：
// 「仅活跃行」唯一性必须真的生效 —— 软删除后同名可重建，活跃同名被拒。
func TestMySQL_E2E_PartialUniqueIndexes(t *testing.T) {
	d := mysqlTestDB(t)

	ins := func(q string, args ...any) error {
		_, err := d.Exec(Rebind(d, q), args...)
		return err
	}

	// upstreams.name：活跃同名被拒
	if err := ins(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES (?,?,?,?)`,
		"u1", "http://x", "k", "openai"); err != nil {
		t.Fatalf("insert u1: %v", err)
	}
	err := ins(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES (?,?,?,?)`,
		"u1", "http://y", "k2", "openai")
	if err == nil {
		t.Fatal("duplicate active upstreams.name should be rejected")
	}
	// 软删除后同名可重建
	if err := ins(`UPDATE upstreams SET is_active = 0 WHERE name=?`, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := ins(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES (?,?,?,?)`,
		"u1", "http://y", "k2", "openai"); err != nil {
		t.Fatalf("recreate after soft delete: %v", err)
	}
	// 两条死行同名也不冲突
	if err := ins(`UPDATE upstreams SET is_active = 0 WHERE name=?`, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := ins(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES (?,?,?,?)`,
		"u1", "http://z", "k3", "openai"); err != nil {
		t.Fatalf("second soft-deleted row should not conflict: %v", err)
	}

	// upstream_models(upstream_id, model_name)：复合部分唯一索引
	if err := ins(`INSERT INTO upstream_models (upstream_id, model_name) VALUES (?,?)`, 1, "m1"); err != nil {
		t.Fatal(err)
	}
	if err := ins(`INSERT INTO upstream_models (upstream_id, model_name) VALUES (?,?)`, 1, "m1"); err == nil {
		t.Fatal("duplicate active (upstream_id, model_name) should be rejected")
	}
	if err := ins(`UPDATE upstream_models SET is_active = 0 WHERE upstream_id=? AND model_name=?`, 1, "m1"); err != nil {
		t.Fatal(err)
	}
	if err := ins(`INSERT INTO upstream_models (upstream_id, model_name) VALUES (?,?)`, 1, "m1"); err != nil {
		t.Fatalf("revive-after-soft-delete should succeed: %v", err)
	}
	// 不同 upstream_id 的同名模型不冲突（前导列必须是真实列，不能一并降级）
	if err := ins(`INSERT INTO upstream_models (upstream_id, model_name) VALUES (?,?)`, 2, "m1"); err != nil {
		t.Fatalf("same model on a different upstream should be allowed: %v", err)
	}

	// ext_keys.key / ext_keys.label
	if err := ins(`INSERT INTO ext_keys (`+QuoteIdent(d, "key")+`, label) VALUES (?,?)`, "all-sk-1", "lbl"); err != nil {
		t.Fatal(err)
	}
	if err := ins(`INSERT INTO ext_keys (`+QuoteIdent(d, "key")+`, label) VALUES (?,?)`, "all-sk-1", "other"); err == nil {
		t.Fatal("duplicate active ext key should be rejected")
	}
	if err := ins(`INSERT INTO ext_keys (`+QuoteIdent(d, "key")+`, label) VALUES (?,?)`, "all-sk-2", "lbl"); err == nil {
		t.Fatal("duplicate active label should be rejected")
	}
	// 两个空 label 共存（空名不参与唯一性）
	if err := ins(`INSERT INTO ext_keys (`+QuoteIdent(d, "key")+`, label) VALUES (?,?)`, "all-sk-3", ""); err != nil {
		t.Fatal(err)
	}
	if err := ins(`INSERT INTO ext_keys (`+QuoteIdent(d, "key")+`, label) VALUES (?,?)`, "all-sk-4", ""); err != nil {
		t.Fatalf("two empty labels should coexist: %v", err)
	}

	// model_aliases.name
	if err := ins(`INSERT INTO model_aliases (name) VALUES (?)`, "alias-1"); err != nil {
		t.Fatal(err)
	}
	if err := ins(`INSERT INTO model_aliases (name) VALUES (?)`, "alias-1"); err == nil {
		t.Fatal("duplicate active alias name should be rejected")
	}

	// model_alias_bindings(alias_id, priority)
	if err := ins(`INSERT INTO model_alias_bindings (alias_id, upstream_id, model_name, priority) VALUES (?,?,?,?)`, 1, 1, "m", 0); err != nil {
		t.Fatal(err)
	}
	if err := ins(`INSERT INTO model_alias_bindings (alias_id, upstream_id, model_name, priority) VALUES (?,?,?,?)`, 1, 2, "m", 0); err == nil {
		t.Fatal("duplicate (alias_id, priority) should be rejected")
	}
}

// TestMySQL_E2E_CRUDAndTimestamps 走真实模型层：RETURNING 的 LastInsertId 替代、
// ON DUPLICATE KEY UPDATE、可空时间列的 nil 绑定、时间戳本地墙钟往返。
func TestMySQL_E2E_CRUDAndTimestamps(t *testing.T) {
	d := mysqlTestDB(t)

	uid, err := createUpstreamE2E(d, "up-1", "https://api.openai.com", "sk-abc", "openai")
	if err != nil {
		t.Fatalf("create upstream: %v", err)
	}
	if uid == 0 {
		t.Fatal("LastInsertId path returned id 0")
	}
	uid2, err := createUpstreamE2E(d, "up-2", "https://api.anthropic.com", "sk-xyz", "anthropic")
	if err != nil {
		t.Fatalf("create upstream 2: %v", err)
	}
	if uid == uid2 {
		t.Fatalf("duplicate ids %d", uid)
	}

	// 时间戳往返：必须是 time.Time、本地时区
	got, err := getUpstreamByNameE2E(d, "up-1")
	if err != nil {
		t.Fatalf("get upstream: %v", err)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps zero: %+v", got)
	}
	if got.CreatedAt.Location() != time.Local {
		t.Errorf("created_at location=%v want Local (parseTime/loc=Local)", got.CreatedAt.Location())
	}
	// 可空时间列 nil 绑定：不设置 expires_at 时应读回 NULL
	var expiresAt sql.NullTime
	if err := d.QueryRow(Rebind(d, `SELECT expires_at FROM upstreams WHERE id=?`), uid).Scan(&expiresAt); err != nil {
		t.Fatal(err)
	}
	if expiresAt.Valid {
		t.Errorf("expires_at should be NULL, got %v", expiresAt.Time)
	}
	// 写一个到期时刻再读回，确认可空列的正反两个方向都对。输入取整到秒：
	// DATETIME(0) 只有秒精度，且 MySQL 对亚秒值是四舍五入而非截断
	// （09:29:10.600 → 09:29:11），把亚秒值混进来只会让断言去测舍入规则，
	// 而不是测「能否往返」。
	soon := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	if _, err := d.Exec(Rebind(d, `UPDATE upstreams SET expires_at=? WHERE id=?`), soon, uid); err != nil {
		t.Fatalf("write expires_at: %v", err)
	}
	if err := d.QueryRow(Rebind(d, `SELECT expires_at FROM upstreams WHERE id=?`), uid).Scan(&expiresAt); err != nil {
		t.Fatal(err)
	}
	if !expiresAt.Valid || !expiresAt.Time.Equal(soon) {
		t.Errorf("expires_at round-trip: got %v want %v", expiresAt.Time, soon)
	}

	// 模型：AddModel 幂等 + 软删除后复活同一行 id
	if err := addModelE2E(d, uid, "gpt-4o", false); err != nil {
		t.Fatal(err)
	}
	if err := addModelE2E(d, uid, "gpt-4o", false); err != nil {
		t.Fatalf("dup add model: %v", err)
	}
	var firstID int64
	if err := d.QueryRow(Rebind(d, `SELECT id FROM upstream_models WHERE upstream_id=? AND model_name=? AND is_active = 1`), uid, "gpt-4o").Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if err := deleteModelE2E(d, firstID); err != nil {
		t.Fatal(err)
	}
	if err := addModelE2E(d, uid, "gpt-4o", false); err != nil {
		t.Fatalf("revive: %v", err)
	}
	var revivedID int64
	if err := d.QueryRow(Rebind(d, `SELECT id FROM upstream_models WHERE upstream_id=? AND model_name=? AND is_active = 1`), uid, "gpt-4o").Scan(&revivedID); err != nil {
		t.Fatal(err)
	}
	if revivedID != firstID {
		t.Errorf("revive should reuse the row id: got %d want %d", revivedID, firstID)
	}

	// 密钥：key 是保留字，走真实查询路径
	k, err := createExtKeyE2E(d, "key-one")
	if err != nil {
		t.Fatalf("create ext key: %v", err)
	}
	if k.ID == 0 {
		t.Fatal("ext key id 0")
	}
	gotKey, err := getExtKeyE2E(d, k.Key)
	if err != nil {
		t.Fatalf("get ext key: %v", err)
	}
	if gotKey.ID != k.ID || gotKey.Label != "key-one" {
		t.Fatalf("ext key=%+v", gotKey)
	}
	if err := touchExtKeyE2E(d, k.ID); err != nil {
		t.Fatal(err)
	}
	gotKey, _ = getExtKeyE2E(d, k.Key)
	if !gotKey.LastUsed.Valid {
		t.Error("last_used_at not set")
	}

	// balance_snapshots 也走 LastInsertId 路径
	if err := insertBalanceSnapshotE2E(d, uid, "up-1", "openai", `{"balance":1}`); err != nil {
		t.Fatalf("insert balance snapshot: %v", err)
	}
	snaps, err := listBalanceSnapshotsE2E(d, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].ID == 0 {
		t.Fatalf("balance snapshots=%+v", snaps)
	}
}

// TestMySQL_E2E_UpsertAndUsage 覆盖 response_sessions 的 upsert（MySQL 的
// ON DUPLICATE KEY UPDATE + VALUES()）与用量汇总（ONLY_FULL_GROUP_BY + DATE_FORMAT）。
func TestMySQL_E2E_UpsertAndUsage(t *testing.T) {
	d := mysqlTestDB(t)

	// upsert：同 id 覆盖而不是报重复键。VALUES(col) 是 SQL 函数引用、不占参数位，
	// 所以与 PG/SQLite 一样只传 4 个。
	upsert := func(msg string) error {
		_, err := d.Exec(Rebind(d, `INSERT INTO response_sessions (id, messages, created_at, last_used_at)
			VALUES (?, ?, ?, ?)`+UpsertSuffix(d, "id", "messages", "last_used_at")),
			"resp_abc", msg, time.Now(), time.Now())
		return err
	}
	if err := upsert("first"); err != nil {
		t.Fatalf("session put: %v", err)
	}
	if err := upsert("second"); err != nil {
		t.Fatalf("session upsert: %v", err)
	}
	var msg string
	if err := d.QueryRow(Rebind(d, `SELECT messages FROM response_sessions WHERE id=?`), "resp_abc").Scan(&msg); err != nil {
		t.Fatal(err)
	}
	if msg != "second" {
		t.Errorf("messages=%q want second (upsert did not overwrite)", msg)
	}

	// 用量汇总：key 维度带 LEFT JOIN + GROUP BY，MySQL 默认开 ONLY_FULL_GROUP_BY
	uid, _ := createUpstreamE2E(d, "up", "https://x", "k", "openai")
	k, _ := createExtKeyE2E(d, "key-one")
	uidPtr := uid
	kID := k.ID
	for i, r := range []usageRecord{
		{extKeyID: &kID, upstreamID: &uidPtr, upstreamName: "up", model: "gpt-4o", inFormat: "openai", upFormat: "openai", prompt: 10, completion: 5, total: 15, status: "ok"},
		{extKeyID: &kID, upstreamID: &uidPtr, upstreamName: "up", model: "gpt-4o", inFormat: "openai", upFormat: "openai", prompt: 20, completion: 10, total: 30, stream: true, status: "ok"},
		{extKeyID: &kID, upstreamID: &uidPtr, upstreamName: "up", model: "gpt-4o-mini", inFormat: "anthropic", upFormat: "anthropic", prompt: 5, completion: 5, total: 10, status: "error"},
	} {
		r := r
		if i == 1 {
			r.stream = true
		}
		if err := insertUsageE2E(d, r); err != nil {
			t.Fatalf("insert usage: %v", err)
		}
	}
	byKey, err := usageSummaryE2E(d, "key", "", "")
	if err != nil {
		t.Fatalf("summary by key: %v", err)
	}
	if len(byKey) != 1 || byKey[0].groupKey != "key-one" {
		t.Fatalf("summary by key=%+v", byKey)
	}
	if byKey[0].requestCount != 3 || byKey[0].totalTokens != 55 {
		t.Fatalf("summary by key aggregates=%+v", byKey[0])
	}
	byModel, err := usageSummaryE2E(d, "model", "", "")
	if err != nil {
		t.Fatalf("summary by model: %v", err)
	}
	if len(byModel) != 2 {
		t.Fatalf("summary by model len=%d", len(byModel))
	}

	// 日桶：DATE_FORMAT 必须按本地日切
	var bucket string
	if err := d.QueryRow(`SELECT ` + DayBucketExpr(d, "created_at") + ` FROM usage_records LIMIT 1`).Scan(&bucket); err != nil {
		t.Fatalf("day bucket: %v", err)
	}
	if want := time.Now().Format("2006-01-02"); bucket != want {
		t.Errorf("day bucket=%q want %q", bucket, want)
	}
}

// TestMySQL_E2E_WriterPath 走真实 db.Writer（生产所有写入都过它）。
func TestMySQL_E2E_WriterPath(t *testing.T) {
	d := mysqlTestDB(t)
	w := NewWriter(d, 512)
	w.Start()
	t.Cleanup(w.Stop)

	var id int64
	if err := w.DoSync(func(tx *sql.DB) error {
		var err error
		id, err = InsertReturningID(tx, `INSERT INTO upstreams (name, base_url, api_key, format) VALUES (?,?,?,?) RETURNING id`,
			"w1", "http://x", "k", "openai")
		return err
	}); err != nil {
		t.Fatalf("sync write: %v", err)
	}
	if id == 0 {
		t.Fatal("sync write id 0")
	}
	w.DoAsync(func(tx *sql.DB) error {
		_, err := tx.Exec(Rebind(tx, `INSERT INTO upstream_models (upstream_id, model_name) VALUES (?,?)`), id, "m")
		return err
	})
	if err := w.DoSync(func(tx *sql.DB) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(Rebind(d, `SELECT COUNT(*) FROM upstream_models WHERE upstream_id=?`), id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("async write landed %d rows want 1", n)
	}
}

// TestMySQL_E2E_ConversationSharding 覆盖对话归档分表在 MySQL 上的落地：
// 计数器表分配 id、分表建表、跨分表翻页与详情查询。
func TestMySQL_E2E_ConversationSharding(t *testing.T) {
	d := mysqlTestDB(t)

	// 计数器表已由主迁移建好
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name = 'id_sequences'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("id_sequences missing: n=%d err=%v", n, err)
	}

	// NextShardID 单调且唯一
	id1, err := NextShardID(d, "conversation_records_id_seq")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := NextShardID(d, "conversation_records_id_seq")
	if err != nil {
		t.Fatal(err)
	}
	if id1 == 0 || id2 <= id1 {
		t.Fatalf("shard ids not increasing: %d, %d", id1, id2)
	}

	// 建当月分表并写入两行，id 应来自计数器表且不重复
	if err := ensureConvShardForTest(d, time.Now()); err != nil {
		t.Fatalf("ensure shard: %v", err)
	}
	insert := func(ts time.Time, model string) (int64, error) {
		id, err := NextShardID(d, "conversation_records_id_seq")
		if err != nil {
			return 0, err
		}
		tbl := "conversation_records_" + ts.Format("2006_01")
		_, err = d.Exec(Rebind(d, `INSERT INTO `+tbl+`
			(id, upstream_name, model, in_format, up_format, harness, user_agent, stream, status,
			 total_tokens, request_ir, response_ir, request_raw, response_raw, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
			id, "up", model, "openai", "anthropic", "claude-code", "test", 0, "ok", 10,
			`{"a":1}`, `{"b":2}`, []byte("req"), []byte("resp"), ts)
		return id, err
	}
	a, err := insert(time.Now(), "m-a")
	if err != nil {
		t.Fatalf("insert a: %v", err)
	}
	b, err := insert(time.Now(), "m-b")
	if err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if a == b {
		t.Fatalf("duplicate shard ids %d", a)
	}

	// 分表发现：LIKE 捞 + Go 侧白名单复校验
	shards, err := ListTablesLike(d, "conversation_records")
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(shards, "conversation_records_"+time.Now().Format("2006_01")) {
		t.Fatalf("shard not discovered: %v", shards)
	}

	// 存量库的裸 conversation_records 是「历史分表」，也必须被捞回来。这里只关心
	// 名字能否被发现，表结构无关紧要，故建一张最小表。前缀带尾下划线
	// （conversation_records_%）就会漏掉它 —— 那是本分支修掉的回归。
	if _, err := d.Exec(`CREATE TABLE conversation_records (id BIGINT NOT NULL PRIMARY KEY)`); err != nil {
		t.Fatalf("create legacy base table: %v", err)
	}
	shards, err = ListTablesLike(d, "conversation_records")
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(shards, "conversation_records") {
		t.Fatalf("legacy base table not discovered: %v", shards)
	}

	// IsUndefinedTable 识别 MySQL 的 1146
	if err := d.QueryRow(`SELECT 1 FROM definitely_not_a_table`).Err(); !IsUndefinedTable(err) {
		t.Errorf("IsUndefinedTable(%v) = false, want true", err)
	}
}

// TestMySQL_E2E_MaxAllowedPacket 只是诊断：服务器 packet 上限装不下归档上限时
// 提前说出来（写失败只会在异步 writer 里静默记日志）。
func TestMySQL_E2E_MaxAllowedPacket(t *testing.T) {
	d := mysqlTestDB(t)
	var v int64
	if err := d.QueryRow(`SELECT @@max_allowed_packet`).Scan(&v); err != nil {
		t.Skipf("cannot read max_allowed_packet: %v", err)
	}
	if v < 64<<20 {
		t.Errorf("max_allowed_packet=%d < 64 MiB (convRawCap); 大响应归档必然写失败", v)
	} else {
		t.Logf("max_allowed_packet=%d", v)
	}
}

// deleteModelE2E / insertBalanceSnapshotE2E / listBalanceSnapshotsE2E 是 MySQL
// 侧需要、PG e2e 没覆盖到的助手。
func deleteModelE2E(d *sql.DB, id int64) error {
	_, err := d.Exec(Rebind(d, `UPDATE upstream_models SET is_active = 0 WHERE id=? AND is_active = 1`), id)
	return err
}

func insertBalanceSnapshotE2E(d *sql.DB, upstreamID int64, name, vendor, payload string) error {
	_, err := d.Exec(Rebind(d, `INSERT INTO balance_snapshots
		(upstream_id, upstream_name, vendor, payload, created_at) VALUES (?,?,?,?,?)`),
		upstreamID, name, vendor, payload, time.Now())
	return err
}

func listBalanceSnapshotsE2E(d *sql.DB, upstreamID int64) ([]struct {
	ID      int64
	Payload string
}, error) {
	rows, err := d.Query(Rebind(d, `SELECT id, payload FROM balance_snapshots
		WHERE upstream_id = ? ORDER BY id DESC`), upstreamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		ID      int64
		Payload string
	}
	for rows.Next() {
		var r struct {
			ID      int64
			Payload string
		}
		if err := rows.Scan(&r.ID, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ensureConvShardForTest 建当月分表。db 包不能 import model（会成环），所以这里
// 直接用渲染器 —— 生产路径走 store.EnsureConversationShard，两者同一份 DDL。
func ensureConvShardForTest(d *sql.DB, t time.Time) error {
	name := "conversation_records_" + t.Format("2006_01")
	stmts, err := ConversationShardDDL(DialectOf(d), name, "conversation_records_id_seq")
	if err != nil {
		return err
	}
	for _, s := range append(ShardSequenceStatements(DialectOf(d), "conversation_records_id_seq"), stmts...) {
		if _, err := d.Exec(s); err != nil {
			return fmt.Errorf("ensure shard %s: %q: %w", name, s, err)
		}
	}
	return nil
}

// containsString 是 contains 的切片版本（contains 只做子串匹配）。
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func tableOf(tableCol string) string {
	for i := 0; i < len(tableCol); i++ {
		if tableCol[i] == '.' {
			return tableCol[:i]
		}
	}
	return tableCol
}

func colOf(tableCol string) string {
	for i := 0; i < len(tableCol); i++ {
		if tableCol[i] == '.' {
			return tableCol[i+1:]
		}
	}
	return tableCol
}

// MySQL 的索引内联在 CREATE TABLE 里，天然没有「声明了却没建」的缺口；这个测试
// 把它钉成不变量（与 SQLite/PG 侧的同名断言对应），防的是将来渲染器改动把内联
// 索引弄丢。
func TestMySQL_E2E_FreshSchemaHasAllDeclaredIndexes(t *testing.T) {
	d := mysqlTestDB(t)
	for _, tbl := range tablesFor(DialectMySQL) {
		for _, ix := range tbl.Idx {
			var n int
			if err := d.QueryRow(`SELECT COUNT(DISTINCT index_name) FROM information_schema.statistics
				WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`, tbl.Name, ix.Name).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				t.Errorf("index %s on %s declared in schema but missing on a fresh database", ix.Name, tbl.Name)
			}
		}
	}
}
