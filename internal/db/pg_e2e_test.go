package db

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// pgTestDB opens a fresh PostgreSQL connection against the DB_TEST_PG_DSN
// and returns it after running migrations in an isolated schema that is
// dropped on cleanup. This lets the e2e test run repeatedly without polluting
// the shared database. Returns nil (and skips the test) if DB_TEST_PG_DSN
// is unset.
func pgTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set DB_TEST_PG_DSN to run postgres e2e tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	schema := fmt.Sprintf("any_llm_test_%d", time.Now().UnixNano())
	// search_path 走连接参数而非 SET：pgx 连接池里 SET 只影响单条连接，
	// 池内其他连接会漏配。
	cfg.RuntimeParams["search_path"] = schema
	d := stdlib.OpenDB(*cfg)
	if err := d.Ping(); err != nil {
		d.Close()
		t.Fatalf("ping: %v", err)
	}
	if _, err := d.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		d.Close()
		t.Fatalf("create schema: %v", err)
	}
	if err := MigratePGForTest(d); err != nil {
		d.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		d.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		d.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		d.Close()
	})
	return d
}

func TestPG_E2E_ModelCRUD(t *testing.T) {
	d := pgTestDB(t)

	// dialect detection
	if got := DialectOf(d); got != DialectPostgres {
		t.Fatalf("DialectOf=%q want postgres", got)
	}

	// rebind sanity
	if got := Rebind(d, "SELECT ?, ?, '?'"); got != "SELECT $1, $2, '?'" {
		t.Fatalf("rebind=%q", got)
	}

	// upstream CRUD
	uid1, err := createUpstreamE2E(d, "u1", "https://api.openai.com", "sk-abc", "openai")
	if err != nil {
		t.Fatalf("create upstream 1: %v", err)
	}
	uid2, err := createUpstreamE2E(d, "u2", "https://api.anthropic.com", "sk-xyz", "anthropic")
	if err != nil {
		t.Fatalf("create upstream 2: %v", err)
	}
	if uid1 == uid2 {
		t.Fatalf("duplicate ids %d", uid1)
	}

	got, err := getUpstreamByNameE2E(d, "u1")
	if err != nil {
		t.Fatalf("get upstream: %v", err)
	}
	if got.ID != uid1 || got.BaseURL != "https://api.openai.com" || got.Format != "openai" {
		t.Fatalf("upstream=%+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps zero: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}

	list, err := listUpstreamsE2E(d)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len=%d", len(list))
	}

	// models
	if err := addModelE2E(d, uid1, "gpt-4o", false); err != nil {
		t.Fatalf("add model: %v", err)
	}
	if err := addModelE2E(d, uid1, "gpt-4o-mini", false); err != nil {
		t.Fatalf("add model 2: %v", err)
	}
	// duplicate insert should be ignored (ON CONFLICT DO NOTHING)
	if err := addModelE2E(d, uid1, "gpt-4o", false); err != nil {
		t.Fatalf("dup add model: %v", err)
	}
	models, err := listModelsE2E(d, uid1)
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models len=%d want 2", len(models))
	}

	// replace models (simulates fetch-models)
	if err := replaceModelsE2E(d, uid1, []string{"gpt-4o", "gpt-5", "o3"}); err != nil {
		t.Fatalf("replace models: %v", err)
	}
	models, _ = listModelsE2E(d, uid1)
	if len(models) != 3 {
		t.Fatalf("after replace len=%d want 3", len(models))
	}

	// delete upstream cascades to models
	if err := deleteUpstreamE2E(d, uid2); err != nil {
		t.Fatalf("delete upstream: %v", err)
	}
	list, _ = listUpstreamsE2E(d)
	if len(list) != 1 || list[0].ID != uid1 {
		t.Fatalf("after delete list=%+v", list)
	}
	_ = uid2
}

