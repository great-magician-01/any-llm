package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/store"
)

// testTimeout 连通性测试的整体超时。上游 HTTP 客户端本身不设超时（流式调用
// 不能设），管理端的交互式测试必须自己兜底，否则一个挂死的上游会把测试请求
// 一直拖住。
const testTimeout = 15 * time.Second

// TestResult 一次连通性测试的结果，语义分三层：
//   - Reachable=false：网络层失败（DNS/连接被拒/超时），没收到任何 HTTP 响应；
//   - Reachable=true 且 OK=false：上游应答了但不符合预期——401/403 多为 key 无效，
//     其它状态（如 404）多为该端点不提供模型列表（部分 anthropic 兼容端点只有
//     /messages），此时连通本身是正常的，Detail/Status 给出细节；
//   - OK=true：/models 返回 2xx，连通与凭据都正常，Models 为模型数。
type TestResult struct {
	OK        bool   `json:"ok"`
	Reachable bool   `json:"reachable"`
	LatencyMs int64  `json:"latency_ms"`
	Status    int    `json:"status,omitempty"`
	Models    *int   `json:"models,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// TestConnectivity 轻量探测上游：GET {base}/models（与 FetchModels 同一端点，
// 不发起对话、不消耗额度）。永远返回非 nil——网络错误也体现在结果里而不是
// 作为 Go error，调用方（adminapi）原样回 JSON 即可。
func TestConnectivity(ctx context.Context, httpClient *http.Client, u *store.Upstream) *TestResult {
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	start := time.Now()
	req, err := newAuthedModelsRequest(ctx, u)
	if err != nil {
		return &TestResult{Detail: err.Error()}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Warn("connectivity test: request failed", "url", req.URL.String(), "upstream", u.Name, "err", err)
		return &TestResult{LatencyMs: time.Since(start).Milliseconds(), Detail: err.Error()}
	}
	defer resp.Body.Close()

	res := &TestResult{Reachable: true, LatencyMs: time.Since(start).Milliseconds(), Status: resp.StatusCode}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxFetchBody))
		res.Detail = truncateFetch(strings.TrimSpace(string(body)), 512)
		logger.Warn("connectivity test: upstream non-2xx",
			"url", req.URL.String(), "upstream", u.Name, "status", resp.StatusCode, "body", res.Detail)
		return res
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// 2xx 但应答不是模型列表（如网关类上游返回 HTML）：连通没问题，
		// 按异常说明返回，由前端按 warning 展示。
		res.Detail = "2xx response is not a models list"
		return res
	}
	n := len(result.Data)
	res.OK = true
	res.Models = &n
	return res
}
