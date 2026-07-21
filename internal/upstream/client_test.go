package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestCall_NonStreamOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("auth=%s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer srv.Close()

	c := NewClient(http.DefaultClient)
	u := &model.Upstream{Name: "test", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"}
	irReq := &translate.Request{Model: "gpt-4o", Stream: false,
		Messages:  []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}},
		MaxTokens: 100}
	res, err := c.Call(context.Background(), u, irReq, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Response == nil {
		t.Fatal("nil response")
	}
	if res.Response.Content[0].Text != "Hi" {
		t.Fatalf("text=%q", res.Response.Content[0].Text)
	}
	if res.Response.Usage.InputTokens != 10 || res.Response.Usage.OutputTokens != 5 {
		t.Fatalf("usage=%+v", res.Response.Usage)
	}
}

func TestCall_NonStreamAnthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-ant" {
			t.Fatalf("x-api-key=%s", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Fatal("missing anthropic-version")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1","model":"claude","content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":3}}`))
	}))
	defer srv.Close()

	c := NewClient(http.DefaultClient)
	u := &model.Upstream{Name: "test", BaseURL: srv.URL, APIKey: "sk-ant", Format: "anthropic"}
	irReq := &translate.Request{Model: "claude", Stream: false, MaxTokens: 100,
		Messages: []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}}}
	res, err := c.Call(context.Background(), u, irReq, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Response.Content[0].Text != "Hello" {
		t.Fatalf("text=%q", res.Response.Content[0].Text)
	}
	if res.Response.Usage.InputTokens != 8 {
		t.Fatalf("usage=%+v", res.Response.Usage)
	}
	if res.Response.StopReason != "stop" {
		t.Fatalf("stop_reason=%q (should be normalized from end_turn)", res.Response.StopReason)
	}
}

func TestCall_StreamOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		w.Write([]byte("data: {\"id\":\"c1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n"))
		flusher.Flush()
		w.Write([]byte("data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n"))
		flusher.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	c := NewClient(http.DefaultClient)
	u := &model.Upstream{Name: "test", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"}
	irReq := &translate.Request{Model: "gpt-4o", Stream: true, MaxTokens: 100,
		Messages: []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}}}
	res, err := c.Call(context.Background(), u, irReq, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stream == nil {
		t.Fatal("nil stream channel")
	}
	var events []*translate.StreamEvent
	for ev := range res.Stream {
		events = append(events, ev)
	}
	if len(events) == 0 {
		t.Fatal("no events")
	}
	usage := res.Usage()
	if usage.InputTokens != 3 || usage.OutputTokens != 2 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestCall_InjectsStreamOptionsForOpenAI(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	c := NewClient(http.DefaultClient)
	u := &model.Upstream{Name: "test", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"}
	irReq := &translate.Request{Model: "gpt-4o", Stream: true, MaxTokens: 100,
		Messages: []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}}}
	_, err := c.Call(context.Background(), u, irReq, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, "include_usage") {
		t.Fatalf("stream_options not injected: %s", gotBody)
	}
}

func TestCall_ForwardsClientHeaders(t *testing.T) {
	type want struct {
		key, val string
	}
	cases := []struct {
		name    string
		format  string
		client  http.Header
		wantSet []want
	}{
		{
			name:   "openai forwards custom + overrides auth",
			format: "openai",
			client: http.Header{
				"Authorization":   {"Bearer all-sk-clientkey"},
				"Content-Type":    {"text/plain"},
				"Accept-Encoding": {"br"},
				"User-Agent":      {"my-sdk/1.0"},
				"Anthropic-Beta":  {"tools-2024-05-16"},
				"X-Request-Id":    {"abc-123"},
			},
			wantSet: []want{
				{"Authorization", "Bearer sk-test"},
				{"Content-Type", "application/json"},
				{"User-Agent", "my-sdk/1.0"},
				{"Anthropic-Beta", "tools-2024-05-16"},
				{"X-Request-Id", "abc-123"},
				// Accept-Encoding is managed: client's "br" must NOT be
				// forwarded; Go's transport re-adds its default "gzip".
				{"Accept-Encoding", "gzip"},
			},
		},
		{
			name:   "anthropic defaults version when client omits",
			format: "anthropic",
			client: http.Header{
				"X-Api-Key": {"all-sk-clientkey"},
			},
			wantSet: []want{
				{"X-Api-Key", "sk-ant"},
				{"Anthropic-Version", "2023-06-01"},
			},
		},
		{
			name:   "anthropic preserves client version",
			format: "anthropic",
			client: http.Header{
				"Anthropic-Version": {"2024-10-22"},
			},
			wantSet: []want{
				{"X-Api-Key", "sk-ant"},
				{"Anthropic-Version", "2024-10-22"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = cloneHeader(r.Header)
				w.Header().Set("Content-Type", "application/json")
				if tc.format == "anthropic" {
					w.Write([]byte(`{"id":"msg_1","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
				} else {
					w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
				}
			}))
			defer srv.Close()

			c := NewClient(http.DefaultClient)
			apiKey := "sk-test"
			if tc.format == "anthropic" {
				apiKey = "sk-ant"
			}
			u := &model.Upstream{Name: "test", BaseURL: srv.URL, APIKey: apiKey, Format: tc.format}
			irReq := &translate.Request{Model: "m", Stream: false, MaxTokens: 10,
				Messages: []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}}}
			if _, err := c.Call(context.Background(), u, irReq, tc.client); err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.wantSet {
				if got := got.Get(w.key); got != w.val {
					t.Errorf("header %s=%q want %q", w.key, got, w.val)
				}
			}
		})
	}
}

func cloneHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vs := range h {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func TestUpstreamError_Message(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantMsg string
		wantTyp string
	}{
		{
			name:    "openai shape",
			body:    `{"error":{"message":"rate limited","type":"rate_limit_error"}}`,
			wantMsg: "rate limited",
			wantTyp: "rate_limit_error",
		},
		{
			name:    "anthropic shape",
			body:    `{"type":"error","error":{"type":"AuthError","message":"Invalid API key."}}`,
			wantMsg: "Invalid API key.",
			wantTyp: "AuthError",
		},
		{
			name:    "missing message falls back to raw body",
			body:    `{"error":{"type":"oops"}}`,
			wantMsg: `{"error":{"type":"oops"}}`,
			wantTyp: "oops",
		},
		{
			name:    "non-json falls back to raw body",
			body:    `plain text error`,
			wantMsg: `plain text error`,
			wantTyp: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ue := &UpstreamError{StatusCode: 400, Body: []byte(tc.body), Format: "openai"}
			if got := ue.Message(); got != tc.wantMsg {
				t.Fatalf("Message()=%q want %q", got, tc.wantMsg)
			}
			if got := ue.ErrorType(); got != tc.wantTyp {
				t.Fatalf("ErrorType()=%q want %q", got, tc.wantTyp)
			}
		})
	}
}