func TestPG_E2E_ExtKeyCRUD(t *testing.T) {
	d := pgTestDB(t)

	k1, err := createExtKeyE2E(d, "label-1")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if k1.ID == 0 {
		t.Fatal("id not returned")
	}
	k2, err := createExtKeyE2E(d, "label-2")
	if err != nil {
		t.Fatalf("create key 2: %v", err)
	}
	if k1.Key == k2.Key {
		t.Fatal("duplicate keys")
	}

	got, err := getExtKeyE2E(d, k1.Key)
	if err != nil {
		t.Fatalf("get key: %v", err)
	}
	if got.ID != k1.ID || got.Enabled == 0 {
		t.Fatalf("got=%+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("created_at zero")
	}

	// touch last_used_at
	if err := touchExtKeyE2E(d, k1.ID); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, _ = getExtKeyE2E(d, k1.Key)
	if !got.LastUsed.Valid {
		t.Fatal("last_used_at not set")
	}

	list, err := listExtKeysE2E(d)
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len=%d", len(list))
	}
	for _, k := range list {
		if !contains(k.Key, "****") {
			t.Fatalf("key not masked: %q", k.Key)
		}
	}

	if err := deleteExtKeyE2E(d, k1.ID); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	list, _ = listExtKeysE2E(d)
	if len(list) != 1 {
		t.Fatalf("after delete len=%d", len(list))
	}
}

func TestPG_E2E_UsageAndSummary(t *testing.T) {
	d := pgTestDB(t)

	uid, _ := createUpstreamE2E(d, "up", "https://x", "k", "openai")
	k, _ := createExtKeyE2E(d, "l")
	uidPtr := uid
	kID := k.ID

	// insert a few usage records
	recs := []usageRecord{
		{extKeyID: &kID, upstreamID: &uidPtr, upstreamName: "up", model: "gpt-4o", inFormat: "openai", upFormat: "openai", prompt: 10, completion: 5, total: 15, stream: false, status: "ok"},
		{extKeyID: &kID, upstreamID: &uidPtr, upstreamName: "up", model: "gpt-4o", inFormat: "openai", upFormat: "openai", prompt: 20, completion: 10, total: 30, stream: true, status: "ok"},
		{extKeyID: &kID, upstreamID: &uidPtr, upstreamName: "up", model: "gpt-4o-mini", inFormat: "anthropic", upFormat: "anthropic", prompt: 5, completion: 5, total: 10, stream: false, status: "error"},
	}
	for _, r := range recs {
		if err := insertUsageE2E(d, r); err != nil {
			t.Fatalf("insert usage: %v", err)
		}
	}

	// summary by model — exercises the COALESCE(CAST(... AS TEXT),'0') fix
	// indirectly (group by model here, but the key grouping path is the one
	// that previously broke due to BIGINT -> string scan).
	sumByKey, err := usageSummaryE2E(d, "key", "", "")
	if err != nil {
		t.Fatalf("summary by key: %v", err)
	}
	if len(sumByKey) != 1 {
		t.Fatalf("summary by key len=%d", len(sumByKey))
	}
	if sumByKey[0].requestCount != 3 || sumByKey[0].totalTokens != 55 {
		t.Fatalf("summary by key=%+v", sumByKey[0])
	}
	if sumByKey[0].okCount != 2 || sumByKey[0].errorCount != 1 {
		t.Fatalf("ok/err=%+v", sumByKey[0])
	}

	sumByModel, err := usageSummaryE2E(d, "model", "", "")
	if err != nil {
		t.Fatalf("summary by model: %v", err)
	}
	if len(sumByModel) != 2 {
		t.Fatalf("summary by model len=%d", len(sumByModel))
	}

	// list records with paging
	records, total, err := usageRecordsListE2E(d, 1, 2)
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	if total != 3 || len(records) != 2 {
		t.Fatalf("total=%d page len=%d", total, len(records))
	}
	if !records[0].CreatedAt.IsZero() {
		// created_at scanned as time.Time successfully
	} else {
		t.Fatal("created_at zero on PG")
	}
}

