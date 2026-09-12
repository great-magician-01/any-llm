package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// okUpstreamServer 返回一个固定应答指定文本的 OpenAI 格式假上游。
func okUpstreamServer(t *testing.T, text string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"` + text + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// failUpstreamServer 返回一个恒以给定状态码报错的假上游。
func failUpstreamServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(`{"error":{"message":"boom","type":"server_error"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setupAliasGateway(t *testing.T) (*Gateway, *model.ExtKey) {
	t.Helper()
	g, d := setupGateway(t)
	k, _ := model.CreateExtKey(d, "test", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)
	return g, k
}

func aliasRequest(t *testing.T, g *Gateway, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	return w
}

// 别名解析：无 / 的固定模型名路由到绑定的上游与真实模型。
func TestAliasRouting(t *testing.T) {
	srv := okUpstreamServer(t, "from-alias")
	g, k := setupAliasGateway(t)
	d := g.db
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk", Format: "openai"})
	if _, err := model.CreateAlias(d, &model.ModelAlias{Name: "fixed-gpt", Bindings: []model.AliasBinding{{UpstreamID: uid, ModelName: "gpt-4o"}}}); err != nil {
		t.Fatal(err)
	}

	w := aliasRequest(t, g, k.Key, `{"model":"fixed-gpt","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "from-alias") {
		t.Fatalf("body=%s", w.Body.String())
	}
	// 用量记录记实际上游模型
	records, total, _ := model.UsageRecordsList(d, 1, 10)
	if total != 1 || records[0].Model != "gpt-4o" || records[0].UpstreamName != "oai" || records[0].Status != "ok" {
		t.Fatalf("records=%+v total=%d", records, total)
	}
}

// 非流式故障转移：首选候选 500，自动切到第二候选成功；每次尝试各记一条 usage。
func TestAliasFailoverNonStream(t *testing.T) {
	bad := failUpstreamServer(t, 500)
	good := okUpstreamServer(t, "from-second")
	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := model.CreateUpstream(d, &model.Upstream{Name: "bad", BaseURL: bad.URL, APIKey: "sk", Format: "openai"})
	uid2, _ := model.CreateUpstream(d, &model.Upstream{Name: "good", BaseURL: good.URL, APIKey: "sk", Format: "openai"})
	model.CreateAlias(d, &model.ModelAlias{Name: "fixed", Bindings: []model.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	w := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "from-second") {
		t.Fatalf("body=%s", w.Body.String())
	}
	records, total, _ := model.UsageRecordsList(d, 1, 10)
	if total != 2 {
		t.Fatalf("usage records=%d want 2 (error+ok)", total)
	}
	var errRec, okRec int
	for _, r := range records {
		switch {
		case r.Status == "error" && r.UpstreamName == "bad" && r.Model == "m1":
			errRec++
		case r.Status == "ok" && r.UpstreamName == "good" && r.Model == "m2" && r.TotalTokens == 2:
			okRec++
		}
	}
	if errRec != 1 || okRec != 1 {
		t.Fatalf("records=%+v", records)
	}
}

// 流式故障转移：头部 flush 后首选候选失败，客户端透明地收到第二候选的流。
func TestAliasFailoverStream(t *testing.T) {
	bad := failUpstreamServer(t, 500)
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n"))
		f.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer good.Close()

	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := model.CreateUpstream(d, &model.Upstream{Name: "bad", BaseURL: bad.URL, APIKey: "sk", Format: "openai"})
	uid2, _ := model.CreateUpstream(d, &model.Upstream{Name: "good", BaseURL: good.URL, APIKey: "sk", Format: "openai"})
	model.CreateAlias(d, &model.ModelAlias{Name: "fixed", Bindings: []model.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	w := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"content":"Hi"`) || !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("stream body=%s", w.Body.String())
	}
	// 不得包含带内错误帧（故障转移对客户端透明）
	var sawErr bool
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"error"`) {
			sawErr = true
		}
	}
	if sawErr {
		t.Fatalf("stream contains error frame: %s", w.Body.String())
	}
	_, total, _ := model.UsageRecordsList(d, 1, 10)
	if total != 2 {
		t.Fatalf("usage records=%d want 2", total)
	}
}

// 全部候选失败：按最后一个错误映射回客户端格式。
func TestAliasAllCandidatesFail(t *testing.T) {
	bad1 := failUpstreamServer(t, 500)
	bad2 := failUpstreamServer(t, 429)
	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := model.CreateUpstream(d, &model.Upstream{Name: "bad1", BaseURL: bad1.URL, APIKey: "sk", Format: "openai"})
	uid2, _ := model.CreateUpstream(d, &model.Upstream{Name: "bad2", BaseURL: bad2.URL, APIKey: "sk", Format: "openai"})
	model.CreateAlias(d, &model.ModelAlias{Name: "fixed", Bindings: []model.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	w := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 429 {
		t.Fatalf("status=%d want 429 (last candidate), body=%s", w.Code, w.Body.String())
	}
	_, total, _ := model.UsageRecordsList(d, 1, 10)
	if total != 2 {
		t.Fatalf("usage records=%d want 2", total)
	}
}

// 别名存在但绑定全部不可用（上游已删）→ 404。
func TestAliasNoAvailableBindings(t *testing.T) {
	g, k := setupAliasGateway(t)
	d := g.db
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: "http://127.0.0.1:1", APIKey: "sk", Format: "openai"})
	model.CreateAlias(d, &model.ModelAlias{Name: "fixed", Bindings: []model.AliasBinding{{UpstreamID: uid, ModelName: "m"}}})
	if err := model.DeleteUpstream(d, uid); err != nil {
		t.Fatal(err)
	}

	w := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[]}`)
	if w.Code != 404 {
		t.Fatalf("status=%d want 404, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no available bindings") {
		t.Fatalf("body=%s", w.Body.String())
	}
}

// 首选候选上游日限额超限：dispatch 前跳过，直接用第二候选（不记失败 usage）。
func TestAliasCandidateSkippedByUpstreamLimit(t *testing.T) {
	good := okUpstreamServer(t, "from-second")
	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := model.CreateUpstream(d, &model.Upstream{Name: "limited", BaseURL: "http://127.0.0.1:1", APIKey: "sk", Format: "openai", DailyTokenLimit: 10})
	uid2, _ := model.CreateUpstream(d, &model.Upstream{Name: "good", BaseURL: good.URL, APIKey: "sk", Format: "openai"})
	model.CreateAlias(d, &model.ModelAlias{Name: "fixed", Bindings: []model.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})
	model.InsertUsage(d, &model.UsageRecord{UpstreamID: &uid1, UpstreamName: "limited", Model: "m1", InFormat: "openai", UpFormat: "openai", TotalTokens: 10, Status: "ok"})

	w := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "from-second") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	// 限额跳过不产生 error usage：只有预置的 1 条 + 成功的 1 条
	_, total, _ := model.UsageRecordsList(d, 1, 10)
	if total != 2 {
		t.Fatalf("usage records=%d want 2 (pre-seeded + ok)", total)
	}
}

// 首选候选上游被禁用：解析时直接跳过（不 dispatch、不记失败 usage），落到第二候选。
func TestAliasCandidateDisabledFallback(t *testing.T) {
	good := okUpstreamServer(t, "from-second")
	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := model.CreateUpstream(d, &model.Upstream{Name: "off", BaseURL: "http://127.0.0.1:1", APIKey: "sk", Format: "openai"})
	uid2, _ := model.CreateUpstream(d, &model.Upstream{Name: "good", BaseURL: good.URL, APIKey: "sk", Format: "openai"})
	model.CreateAlias(d, &model.ModelAlias{Name: "fixed", Bindings: []model.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})
	u, _ := model.GetUpstreamByID(d, uid1)
	u.Enabled = false
	if err := model.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}

	w := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "from-second") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	// 禁用跳过不产生 usage：只有成功的 1 条
	_, total, _ := model.UsageRecordsList(d, 1, 10)
	if total != 1 {
		t.Fatalf("usage records=%d want 1 (ok only, disabled candidate never dispatched)", total)
	}
}

// 别名候选全部被禁用 → 404 no available bindings（与全部删除一致）。
func TestAliasAllCandidatesDisabled(t *testing.T) {
	g, k := setupAliasGateway(t)
	d := g.db
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: "http://127.0.0.1:1", APIKey: "sk", Format: "openai"})
	model.CreateAlias(d, &model.ModelAlias{Name: "fixed", Bindings: []model.AliasBinding{{UpstreamID: uid, ModelName: "m"}}})
	u, _ := model.GetUpstreamByID(d, uid)
	u.Enabled = false
	if err := model.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}

	w := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[]}`)
	if w.Code != 404 {
		t.Fatalf("status=%d want 404, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no available bindings") {
		t.Fatalf("body=%s", w.Body.String())
	}
}

// /v1/models 包含有可用绑定的别名。
func TestModelsEndpointIncludesAliases(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	model.CreateAlias(d, &model.ModelAlias{Name: "fixed-gpt", Bindings: []model.AliasBinding{{UpstreamID: uid, ModelName: "gpt-4o"}}})
	// 无可用绑定的别名不出现
	model.CreateAlias(d, &model.ModelAlias{Name: "empty-alias", Bindings: nil})
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	ids := map[string]bool{}
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	if !ids["oai/gpt-4o"] || !ids["fixed-gpt"] {
		t.Fatalf("ids=%+v", ids)
	}
	if ids["empty-alias"] {
		t.Fatalf("empty alias should not be listed: %+v", ids)
	}
}

// 别名优先于直连拆分：精确匹配命中别名时按别名路由。
func TestAliasPrecedenceOverDirectFormat(t *testing.T) {
	aliasSrv := okUpstreamServer(t, "from-alias")
	directSrv := okUpstreamServer(t, "from-direct")
	g, k := setupAliasGateway(t)
	d := g.db
	uidA, _ := model.CreateUpstream(d, &model.Upstream{Name: "a", BaseURL: aliasSrv.URL, APIKey: "sk", Format: "openai"})
	uidB, _ := model.CreateUpstream(d, &model.Upstream{Name: "b", BaseURL: directSrv.URL, APIKey: "sk", Format: "openai"})
	model.AddModel(d, uidB, "m", false, 0, 0)
	// 别名名称带 /，遮蔽直连路由 "b/m"
	model.CreateAlias(d, &model.ModelAlias{Name: "b/m", Bindings: []model.AliasBinding{{UpstreamID: uidA, ModelName: "x"}}})

	w := aliasRequest(t, g, k.Key, `{"model":"b/m","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "from-alias") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
