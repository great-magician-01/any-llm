package upstream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/translate"
	"github.com/great-magician-01/any-llm/internal/translate/anthropic"
	"github.com/great-magician-01/any-llm/internal/translate/openai"
	"github.com/great-magician-01/any-llm/internal/translate/responses"
)

type Client struct {
	http *http.Client
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{http: httpClient}
}

type Result struct {
	Response  *translate.Response
	Stream    <-chan *translate.StreamEvent
	usage     translate.Usage
	usageMux  sync.Mutex
	streamErr error
}

func (r *Result) Usage() translate.Usage {
	r.usageMux.Lock()
	defer r.usageMux.Unlock()
	return r.usage
}

func (r *Result) StreamErr() error { return r.streamErr }

func (r *Result) setUsage(u translate.Usage) {
	r.usageMux.Lock()
	r.usage = u
	r.usageMux.Unlock()
}

func (c *Client) HTTP() *http.Client { return c.http }

func (c *Client) Call(ctx context.Context, u *store.Upstream, irReq *translate.Request, clientHeaders http.Header) (*Result, error) {
	var body []byte
	var err error
	var path, contentType string
	var reqHeaders map[string]string

	switch u.Format {
	case "openai":
		body, err = openai.EncodeRequest(irReq)
		if err != nil {
			return nil, fmt.Errorf("encode openai request: %w", err)
		}
		if irReq.Stream {
			body = injectStreamOptions(body)
		}
		path = "/chat/completions"
		contentType = "application/json"
		reqHeaders = map[string]string{"Authorization": "Bearer " + u.APIKey}
	case "anthropic":
		body, err = anthropic.EncodeRequest(irReq)
		if err != nil {
			return nil, fmt.Errorf("encode anthropic request: %w", err)
		}
		path = "/messages"
		contentType = "application/json"
		reqHeaders = map[string]string{"x-api-key": u.APIKey}
	case "responses":
		body, err = responses.EncodeRequest(irReq)
		if err != nil {
			return nil, fmt.Errorf("encode responses request: %w", err)
		}
		// 上游为 responses 格式时不做 include_usage 注入：usage 随 response.completed 返回
		path = "/responses"
		contentType = "application/json"
		reqHeaders = map[string]string{"Authorization": "Bearer " + u.APIKey}
	default:
		return nil, fmt.Errorf("unknown upstream format: %s", u.Format)
	}

	target := endpointURL(u, path)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	// Forward inbound client headers verbatim so custom metadata (e.g.
	// anthropic-beta, trace ids, user-agent) reaches the upstream. Hop-by-hop
	// headers and those the gateway manages (auth, content-type, content-length,
	// host, accept-encoding) are skipped and set explicitly below, so the
	// upstream's own credentials and transport always take precedence.
	copyForwardableHeaders(httpReq.Header, clientHeaders)
	httpReq.Header.Set("Content-Type", contentType)
	for k, v := range reqHeaders {
		httpReq.Header.Set(k, v)
	}
	if u.Format == "anthropic" && httpReq.Header.Get("anthropic-version") == "" {
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		logger.Error("upstream call failed", "url", target, "err", err)
		return nil, fmt.Errorf("call upstream: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		// 错误 body 也要有读上限：厂商的错误页可能有几 MB，整体读进内存再原样
		// 回给客户端毫无意义（fetch.go / balance.go 同样用 1 MiB 上限 + 512 截断日志）。
		errBody, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamErrorBody))
		if err != nil {
			logger.Warn("upstream error: failed to read error body", "url", target, "status", resp.StatusCode, "err", err)
		}
		logger.Error("upstream returned error",
			"url", target,
			"status", resp.StatusCode,
			"body", truncateLog(string(errBody), logErrorBodyCap),
		)
		return nil, &UpstreamError{StatusCode: resp.StatusCode, Body: errBody, Format: u.Format}
	}

	result := &Result{}

	// 上游无视 stream:true、直接以 JSON 应答（部分兼容层会这样）时，按非流式解码
	// 并填 result.Response，让网关走「完整响应」分支把内容转给客户端。否则流式
	// 解析器会把整个 JSON 当成一行 SSE 丢掉，客户端拿到一个空的 200 流。
	// 判据只认明确的 JSON Content-Type：缺失或非标准头的上游仍走流式解析，不能
	// 因为头不规范就把真正的 SSE 流缓冲成 JSON。
	if irReq.Stream && isJSONContentType(resp.Header.Get("Content-Type")) {
		defer resp.Body.Close()
		// 这条分支把整段响应缓冲进内存（流式路径原本只按行扫描），必须有读
		// 上限，否则一个失控/恶意的兼容上游能用超大 body 把网关内存打爆。
		// 非流式分支是历史口径（完整响应本就要整体读入），不在此收敛。
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxNonStreamJSONBody+1))
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		if len(body) > maxNonStreamJSONBody {
			return nil, fmt.Errorf("non-stream JSON response exceeds %d bytes", maxNonStreamJSONBody)
		}
		irResp, err := decodeResponseByFormat(u.Format, body)
		if err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		result.Response = irResp
		result.setUsage(irResp.Usage)
		return result, nil
	}

	if !irReq.Stream {
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		irResp, err := decodeResponseByFormat(u.Format, respBody)
		if err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		result.Response = irResp
		result.setUsage(irResp.Usage)
		return result, nil
	}

	ch := make(chan *translate.StreamEvent, 64)
	result.Stream = ch
	go c.streamLoop(ctx, resp, u.Format, ch, result)
	return result, nil
}

