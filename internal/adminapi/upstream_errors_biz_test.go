package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// 本文件补 upstreams.go 上「错误确实是上游/环境给的」那几条分支 —— 它们可以用
// httptest 真实触发，不需要往 DB 层注入故障。同一个 handler 里依赖「DB 突然失败」
// 的兜底分支（list/write/replace 报错）仍保持未覆盖：那要伪造数据库故障，成本高
// 且会测出与实现耦合的假保证。

// TestFetchModels_UpstreamFailurePreservesModels 覆盖 fetchModels 的 502 分支。
// 重点不只是状态码：**失败路径绝不能走到 ReplaceModels** —— 那个函数是「按抓取
// 结果精确替换模型表」，一次失败的抓取若照样执行，就会把管理员手工配置的模型整表
// 清空。所以这里先播种一个手工模型，再让抓取失败，然后断言它还在。
func TestFetchModels_UpstreamFailurePreservesModels(t *testing.T) {
	a, d := setupAPI(t)
	a.client = upstream.NewClient(http.DefaultClient)

	// 关闭的服务器：端口还在但没人监听 → 传输错误。
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	uid, _ := store.CreateUpstream(d, &store.Upstream{
		Name: "dead", BaseURL: deadURL, APIKey: "k", Format: "openai",
	})
	if err := store.AddModel(d, uid, store.UpstreamModel{
		ModelName: "hand-configured", Manual: true, ContextLength: 1000, MaxOutputLength: 100,
	}); err != nil {
		t.Fatal(err)
	}

	w := doAliasReq(t, a, "POST", upstreamIDPath(uid)+"/fetch-models", nil)
	if w.Code != 502 {
		t.Fatalf("status=%d want 502 body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if msg, _ := body["error"].(string); msg == "" {
		t.Fatalf("502 未带 error 说明：%v", body)
	}

	ms, err := store.ListModels(d, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].ModelName != "hand-configured" {
		t.Fatalf("抓取失败却改动了模型表（ReplaceModels 不该被调用）：%+v", ms)
	}
}

// TestTestUpstreamConfig_NotConfiguredClient 覆盖未保存配置那条测试端点的 500。
// a.client 为 nil 是 setupAPI 的默认状态，也是启动顺序错误的真实形态。
func TestTestUpstreamConfig_NotConfiguredClient(t *testing.T) {
	a, _ := setupAPI(t) // client 为 nil

	w := doAliasReq(t, a, "POST", "/api/admin/upstreams/test", map[string]any{
		"base_url": "https://api.example.com", "api_key": "k", "format": "openai",
	})
	if w.Code != 500 {
		t.Fatalf("status=%d want 500 body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "upstream client not configured" {
		t.Fatalf("body=%v", body)
	}

	// 参数校验先于 client 检查：非法 format 仍报 400，不会被 500 盖掉。
	if w := doAliasReq(t, a, "POST", "/api/admin/upstreams/test", map[string]any{
		"base_url": "https://api.example.com", "format": "yaml",
	}); w.Code != 400 {
		t.Fatalf("bad format status=%d want 400", w.Code)
	}
}

// TestTestUpstream_ByIDWithOverrides 覆盖按 id 测试已保存上游时的覆盖语义：
// 空 api_key 与掩码占位符都沿用库存真 key，显式新值才替换。这条路径此前只测了
// 404 与「列表页不带体」两种，覆盖分支（324-335）没走。
func TestTestUpstream_ByIDWithOverrides(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer srv.Close()

	a, d := setupAPI(t)
	a.client = upstream.NewClient(http.DefaultClient)
	uid, _ := store.CreateUpstream(d, &store.Upstream{
		Name: "live", BaseURL: srv.URL, APIKey: "sk-stored-real", Format: "openai",
	})
	path := upstreamIDPath(uid) + "/test"

	// 显式新 key → 用新 key 发请求。
	if w := doAliasReq(t, a, "POST", path, map[string]any{
		"base_url": srv.URL, "api_key": "sk-override", "format": "openai",
	}); w.Code != 200 {
		t.Fatalf("override status=%d body=%s", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer sk-override" {
		t.Fatalf("override 未生效：Authorization=%q", gotAuth)
	}

	// 掩码占位符 → 沿用库存真 key（否则编辑表单点测试会用 "sk-****" 去认证而 401）。
	masked := mask("sk-stored-real")
	gotAuth = ""
	if w := doAliasReq(t, a, "POST", path, map[string]any{"api_key": masked}); w.Code != 200 {
		t.Fatalf("masked status=%d body=%s", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer sk-stored-real" {
		t.Fatalf("掩码回传未沿用库存 key：Authorization=%q", gotAuth)
	}

	// 空 key → 同样沿用库存 key（列表页点测试）。
	gotAuth = ""
	if w := doAliasReq(t, a, "POST", path, map[string]any{}); w.Code != 200 {
		t.Fatalf("empty body status=%d body=%s", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer sk-stored-real" {
		t.Fatalf("空 key 未沿用库存 key：Authorization=%q", gotAuth)
	}

	// format 覆盖：非法值 400，且不落库（u 是内存副本，库里的行不受影响）。
	if w := doAliasReq(t, a, "POST", path, map[string]any{"format": "yaml"}); w.Code != 400 {
		t.Fatalf("bad format status=%d want 400", w.Code)
	}
	got, err := store.GetUpstreamByID(d, uid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != "openai" || got.APIKey != "sk-stored-real" {
		t.Fatalf("测试端点改动了库存行：format=%q key=%q", got.Format, got.APIKey)
	}
}

// TestCreateUpstreamFormatValidation 补齐创建端点的非法 format 分支在响应体上的
// 断言（现有用例只断言状态码），确保错误文案对前端可读。
func TestCreateUpstreamFormatValidation(t *testing.T) {
	a, d := setupAPI(t)

	w := doAliasReq(t, a, "POST", "/api/admin/upstreams", map[string]any{
		"name": "bad", "base_url": "b", "api_key": "k", "format": "gemini",
	})
	if w.Code != 400 {
		t.Fatalf("status=%d want 400", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "format must be openai, anthropic or responses" {
		t.Fatalf("body=%v", body)
	}
	list, _ := store.ListUpstreams(d, nil)
	if len(list) != 0 {
		t.Fatalf("非法 format 落库了：%v", list)
	}

	// 负数 max_concurrent 同样 400（0 表示不限，负数无意义）。
	w = doAliasReq(t, a, "POST", "/api/admin/upstreams", map[string]any{
		"name": "negc", "base_url": "b", "api_key": "k", "format": "openai", "max_concurrent": -1,
	})
	if w.Code != 400 {
		t.Fatalf("negative max_concurrent status=%d want 400 body=%s", w.Code, w.Body.String())
	}
	if list, _ := store.ListUpstreams(d, nil); len(list) != 0 {
		t.Fatalf("负数 max_concurrent 落库了：%v", list)
	}
}

// TestUpstreamIDRoutesRejectBadID 覆盖 withID/withIDPair 的路径参数解析失败：
// 非数字 id 必须 404/400，不能当成 0 去查（id=0 不存在的行会掩盖真实的 URL 拼错）。
func TestUpstreamIDRoutesRejectBadID(t *testing.T) {
	a, _ := setupAPI(t)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/admin/upstreams/abc"},
		{"PUT", "/api/admin/upstreams/abc"},
		{"DELETE", "/api/admin/upstreams/abc"},
		{"POST", "/api/admin/upstreams/abc/fetch-models"},
		{"GET", "/api/admin/upstreams/abc/models"},
		{"DELETE", "/api/admin/upstreams/1/models/abc"},
	} {
		w := doAliasReq(t, a, tc.method, tc.path, nil)
		if w.Code != 400 && w.Code != 404 {
			t.Errorf("%s %s: status=%d want 400/404 body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
