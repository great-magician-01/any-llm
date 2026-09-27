package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// TestCompletion_StreamRecordsAnthropicCacheTokens 是 usage 落库口径的端到端
// 回归位：Anthropic 上游把 cache_read/cache_creation 放在 message_start，
// message_delta 只带 output_tokens。网关落 usage_records 用的是
// upstream.Result.Usage()，一旦 message_delta 的零值把 message_start 的值覆盖掉，
// 缓存命中的 token 就会静默记成 0——而客户端从 SSE 事件里算出来的 usage 又是对的，
// 两边不一致，账单/统计口径失真且很难发现。
func TestCompletion_StreamRecordsAnthropicCacheTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		write := func(s string) {
			_, _ = w.Write([]byte(s))
			f.Flush()
		}
		write("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-3-5\",\"content\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":1,\"cache_read_input_tokens\":40,\"cache_creation_input_tokens\":7}}}\n\n")
		write("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		write("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n")
		write("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		write("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":25}}\n\n")
		write("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "ant", BaseURL: srv.URL, APIKey: "sk-ant", Format: "anthropic"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "claude-3-5"})
	k, _ := store.CreateExtKey(d, "cache-tokens", "", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"ant/claude-3-5","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	records, _, err := store.UsageRecordsList(d, 1, 10)
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("usage records=%d want 1", len(records))
	}
	r := records[0]
	if r.PromptTokens != 100 || r.CompletionTokens != 25 {
		t.Errorf("tokens = %d/%d want 100/25", r.PromptTokens, r.CompletionTokens)
	}
	if r.CacheReadTokens != 40 {
		t.Errorf("cache_read_tokens = %d want 40（message_delta 的零值不能覆盖 message_start 的缓存命中）", r.CacheReadTokens)
	}
	if r.CacheCreationTokens != 7 {
		t.Errorf("cache_creation_tokens = %d want 7", r.CacheCreationTokens)
	}
	if r.Status != "ok" {
		t.Errorf("status = %q want ok", r.Status)
	}
}