func (c *Client) streamLoop(ctx context.Context, resp *http.Response, format string, ch chan<- *translate.StreamEvent, result *Result) {
	defer resp.Body.Close()
	defer close(ch)

	var oaiDec *openai.StreamDecoder
	if format == "openai" {
		oaiDec = openai.NewStreamDecoder()
	}
	var rspDec *responses.StreamDecoder
	if format == "responses" {
		rspDec = responses.NewStreamDecoder()
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		// SSE spec: a field line is "name:value" (one optional leading space
		// after the colon is stripped). Accept both "data:foo" and "data: foo".
		// Comment lines (": ...") and other fields (event:, id:, retry:) are
		// skipped — the type is carried inside the data payload for both
		// OpenAI and Anthropic streams.
		if strings.HasPrefix(line, ":") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		field := line[:colon]
		if field != "data" {
			continue
		}
		data := line[colon+1:]
		if strings.HasPrefix(data, " ") {
			data = data[1:]
		}
		if data == "" {
			continue
		}

		switch format {
		case "openai":
			events, err := oaiDec.Decode([]byte(data))
			if err != nil {
				logger.Warn("stream decode error", "format", "openai", "err", err, "data", truncateLog(data, logSSELineCap))
				continue
			}
			for _, ev := range events {
				if ev.Type == "message_delta" {
					u := translate.Usage{
						InputTokens:     ev.InputTokens,
						OutputTokens:    ev.OutputTokens,
						CacheReadTokens: ev.CacheReadTokens,
						ReasoningTokens: ev.ReasoningTokens,
					}
					result.setUsage(u)
				}
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
		case "anthropic":
			logger.FileOnly().Info("raw upstream SSE", "format", "anthropic", "data", truncateLog(data, logSSELineCap))
			ev, err := anthropic.DecodeStreamEvent([]byte(data))
			if err != nil {
				logger.Warn("stream decode error", "format", "anthropic", "err", err, "data", truncateLog(data, logSSELineCap))
				continue
			}
			if ev == nil {
				continue
			}
			if ev.Type == "message_start" {
				result.setUsage(translate.Usage{
					InputTokens:         ev.InputTokens,
					CacheReadTokens:     ev.CacheReadTokens,
					CacheCreationTokens: ev.CacheCreationTokens,
				})
			} else if ev.Type == "message_delta" {
				prev := result.Usage()
				// Anthropic message_delta carries only output_tokens; input_tokens
				// and cache_* arrived in message_start. 先把 message_start 的值回填到
				// 事件上（跨格式编码器要读它们），再用同一份值更新聚合 usage——
				// 直接用事件里的零值覆盖会让 Result.Usage() 丢掉 cache token，
				// 而网关正是用它落 usage_records.cache_read/creation_tokens
				// （客户端从事件算出来的 usage 又是对的，两边会不一致）。
				if ev.InputTokens == 0 {
					ev.InputTokens = prev.InputTokens
				}
				if ev.CacheReadTokens == 0 {
					ev.CacheReadTokens = prev.CacheReadTokens
				}
				if ev.CacheCreationTokens == 0 {
					ev.CacheCreationTokens = prev.CacheCreationTokens
				}
				if ev.ReasoningTokens == 0 {
					ev.ReasoningTokens = prev.ReasoningTokens
				}
				result.setUsage(translate.Usage{
					InputTokens:         ev.InputTokens,
					OutputTokens:        ev.OutputTokens,
					CacheReadTokens:     ev.CacheReadTokens,
					CacheCreationTokens: ev.CacheCreationTokens,
					ReasoningTokens:     ev.ReasoningTokens,
				})
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		case "responses":
			events, err := rspDec.Decode([]byte(data))
			if err != nil {
				logger.Warn("stream decode error", "format", "responses", "err", err, "data", truncateLog(data, logSSELineCap))
				continue
			}
			for _, ev := range events {
				if ev.Type == "message_delta" {
					result.setUsage(translate.Usage{
						InputTokens:     ev.InputTokens,
						OutputTokens:    ev.OutputTokens,
						CacheReadTokens: ev.CacheReadTokens,
						ReasoningTokens: ev.ReasoningTokens,
					})
				}
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		result.streamErr = fmt.Errorf("stream scan error: %w", err)
		logger.Warn("stream scan error", "format", format, "err", err)
	}
}

// endpointURL joins an upstream's base URL with an endpoint path. Anthropic
// upstreams follow the SDK convention that the base URL excludes the version
// prefix (e.g. https://api.anthropic.com), so "/v1" is inserted when the base
// URL's path has no v1 segment; a base URL that already has one (ending in
// /v1, or containing a /v1/ segment on a versioned proxy path) is left as-is.
// openai/responses base URLs are expected to include the version prefix
// themselves, matching the OpenAI SDK convention.
func endpointURL(u *store.Upstream, path string) string {
	base := strings.TrimRight(u.BaseURL, "/")
	if u.Format == "anthropic" && !pathHasV1Segment(base) {
		base += "/v1"
	}
	return base + path
}

func pathHasV1Segment(rawurl string) bool {
	p := rawurl
	if parsed, err := url.Parse(rawurl); err == nil {
		p = parsed.Path
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "v1" {
			return true
		}
	}
	return false
}

func injectStreamOptions(body []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		logger.Warn("injectStreamOptions: JSON unmarshal failed, sending original body", "err", err)
		return body
	}
	m["stream_options"] = map[string]any{"include_usage": true}
	out, err := json.Marshal(m)
	if err != nil {
		logger.Warn("injectStreamOptions: JSON marshal failed, sending original body", "err", err)
		return body
	}
	return out
}

type UpstreamError struct {
	StatusCode int
	Body       []byte
	Format     string
}

// Error 会进日志（handler 的 candidate call failed 等），body 上限 1 MiB，
// 与日志侧同口径截断；完整 body 仍在 Body 字段里供 Message() 提取。
func (e *UpstreamError) Error() string {
	return fmt.Sprintf("upstream returned %d: %s", e.StatusCode, truncateLog(string(e.Body), logErrorBodyCap))
}

// Message extracts a human-readable error message from the upstream response
// body. Handles both OpenAI ({"error":{"message":"..."}}) and Anthropic
// ({"type":"error","error":{"message":"..."}}) error shapes. Falls back to the
// raw body when parsing fails or the message is empty.
func (e *UpstreamError) Message() string {
	if msg := e.parseError().Message; msg != "" {
		return msg
	}
	return string(e.Body)
}

// ErrorType extracts the upstream error type string if present.
func (e *UpstreamError) ErrorType() string {
	return e.parseError().Type
}

func (e *UpstreamError) parseError() struct {
	Message string `json:"message"`
	Type    string `json:"type"`
} {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(e.Body, &parsed)
	return parsed.Error
}

// maxUpstreamErrorBody 限制上游错误响应体的读取上限（1 MiB，与 fetch.go /
// balance.go 一致）。错误页动辄几 MB，无上限读进内存再回给客户端没有任何收益。
const maxUpstreamErrorBody = 1 << 20

// maxNonStreamJSONBody 限制「流式请求被上游以非流式 JSON 应答」时整段响应的
// 读取上限（8 MiB）。这条路径把完整响应缓冲进内存：取 8 MiB 是因为 IR 不建模
// 的超大载荷（如内联 base64 图片）本来也活不过翻译层，而长文本完成体
// （百万 token 级）远低于这个量级。
const maxNonStreamJSONBody = 8 << 20

// isJSONContentType 报告上游是否明确以 JSON 应答（application/json、
// application/problem+json 等）。只用于「流式请求被非流式应答」的识别。
func isJSONContentType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "application/json") || strings.Contains(ct, "+json")
}

// decodeResponseByFormat 按上游格式解码完整（非流式）响应体。
// Call 开头的 format switch 已拦未知格式，这里的 default 是防御：库里若混进
// 未知 format 的行，必须响亮报错而不是返回 nil 响应。
func decodeResponseByFormat(format string, body []byte) (*translate.Response, error) {
	switch format {
	case "openai":
		return openai.DecodeResponse(body)
	case "anthropic":
		return anthropic.DecodeResponse(body)
	case "responses":
		return responses.DecodeResponse(body)
	}
	return nil, fmt.Errorf("unknown upstream format: %s", format)
}

// 日志/错误文案里的上游内容截断长度：错误 body 512、调试级原始 body 1024、
// 单条 SSE 数据行 256。正常行远小于此，截断只为兜住异常或恶意上游。
const (
	logErrorBodyCap = 512
	logDebugBodyCap = 1024
	logSSELineCap   = 256
)

// truncateLog 把 s 截到最多 n 字节并加截断标记。按 rune 边界截断：上游
// body/SSE 行里中文与 emoji 常见，按字节切会往日志写入非法 UTF-8，采集端会报错。
func truncateLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...(truncated)"
}

// copyForwardableHeaders copies inbound client request headers onto the
// upstream request, skipping hop-by-hop headers and those the gateway manages
// (auth, content-type, content-length, host, accept-encoding). Managed
// headers are set by Call afterwards, so they always override any
// client-supplied value. All values of a repeated header are preserved.
func copyForwardableHeaders(dst, src http.Header) {
	for k, vs := range src {
		if isManagedHeader(k) {
			continue
		}
		dst[k] = append([]string(nil), vs...)
	}
}

// isManagedHeader reports whether key is a hop-by-hop header or one the
// gateway sets explicitly on the upstream request, and therefore must not be
// forwarded from the client.
func isManagedHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Authorization", "X-Api-Key", "Content-Type", "Content-Length", "Host",
		"Accept-Encoding",
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailers", "Transfer-Encoding", "Upgrade":
		return true
	}
	return false
}
