package adminapi

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
)

// 本文件补 upstreams.go 上「整条端点没人调」和「校验分支没人走」的缺口。
// 口径与前几轮一致：只补真能到达的分支，不为了数字硬造 DB 故障。

// doRawReq 发一个**原样字符串**请求体，用来打非法 JSON 分支。
// doAliasReq 走 json.Marshal，造不出坏体。
func doRawReq(t *testing.T, a *API, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	return w
}

// upstreamIDPath 拼 /api/admin/upstreams/{id}。
func upstreamIDPath(id int64) string {
	return "/api/admin/upstreams/" + strconv.FormatInt(id, 10)
}

// TestGetUpstreamByID 覆盖 GET /api/admin/upstreams/{id} —— 此前一次都没被调用过
// （路由在，测试没有）。它和列表接口共用 mask 约定，必须一起钉住：前端编辑表单
// 靠这个响应回填，若把真 key 原样吐出来就是泄漏，若返回 404 分支写错则表单打不开。
func TestGetUpstreamByID(t *testing.T) {
	a, d := setupAPI(t)
	id, err := store.CreateUpstream(d, &store.Upstream{
		Name: "solo", BaseURL: "https://api.example.com", APIKey: "sk-secret-value-0123456789",
		Format: "anthropic", Remark: "备注",
	})
	if err != nil {
		t.Fatal(err)
	}

	w := doAliasReq(t, a, "GET", upstreamIDPath(id), nil)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["name"] != "solo" || got["format"] != "anthropic" || got["remark"] != "备注" {
		t.Fatalf("payload=%v", got)
	}
	// api_key 必须是掩码：真值出现在响应里就是泄漏。
	key, _ := got["api_key"].(string)
	if key == "sk-secret-value-0123456789" {
		t.Fatalf("api_key 未脱敏：%q", key)
	}
	if key == "" || key == "***" {
		t.Fatalf("api_key 掩码形态异常：%q", key)
	}

	// 不存在的 id → 404 + {"error":"not found"}（前端据此提示并返回列表）。
	w = doAliasReq(t, a, "GET", upstreamIDPath(999999), nil)
	if w.Code != 404 {
		t.Fatalf("missing id status=%d want 404", w.Code)
	}
	var errBody map[string]any
	json.Unmarshal(w.Body.Bytes(), &errBody)
	if errBody["error"] != "not found" {
		t.Fatalf("error body=%v", errBody)
	}

	// 已删除（软删）的上游同样 404，而不是把软删行吐出来。
	if err := store.DeleteUpstream(d, id); err != nil {
		t.Fatal(err)
	}
	if w := doAliasReq(t, a, "GET", upstreamIDPath(id), nil); w.Code != 404 {
		t.Fatalf("soft-deleted id status=%d want 404", w.Code)
	}
}

// TestDeleteModelByID 覆盖 DELETE /api/admin/upstreams/{id}/models/{mid}。
// 这条端点的 handler 此前是**零调用**：软删语义（is_active=0 而不是物理删）和
// 列表过滤都只由 store 层测试间接保证，HTTP 层没人走过。
func TestDeleteModelByID(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	if err := store.AddModel(d, uid, store.UpstreamModel{
		ModelName: "m1", Manual: true, ContextLength: 1000, MaxOutputLength: 100,
	}); err != nil {
		t.Fatal(err)
	}
	ms, err := store.ListModels(d, uid)
	if err != nil || len(ms) != 1 {
		t.Fatalf("seed models=%v err=%v", ms, err)
	}
	mid := ms[0].ID

	listPath := upstreamIDPath(uid) + "/models"
	w := doAliasReq(t, a, "DELETE", listPath+"/"+strconv.FormatInt(mid, 10), nil)
	if w.Code != 200 {
		t.Fatalf("delete status=%d body=%s", w.Code, w.Body.String())
	}
	var ok map[string]any
	json.Unmarshal(w.Body.Bytes(), &ok)
	if ok["ok"] != true {
		t.Fatalf("delete body=%v", ok)
	}

	// 软删：列表里必须消失。
	w = doAliasReq(t, a, "GET", listPath, nil)
	if w.Code != 200 {
		t.Fatalf("list status=%d", w.Code)
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) != 0 {
		t.Fatalf("软删后仍在列表里：%v", resp.Data)
	}

	// 同名可以重新添加（软删不占用唯一槽位）——这是 partial unique index 的
	// HTTP 层体现，也是删除模型最常见的后续动作。
	if w := doAliasReq(t, a, "POST", listPath, map[string]any{
		"model_name": "m1", "context_length": 2000, "max_output_length": 200,
	}); w.Code != 200 {
		t.Fatalf("re-add after delete status=%d body=%s", w.Code, w.Body.String())
	}
	ms, _ = store.ListModels(d, uid)
	if len(ms) != 1 || ms[0].ContextLength != 2000 {
		t.Fatalf("re-added model=%+v", ms)
	}
}

