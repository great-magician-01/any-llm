package adminapi

import (
	"fmt"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
)

// 模型条目的写入校验与归属：addModel 必须拒绝坏 body 与空名（否则会插入
// model_name="" 的行，/v1/models 冒出 "upstream/" 空名模型）；deleteModel
// 必须按路径里的上游 ID 限定归属，不能凭模型 ID 跨上游裸删。

func TestAddModel_RejectsBadBodyAndEmptyName(t *testing.T) {
	a, d := setupAPI(t)
	uid, err := store.CreateUpstream(d, &store.Upstream{
		Name: "u1", BaseURL: "https://api.example.com", APIKey: "sk-1", Format: "openai",
	})
	if err != nil {
		t.Fatal(err)
	}
	modelsPath := fmt.Sprintf("/api/admin/upstreams/%d/models", uid)

	// 坏 JSON → 400（此前解码错误被丢弃，空请求会静默插一个空名模型）
	if w := doRawReq(t, a, "POST", modelsPath, `{`); w.Code != 400 {
		t.Fatalf("bad JSON status=%d want 400", w.Code)
	}
	// 空 body → 400
	if w := doRawReq(t, a, "POST", modelsPath, ``); w.Code != 400 {
		t.Fatalf("empty body status=%d want 400", w.Code)
	}
	// 空模型名（含纯空白）→ 400
	if w := doAliasReq(t, a, "POST", modelsPath, map[string]any{"model_name": "  "}); w.Code != 400 {
		t.Fatalf("blank model_name status=%d want 400", w.Code)
	}

	// 正常添加 → 200；同名再加 → 409（假成功会让管理员以为新配置生效了）
	if w := doAliasReq(t, a, "POST", modelsPath, map[string]any{"model_name": "gpt-x"}); w.Code != 200 {
		t.Fatalf("add status=%d body=%s", w.Code, w.Body.String())
	}
	if w := doAliasReq(t, a, "POST", modelsPath, map[string]any{"model_name": "gpt-x"}); w.Code != 409 {
		t.Fatalf("duplicate add status=%d want 409", w.Code)
	}

	// 上述坏请求一个都不能落库。
	models, err := store.ListModels(d, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ModelName != "gpt-x" {
		t.Fatalf("models=%+v want exactly [gpt-x]", models)
	}
}

func TestDeleteModel_ScopedToPathUpstream(t *testing.T) {
	a, d := setupAPI(t)
	u1, err := store.CreateUpstream(d, &store.Upstream{
		Name: "u1", BaseURL: "https://a.example.com", APIKey: "sk-1", Format: "openai",
	})
	if err != nil {
		t.Fatal(err)
	}
	u2, err := store.CreateUpstream(d, &store.Upstream{
		Name: "u2", BaseURL: "https://b.example.com", APIKey: "sk-2", Format: "openai",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddModel(d, u2, store.UpstreamModel{ModelName: "gpt-x", Manual: true}); err != nil {
		t.Fatal(err)
	}
	models, err := store.ListModels(d, u2)
	if err != nil || len(models) != 1 {
		t.Fatalf("list u2 models: %v %+v", err, models)
	}
	mid := models[0].ID

	// 跨上游：用 u1 的路径删 u2 的模型 → 404，且模型必须还在。
	w := doAliasReq(t, a, "DELETE", fmt.Sprintf("/api/admin/upstreams/%d/models/%d", u1, mid), nil)
	if w.Code != 404 {
		t.Fatalf("cross-upstream delete status=%d want 404", w.Code)
	}
	if models, _ := store.ListModels(d, u2); len(models) != 1 {
		t.Fatal("cross-upstream delete removed another upstream's model")
	}

	// 正确归属 → 200；再删一次（已软删）→ 404。
	w = doAliasReq(t, a, "DELETE", fmt.Sprintf("/api/admin/upstreams/%d/models/%d", u2, mid), nil)
	if w.Code != 200 {
		t.Fatalf("delete status=%d body=%s", w.Code, w.Body.String())
	}
	w = doAliasReq(t, a, "DELETE", fmt.Sprintf("/api/admin/upstreams/%d/models/%d", u2, mid), nil)
	if w.Code != 404 {
		t.Fatalf("re-delete status=%d want 404", w.Code)
	}
}
