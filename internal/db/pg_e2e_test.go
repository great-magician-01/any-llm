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
	d := stdlib.OpenDB(*cfg)
	if err := d.Ping(); err != nil {
		d.Close()
		t.Fatalf("ping: %v", err)
	}
	schema := fmt.Sprintf("any_llm_test_%d", time.Now().UnixNano())
	if _, err := d.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		d.Close()
		t.Fatalf("create schema: %v", err)
	}
	if _, err := d.Exec(fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		d.Close()
		t.Fatalf("set search_path: %v", err)
	}
	if _, err := d.Exec(migrationPG); err != nil {
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
	err := d.QueryRow(Rebind(d, `SELECT id, name, base_url, api_key, format, created_at, updated_at FROM upstreams WHERE name=?`), name).
		Scan(&u.ID, &u.Name, &u.BaseURL, &u.APIKey, &u.Format, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func listUpstreamsE2E(d *sql.DB) ([]struct {
	ID         int64
	Name       string
	ModelCount int64
}, error) {
	rows, err := d.Query(`SELECT u.id, u.name,
		(SELECT COUNT(*) FROM upstream_models WHERE upstream_id = u.id) AS model_count
		FROM upstreams u ORDER BY u.id`)
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
	_, err := d.Exec(Rebind(d, `DELETE FROM upstreams WHERE id=?`), id)
	return err
}

func addModelE2E(d *sql.DB, upstreamID int64, name string, manual bool) error {
	m := 0
	if manual {
		m = 1
	}
	_, err := d.Exec(Rebind(d, `INSERT INTO upstream_models (upstream_id, model_name, manual) VALUES (?,?,?) ON CONFLICT (upstream_id, model_name) DO NOTHING`),
		upstreamID, name, m)
	return err
}

func listModelsE2E(d *sql.DB, upstreamID int64) ([]struct {
	ID         int64
	UpstreamID int64
	ModelName  string
	Manual     int
}, error) {
	rows, err := d.Query(Rebind(d, `SELECT id, upstream_id, model_name, manual FROM upstream_models WHERE upstream_id=? ORDER BY model_name`), upstreamID)
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
	if _, err := tx.Exec(Rebind(d, `DELETE FROM upstream_models WHERE upstream_id=? AND manual=0`), upstreamID); err != nil {
		return err
	}
	for _, n := range names {
		if _, err := tx.Exec(Rebind(d, `INSERT INTO upstream_models (upstream_id, model_name, manual) VALUES (?,?,0) ON CONFLICT (upstream_id, model_name) DO NOTHING`), upstreamID, n); err != nil {
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
	rows, err := d.Query(`SELECT id, key, label, enabled FROM ext_keys ORDER BY id DESC`)
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
	_, err := d.Exec(Rebind(d, `DELETE FROM ext_keys WHERE id=?`), id)
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