// TestUpstreamHandlerValidation 把创建/更新/模型写这几条端点上**可达**的 400
// 校验分支一次走完。这些分支此前全部零覆盖，而它们正是前端表单报错的唯一来源：
// 校验一旦失效，非法值会静默落库。
func TestUpstreamHandlerValidation(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	idPath := upstreamIDPath(uid)
	modelsPath := idPath + "/models"

	// 非法 JSON 体（不是合法 JSON 对象）→ 400，而不是 panic 或静默 200。
	t.Run("invalid JSON", func(t *testing.T) {
		for _, path := range []string{"/api/admin/upstreams", idPath, "/api/admin/upstreams/test"} {
			method := "POST"
			if path == idPath {
				method = "PUT"
			}
			w := doRawReq(t, a, method, path, "{not-json")
			if w.Code != 400 {
				t.Fatalf("%s %s: status=%d want 400 body=%s", method, path, w.Code, w.Body.String())
			}
		}
	})

	// 创建：负数 token 限额 → 400（0 才是「不限」）。
	t.Run("create negative limits", func(t *testing.T) {
		w := doAliasReq(t, a, "POST", "/api/admin/upstreams", map[string]any{
			"name": "neg", "base_url": "b", "api_key": "k", "format": "openai",
			"daily_token_limit": -1,
		})
		if w.Code != 400 {
			t.Fatalf("negative daily status=%d want 400 body=%s", w.Code, w.Body.String())
		}
		// 负数被拒后不能留下半成品行。
		list, _ := store.ListUpstreams(d, nil)
		if len(list) != 1 {
			t.Fatalf("被拒的创建留下了行：%v", list)
		}
	})

	// 更新：负数限额 → 400，且原行不变（校验必须发生在写之前）。
	t.Run("update negative limits", func(t *testing.T) {
		w := doAliasReq(t, a, "PUT", idPath, map[string]any{"daily_token_limit": -5})
		if w.Code != 400 {
			t.Fatalf("daily status=%d want 400", w.Code)
		}
		w = doAliasReq(t, a, "PUT", idPath, map[string]any{"monthly_token_limit": -5})
		if w.Code != 400 {
			t.Fatalf("monthly status=%d want 400", w.Code)
		}
		got, err := store.GetUpstreamByID(d, uid)
		if err != nil {
			t.Fatal(err)
		}
		if got.DailyTokenLimit != 0 || got.MonthlyTokenLimit != 0 {
			t.Fatalf("被拒的更新改动了行：daily=%d monthly=%d", got.DailyTokenLimit, got.MonthlyTokenLimit)
		}
	})

	// 新增模型：负长度 → 400，不落库。
	t.Run("add model negative lengths", func(t *testing.T) {
		w := doAliasReq(t, a, "POST", modelsPath, map[string]any{
			"model_name": "bad", "context_length": -1, "max_output_length": 100,
		})
		if w.Code != 400 {
			t.Fatalf("negative context status=%d want 400 body=%s", w.Code, w.Body.String())
		}
		ms, _ := store.ListModels(d, uid)
		if len(ms) != 0 {
			t.Fatalf("被拒的模型写入了库：%+v", ms)
		}
	})

	// 编辑模型：非法 JSON → 400（PUT 全量语义下不能把非法体当成「恢复默认」）。
	t.Run("update model invalid JSON", func(t *testing.T) {
		if err := store.AddModel(d, uid, store.UpstreamModel{
			ModelName: "m1", Manual: true, ContextLength: 1000, MaxOutputLength: 100,
		}); err != nil {
			t.Fatal(err)
		}
		ms, _ := store.ListModels(d, uid)
		w := doRawReq(t, a, "PUT", modelsPath+"/"+strconv.FormatInt(ms[0].ID, 10), "{not-json")
		if w.Code != 400 {
			t.Fatalf("status=%d want 400 body=%s", w.Code, w.Body.String())
		}
		// 原行不得被改动。
		after, _ := store.ListModels(d, uid)
		if len(after) != 1 || after[0].ContextLength != 1000 {
			t.Fatalf("非法请求改动了行：%+v", after)
		}
	})
}

// TestUpstreamByIDNotConfiguredClient 覆盖「client 未配置」这条 500：
// fetch-models 与按 id 测试都需要 upstream client，缺了要明确报错而不是 panic。
// setupAPI 传的就是 nil client，所以这是默认状态。
func TestUpstreamByIDNotConfiguredClient(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "https://api.example.com", APIKey: "k", Format: "openai"})
	idPath := upstreamIDPath(uid)

	for _, path := range []string{idPath + "/fetch-models", idPath + "/test"} {
		w := doAliasReq(t, a, "POST", path, nil)
		if w.Code != 500 {
			t.Fatalf("%s: status=%d want 500 body=%s", path, w.Code, w.Body.String())
		}
		var body map[string]any
		json.Unmarshal(w.Body.Bytes(), &body)
		if body["error"] != "upstream client not configured" {
			t.Fatalf("%s: body=%v", path, body)
		}
	}

	// 不存在的上游优先报 404：client 缺失不该掩盖「这行不存在」。
	if w := doAliasReq(t, a, "POST", upstreamIDPath(999999)+"/fetch-models", nil); w.Code != 404 {
		t.Fatalf("missing upstream status=%d want 404", w.Code)
	}
	if w := doAliasReq(t, a, "POST", upstreamIDPath(999999)+"/test", nil); w.Code != 404 {
		t.Fatalf("missing upstream test status=%d want 404", w.Code)
	}
}
