package adminapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/store"
)

// 业务规则：关服期间管理端写请求必须是 **503 Service Unavailable**，不是 500，
// 也不是 400。db.Writer 在所有写都经过它时会以 ErrWriterStopped 拒绝新写入；
// writeSyncErr 把它单独映射成 503（"server is shutting down"），其余错误仍按
// 调用方给的状态码（400 客户端原因 / 500 内部故障）返回。
//
// 这条规则对客户端有实际后果：503 是可重试信号、500 会让运维去查内部故障、
// 400 会让管理员以为自己的输入有问题。

// wstopAPI 返回一个 writer 已停止的 admin API：此后每一次写都必然拿到
// ErrWriterStopped，用来验证 HTTP 层的映射。
func wstopAPI(t *testing.T) (*API, *sql.DB) {
	t.Helper()
	store.ResetConfigCache()
	t.Cleanup(store.ResetConfigCache)
	d, err := db.OpenSQLite(filepath.Join(t.TempDir(), "wstop.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	w := db.NewWriter(d, 8)
	w.Start()
	w.Stop()
	return NewAPI(d, w, nil), d
}

// 直接钉住映射函数本身：ErrWriterStopped（含被 %w 包装的）→ 503，
// 其它错误 → 调用方给的状态码，两个分支都不能互相污染。
func TestWriteSyncErrMapsWriterStoppedTo503(t *testing.T) {
	cases := []struct {
		name   string
		status int
		err    error
		want   int
	}{
		{"writer stopped", 400, db.ErrWriterStopped, 503},
		{"writer stopped, wrapped", 400, fmt.Errorf("create key: %w", db.ErrWriterStopped), 503},
		{"writer stopped, internal default", 500, db.ErrWriterStopped, 503},
		{"other error keeps 400", 400, errors.New("label already exists"), 400},
		{"other error keeps 500", 500, errors.New("db exploded"), 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeSyncErr(rec, tc.status, tc.err)
			if rec.Code != tc.want {
				t.Fatalf("status=%d，期望 %d（body=%s）", rec.Code, tc.want, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应不是 JSON: %v (%s)", err, rec.Body.String())
			}
			msg, _ := body["error"].(string)
			if tc.want == 503 {
				if msg != "server is shutting down" {
					t.Fatalf("503 的错误文案=%q，客户端靠它区分「稍后重试」与内部故障", msg)
				}
			} else if msg != tc.err.Error() {
				t.Fatalf("错误文案=%q，期望透传 %q", msg, tc.err.Error())
			}
		})
	}
}

// 端到端：writer 停止后走完整 handler 链（writeSync → DoSync → writeSyncErr），
// admin 写接口必须回 503，且不能有任何半成品写入落库（行数前后不变）。
func TestAdminWriteAfterWriterStopReturns503(t *testing.T) {
	a, d := wstopAPI(t)
	h := a.Handler()

	// 预置被更新的目标行（直接落库，不经 writeSync）：否则 handler 会先在
	// 「读现值」步骤回 404/400，走不到我们关心的那次写。
	upID, err := store.CreateUpstream(d, &store.Upstream{Name: "u1", BaseURL: "https://x", APIKey: "k", Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateExtKey(d, "k1", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		method, path string
		body         string
	}{
		{"POST", "/api/admin/keys", `{"label":"k2"}`},
		{"POST", "/api/admin/upstreams", `{"name":"u2","base_url":"https://x","api_key":"k","format":"openai"}`},
		{"POST", "/api/admin/aliases", fmt.Sprintf(`{"name":"a1","bindings":[{"upstream_id":%d,"model_name":"m1"}]}`, upID)},
		{"PUT", "/api/admin/keys/" + strconv.FormatInt(key.ID, 10), `{"label":"renamed"}`},
		{"DELETE", "/api/admin/keys/" + strconv.FormatInt(key.ID, 10), ``},
		{"DELETE", "/api/admin/upstreams/" + strconv.FormatInt(upID, 10), ``},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte(tc.body)))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 503 {
				t.Fatalf("status=%d，期望 503（body=%s）", rec.Code, rec.Body.String())
			}
			var resp map[string]any
			json.Unmarshal(rec.Body.Bytes(), &resp)
			if resp["error"] != "server is shutting down" {
				t.Fatalf("错误文案=%v，期望 server is shutting down", resp["error"])
			}
		})
	}

	// 关服期间的写请求不能留下半成品：预置的两行还在，且没有新增/删除。
	for tbl, want := range map[string]int{"ext_keys": 1, "upstreams": 1, "model_aliases": 0} {
		var n int
		if err := d.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("%s 行数=%d，期望 %d：关服期间的写请求不该落库", tbl, n, want)
		}
	}
}

// 对照：writer 正常工作时同一批接口不返回 503（否则上面的用例可能只是碰巧
// 命中了一个恒返回 503 的路径）。同时确认 5xx 不是默认行为——正常写是 200。
func TestAdminWriteWithRunningWriterIsNot503(t *testing.T) {
	store.ResetConfigCache()
	t.Cleanup(store.ResetConfigCache)
	d, err := db.OpenSQLite(filepath.Join(t.TempDir(), "wrun.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	w := db.NewWriter(d, 8)
	w.Start()
	t.Cleanup(w.Stop)
	a := NewAPI(d, w, nil)

	req := httptest.NewRequest("POST", "/api/admin/keys", bytes.NewReader([]byte(`{"label":"live"}`)))
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	if rec.Code == 503 {
		t.Fatalf("writer 正常工作时不该回 503: %s", rec.Body.String())
	}
	if rec.Code != 200 {
		t.Fatalf("status=%d，期望 200（body=%s）", rec.Code, rec.Body.String())
	}
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM ext_keys").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("ext_keys 行数=%d，期望 1", n)
	}
}