func TestPG_E2E_WriterPath(t *testing.T) {
	d := pgTestDB(t)

	w := NewWriter(d, 16)
	w.Start()
	defer w.Stop()

	// DoSync: create upstream via writer
	var uid int64
	if err := w.DoSync(func(db *sql.DB) error {
		var e error
		uid, e = createUpstreamE2E(db, "w-up", "https://w", "k", "openai")
		return e
	}); err != nil {
		t.Fatalf("DoSync create: %v", err)
	}
	if uid == 0 {
		t.Fatal("uid not returned via writer")
	}

	// DoAsync: touch key via writer, then verify with a sync read
	k, _ := createExtKeyE2E(d, "wl")
	w.DoAsync(func(db *sql.DB) error { return touchExtKeyE2E(db, k.ID) })

	// wait for async to drain by doing a sync op (writer is single-goroutine,
	// so a subsequent DoSync observes ordering after the async)
	var got lastUsed
	if err := w.DoSync(func(db *sql.DB) error {
		row := db.QueryRow(Rebind(db, "SELECT last_used_at FROM ext_keys WHERE id=?"), k.ID)
		var nu sql.NullTime
		if err := row.Scan(&nu); err != nil {
			return err
		}
		got.nu = nu
		return nil
	}); err != nil {
		t.Fatalf("DoSync read: %v", err)
	}
	if !got.nu.Valid {
		t.Fatal("async touch did not apply (last_used_at NULL)")
	}
}

type lastUsed struct {
	nu sql.NullTime
}

// --- thin SQL helpers (mirror the model package but keep this test
//     self-contained so failures point at the SQL/dialect layer, not at
//     the model package's own logic which has its own tests). ---

type usageRecord struct {
	extKeyID     *int64
	upstreamID   *int64
	upstreamName string
	model        string
	inFormat     string
	upFormat     string
	prompt       int
	completion   int
	total        int
	stream       bool
	status       string
}

type summaryRow struct {
	groupKey      string
	requestCount  int
	totalTokens   int
	promptTokens  int
	completionTok int
	okCount       int
	errorCount    int
}

func createUpstreamE2E(d *sql.DB, name, baseURL, apiKey, format string) (int64, error) {
	var id int64
	err := d.QueryRow(Rebind(d, `INSERT INTO upstreams (name, base_url, api_key, format) VALUES (?,?,?,?) RETURNING id`),
		name, baseURL, apiKey, format).Scan(&id)
	return id, err
}

