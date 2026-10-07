package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// ---------------------------------------------------------------------------
// 8. 配额窗口：日窗是「今天」，月窗是「本自然月」——两件事
// ---------------------------------------------------------------------------

// qwOKUpstream 回一个固定 OpenAI 补全的假上游；先读完请求体再应答，避免服务端
// 带着未读请求体关连接发 RST、把响应截断成解码错误。
func qwOKUpstream(t *testing.T, text string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"` + text + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 业务规则（router.go checkKeyLimits）：日配额按服务端本地日的 [今天 00:00,
// 明天 00:00) 聚合，月配额按 [本月 1 日 00:00, 下月 1 日 00:00) 聚合，两者
// 是相互独立的两把尺子。
//
// 现有 router_test.go 的日/月限额用例插入的 usage 都落在「今天」，而今天同时
// 属于两个窗口——把日窗和月窗的实现互换（甚至把两者都写成「今天」或都写成
// 「本月」）那些用例照样通过。这里用不同时间戳钉死窗口边界：
//
//	插在昨天 → 不计入日窗（今天可用），但计入月窗；
//	插在本月 1 日 → 不计入日窗（除非今天就是 1 日），但计入月窗；
//	插在上个月 → 两个窗口都不计入。
//
// 第 4 条是专门用来抓「月窗退化成日窗」的：它的时间戳在本月内、今天之外。
func TestQuotaWindowsDistinguishCalendarDayFromMonth(t *testing.T) {
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	yesterday := now.AddDate(0, 0, -1)
	lastMonthEnd := monthStart.Add(-time.Hour) // 上月最后一天 23:00

	cases := []struct {
		name    string
		daily   int
		monthly int
		at      time.Time
		want    int
		wantMsg string // 非空：429 的错误文案必须点名是哪把尺子
	}{
		{"today counts against the daily quota", 100, 0, now, 429, "daily token limit exceeded for API key"},
		{"yesterday does not count against the daily quota", 100, 0, yesterday, 200, ""},
		{"this month counts against the monthly quota", 0, 100, now, 429, "monthly token limit exceeded for API key"},
		{"the monthly window is the calendar month, not today", 0, 100, monthStart, 429, "monthly token limit exceeded for API key"},
		{"last month does not count against the monthly quota", 0, 100, lastMonthEnd, 200, ""},
	}

	fake := qwOKUpstream(t, "quota-ok")
	g, d := setupGateway(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: fake.URL, APIKey: "sk", Format: "openai"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})
	g.client = upstream.NewClient(http.DefaultClient)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 每个用例一把独立 key（标签唯一），避免用量互相串
			k, err := store.CreateExtKey(d, tc.name, "", tc.daily, tc.monthly, nil)
			if err != nil {
				t.Fatal(err)
			}
			// 直接写 usage_records，created_at 就是被测的时间戳
			if err := store.InsertUsage(d, &store.UsageRecord{
				ExtKeyID: &k.ID, UpstreamID: &uid, UpstreamName: "oai", Model: "m",
				InFormat: "openai", UpFormat: "openai", TotalTokens: 100, Status: "ok",
				CreatedAt: tc.at,
			}); err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest("POST", "/v1/chat/completions",
				strings.NewReader(`{"model":"oai/m","messages":[{"role":"user","content":"hi"}]}`))
			req.Header.Set("Authorization", "Bearer "+k.Key)
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)

			if w.Code != tc.want {
				t.Fatalf("usage at %s (daily=%d monthly=%d): status=%d want %d, body=%s",
					tc.at.Format(time.RFC3339), tc.daily, tc.monthly, w.Code, tc.want, w.Body.String())
			}
			if tc.wantMsg != "" {
				if !strings.Contains(w.Body.String(), tc.wantMsg) {
					t.Fatalf("429 must name the exceeded window %q, body=%s", tc.wantMsg, w.Body.String())
				}
				if !strings.Contains(w.Body.String(), "rate_limit_error") {
					t.Fatalf("429 error type missing: %s", w.Body.String())
				}
			} else if !strings.Contains(w.Body.String(), "quota-ok") {
				t.Fatalf("200 but the request never reached the upstream: %s", w.Body.String())
			}
		})
	}
}