func getUpstreamByNameE2E(d *sql.DB, name string) (struct {
	ID, ModelCount                int64
	Name, BaseURL, APIKey, Format string
	CreatedAt, UpdatedAt          time.Time
}, error) {
	var u struct {
		ID, ModelCount                int64
		Name, BaseURL, APIKey, Format string
		CreatedAt, UpdatedAt          time.Time
	}
	err := d.QueryRow(Rebind(d, `SELECT id, name, base_url, api_key, format, created_at, updated_at FROM upstreams WHERE name=? AND is_active = 1`), name).
		Scan(&u.ID, &u.Name, &u.BaseURL, &u.APIKey, &u.Format, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func listUpstreamsE2E(d *sql.DB) ([]struct {
	ID         int64
	Name       string
	ModelCount int64
}, error) {
	rows, err := d.Query(`SELECT u.id, u.name,
		(SELECT COUNT(*) FROM upstream_models WHERE upstream_id = u.id AND is_active = 1) AS model_count
		FROM upstreams u WHERE u.is_active = 1 ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		ID         int64
		Name       string
		ModelCount int64
	}
	for rows.Next() {
		var u struct {
			ID         int64
			Name       string
			ModelCount int64
		}
		if err := rows.Scan(&u.ID, &u.Name, &u.ModelCount); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func deleteUpstreamE2E(d *sql.DB, id int64) error {
	_, err := d.Exec(Rebind(d, `UPDATE upstreams SET is_active = 0 WHERE id=? AND is_active = 1`), id)
	if err != nil {
		return err
	}
	_, err = d.Exec(Rebind(d, `UPDATE upstream_models SET is_active = 0 WHERE upstream_id=? AND is_active = 1`), id)
	return err
}

func addModelE2E(d *sql.DB, upstreamID int64, name string, manual bool) error {
	m := 0
	if manual {
		m = 1
	}
	_, err := d.Exec(Rebind(d, `INSERT INTO upstream_models (upstream_id, model_name, manual) VALUES (?,?,?) ON CONFLICT (upstream_id, model_name) WHERE is_active = 1 DO NOTHING`),
		upstreamID, name, m)
	return err
}

func listModelsE2E(d *sql.DB, upstreamID int64) ([]struct {
	ID         int64
	UpstreamID int64
	ModelName  string
	Manual     int
}, error) {
	rows, err := d.Query(Rebind(d, `SELECT id, upstream_id, model_name, manual FROM upstream_models WHERE upstream_id=? AND is_active = 1 ORDER BY model_name`), upstreamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		ID         int64
		UpstreamID int64
		ModelName  string
		Manual     int
	}
	for rows.Next() {
		var m struct {
			ID         int64
			UpstreamID int64
			ModelName  string
			Manual     int
		}
		if err := rows.Scan(&m.ID, &m.UpstreamID, &m.ModelName, &m.Manual); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func replaceModelsE2E(d *sql.DB, upstreamID int64, names []string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(Rebind(d, `UPDATE upstream_models SET is_active = 0 WHERE upstream_id=? AND manual=0 AND is_active = 1`), upstreamID); err != nil {
		return err
	}
	for _, n := range names {
		if _, err := tx.Exec(Rebind(d, `UPDATE upstream_models SET is_active = 1 WHERE upstream_id=? AND model_name=? AND manual=0 AND is_active = 0`), upstreamID, n); err != nil {
			return err
		}
		if _, err := tx.Exec(Rebind(d, `INSERT INTO upstream_models (upstream_id, model_name, manual) VALUES (?,?,0) ON CONFLICT (upstream_id, model_name) WHERE is_active = 1 DO NOTHING`), upstreamID, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func createExtKeyE2E(d *sql.DB, label string) (struct {
	ID      int64
	Key     string
	Label   string
	Enabled int
}, error) {
	key, err := generateKeyE2E()
	if err != nil {
		return struct {
			ID      int64
			Key     string
			Label   string
			Enabled int
		}{}, err
	}
	var id int64
	err = d.QueryRow(Rebind(d, `INSERT INTO ext_keys (key, label) VALUES (?,?) RETURNING id`), key, label).Scan(&id)
	if err != nil {
		return struct {
			ID      int64
			Key     string
			Label   string
			Enabled int
		}{}, err
	}
	return struct {
		ID      int64
		Key     string
		Label   string
		Enabled int
	}{ID: id, Key: key, Label: label, Enabled: 1}, nil
}

func getExtKeyE2E(d *sql.DB, key string) (struct {
	ID        int64
	Key       string
	Label     string
	Enabled   int
	CreatedAt time.Time
	LastUsed  sql.NullTime
}, error) {
	var k struct {
		ID        int64
		Key       string
		Label     string
		Enabled   int
		CreatedAt time.Time
		LastUsed  sql.NullTime
	}
	err := d.QueryRow(Rebind(d, `SELECT id, key, label, enabled, created_at, last_used_at FROM ext_keys WHERE key=?`), key).
		Scan(&k.ID, &k.Key, &k.Label, &k.Enabled, &k.CreatedAt, &k.LastUsed)
	return k, err
}

func listExtKeysE2E(d *sql.DB) ([]struct {
	ID      int64
	Key     string
	Label   string
	Enabled int
}, error) {
	rows, err := d.Query(`SELECT id, key, label, enabled FROM ext_keys WHERE is_active = 1 ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		ID      int64
		Key     string
		Label   string
		Enabled int
	}
	for rows.Next() {
		var k struct {
			ID      int64
			Key     string
			Label   string
			Enabled int
		}
		if err := rows.Scan(&k.ID, &k.Key, &k.Label, &k.Enabled); err != nil {
			return nil, err
		}
		k.Key = maskKeyE2E(k.Key)
		out = append(out, k)
	}
	return out, nil
}

func touchExtKeyE2E(d *sql.DB, id int64) error {
	_, err := d.Exec(Rebind(d, `UPDATE ext_keys SET last_used_at=CURRENT_TIMESTAMP WHERE id=?`), id)
	return err
}

func deleteExtKeyE2E(d *sql.DB, id int64) error {
	_, err := d.Exec(Rebind(d, `UPDATE ext_keys SET is_active = 0 WHERE id=? AND is_active = 1`), id)
	return err
}

func insertUsageE2E(d *sql.DB, r usageRecord) error {
	stream := 0
	if r.stream {
		stream = 1
	}
	_, err := d.Exec(Rebind(d, `INSERT INTO usage_records
		(ext_key_id, upstream_id, upstream_name, model, in_format, up_format,
		 prompt_tokens, completion_tokens, total_tokens, stream, status, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`),
		r.extKeyID, r.upstreamID, r.upstreamName, r.model, r.inFormat, r.upFormat,
		r.prompt, r.completion, r.total, stream, r.status, time.Now())
	return err
}

func usageSummaryE2E(d *sql.DB, groupBy, from, to string) ([]summaryRow, error) {
	var groupCol string
	switch groupBy {
	case "key":
		groupCol = "COALESCE(CAST(ext_key_id AS TEXT), '0')"
	case "model":
		groupCol = "model"
	case "upstream":
		groupCol = "upstream_name"
	default:
		groupCol = "model"
	}
	q := fmt.Sprintf(`SELECT %s AS gk, COUNT(*), SUM(total_tokens), SUM(prompt_tokens), SUM(completion_tokens),
		SUM(CASE WHEN status='ok' THEN 1 ELSE 0 END), SUM(CASE WHEN status='error' THEN 1 ELSE 0 END)
		FROM usage_records`, groupCol)
	var conditions []string
	var args []any
	if from != "" {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, from)
	}
	if to != "" {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, to)
	}
	if len(conditions) > 0 {
		q += " WHERE " + joinAnd(conditions)
	}
	q += fmt.Sprintf(" GROUP BY %s ORDER BY gk", groupCol)
	rows, err := d.Query(Rebind(d, q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []summaryRow
	for rows.Next() {
		var s summaryRow
		if err := rows.Scan(&s.groupKey, &s.requestCount, &s.totalTokens, &s.promptTokens,
			&s.completionTok, &s.okCount, &s.errorCount); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func usageRecordsListE2E(d *sql.DB, page, size int) ([]struct {
	ID               int64
	ExtKeyID         sql.NullInt64
	UpstreamID       sql.NullInt64
	UpstreamName     string
	Model            string
	InFormat         string
	UpFormat         string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Stream           int
	Status           string
	CreatedAt        time.Time
}, int, error) {
	var total int
	if err := d.QueryRow("SELECT COUNT(*) FROM usage_records").Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * size
	rows, err := d.Query(Rebind(d, `SELECT id, ext_key_id, upstream_id, upstream_name, model, in_format, up_format,
		prompt_tokens, completion_tokens, total_tokens, stream, status, created_at
		FROM usage_records ORDER BY id DESC LIMIT ? OFFSET ?`), size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	type rec = struct {
		ID               int64
		ExtKeyID         sql.NullInt64
		UpstreamID       sql.NullInt64
		UpstreamName     string
		Model            string
		InFormat         string
		UpFormat         string
		PromptTokens     int
		CompletionTokens int
		TotalTokens      int
		Stream           int
		Status           string
		CreatedAt        time.Time
	}
	var out []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.ID, &r.ExtKeyID, &r.UpstreamID, &r.UpstreamName, &r.Model,
			&r.InFormat, &r.UpFormat, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
			&r.Stream, &r.Status, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, nil
}

// tiny helpers to avoid pulling strings/fmt duplication
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func joinAnd(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " AND "
		}
		out += p
	}
	return out
}

func maskKeyE2E(key string) string {
	if len(key) < 16 {
		return key + "****"
	}
	return key[:12] + "****" + key[len(key)-4:]
}

func generateKeyE2E() (string, error) {
	b := make([]byte, 32)
	const chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		b[i] = chars[n.Int64()]
	}
	return "all-sk-" + string(b), nil
}

// TestPG_SoftDeleteMigrationFromOldSchema 在旧版 schema（内联 UNIQUE、外键、
// format CHECK）上跑当前迁移管线，验证：约束全部移除、is_active 列就位
// （存量行默认活跃）、部分唯一索引生效、软删除后同名可重建。
func TestPG_SoftDeleteMigrationFromOldSchema(t *testing.T) {
	dsn := os.Getenv("DB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set DB_TEST_PG_DSN to run postgres e2e tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	schema := fmt.Sprintf("any_llm_oldmig_test_%d", time.Now().UnixNano())
	// search_path 走连接参数而非 SET：pgx 连接池里 SET 只影响单条连接。
	cfg.RuntimeParams["search_path"] = schema
	d := stdlib.OpenDB(*cfg)
	if err := d.Ping(); err != nil {
		d.Close()
		t.Fatalf("ping: %v", err)
	}
	if _, err := d.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		d.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		d.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		d.Close()
	})

	oldDDL := `
CREATE TABLE upstreams (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    base_url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    format TEXT NOT NULL CHECK(format IN ('openai','anthropic')),
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE upstream_models (
    id BIGSERIAL PRIMARY KEY,
    upstream_id BIGINT NOT NULL REFERENCES upstreams(id) ON DELETE CASCADE,
    model_name TEXT NOT NULL,
    manual INTEGER NOT NULL DEFAULT 0,
    context_length INTEGER NOT NULL DEFAULT 200000,
    max_output_length INTEGER NOT NULL DEFAULT 200000,
    UNIQUE(upstream_id, model_name)
);
CREATE TABLE ext_keys (
    id BIGSERIAL PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP(0)
);
CREATE TABLE usage_records (
    id BIGSERIAL PRIMARY KEY,
    ext_key_id BIGINT REFERENCES ext_keys(id),
    upstream_id BIGINT,
    upstream_name TEXT NOT NULL,
    model TEXT NOT NULL,
    in_format TEXT NOT NULL,
    up_format TEXT NOT NULL,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    stream INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'ok',
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE conversation_records (
    id BIGSERIAL PRIMARY KEY,
    ext_key_id BIGINT REFERENCES ext_keys(id),
    upstream_id BIGINT,
    upstream_name TEXT NOT NULL,
    model TEXT NOT NULL,
    in_format TEXT NOT NULL,
    up_format TEXT NOT NULL,
    harness TEXT NOT NULL,
    user_agent TEXT NOT NULL,
    stream INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'ok',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    request_ir JSONB NOT NULL DEFAULT '{}'::jsonb,
    response_ir JSONB NOT NULL DEFAULT '{}'::jsonb,
    request_raw BYTEA NOT NULL,
    response_raw BYTEA NOT NULL,
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
);`
	if _, err := d.Exec(oldDDL); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u1','http://x','k1','openai')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO upstream_models (upstream_id, model_name) VALUES (1,'m1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO ext_keys (key, label) VALUES ('all-sk-old','legacy')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO usage_records (ext_key_id, upstream_id, upstream_name, model, in_format, up_format) VALUES (1, 1, 'u1', 'm1', 'anthropic', 'openai')`); err != nil {
		t.Fatal(err)
	}

	// 与 OpenPG 相同的迁移管线
	if _, err := d.Exec(migrationPG); err != nil {
		t.Fatalf("migrationPG: %v", err)
	}
	if err := migrateExtraCols(d); err != nil {
		t.Fatalf("extraCols: %v", err)
	}
	if err := migrateSoftDelete(d); err != nil {
		t.Fatalf("soft delete migrate: %v", err)
	}

	// CHECK 已移除：可插入 responses
	if _, err := d.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u2','http://y','k2','responses')`); err != nil {
		t.Fatalf("CHECK not dropped: %v", err)
	}
	// 外键已移除：坏 id 可插入
	if _, err := d.Exec(`INSERT INTO upstream_models (upstream_id, model_name) VALUES (999,'m2')`); err != nil {
		t.Fatalf("FK not dropped: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO usage_records (ext_key_id, upstream_name, model, in_format, up_format) VALUES (999,'x','y','z','w')`); err != nil {
		t.Fatalf("usage FK not dropped: %v", err)
	}
	// 存量行默认活跃
	var active int
	if err := d.QueryRow(`SELECT COUNT(*) FROM upstreams WHERE is_active = 1`).Scan(&active); err != nil || active != 2 {
		t.Fatalf("existing rows should default active: n=%d err=%v", active, err)
	}
	// 部分唯一索引：活跃行重名被拒；软删除后同名可重建
	if _, err := d.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u1','http://z','k3','openai')`); err == nil {
		t.Fatal("duplicate active name should be rejected")
	}
	if _, err := d.Exec(`UPDATE upstreams SET is_active = 0 WHERE name='u1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u1','http://z2','k4','openai')`); err != nil {
		t.Fatalf("re-create same name after soft delete: %v", err)
	}
	// 旧数据完好
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM upstream_models WHERE model_name='m1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("old rows kept: n=%d err=%v", n, err)
	}
}
