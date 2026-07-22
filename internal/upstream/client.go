package upstream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/translate"
	"github.com/great-magician-01/any-llm/internal/translate/anthropic"
	"github.com/great-magician-01/any-llm/internal/translate/openai"
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

func (c *Client) Call(ctx context.Context, u *model.Upstream, irReq *translate.Request, clientHeaders http.Header) (*Result, error) {
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
	default:
		return nil, fmt.Errorf("unknown upstream format: %s", u.Format)
	}

	url := strings.TrimRight(u.BaseURL, "/") + path
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
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
		logger.Error("upstream call failed", "url", url, "err", err)
		return nil, fmt.Errorf("call upstream: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		errBody, err := io.ReadAll(resp.Body)
		if err != nil {
			logger.Warn("upstream error: failed to read error body", "url", url, "status", resp.StatusCode, "err", err)
		}
		logger.Error("upstream returned error",
			"url", url,
			"status", resp.StatusCode,
			"body", truncateUpstream(string(errBody), 512),
		)
		return nil, &UpstreamError{StatusCode: resp.StatusCode, Body: errBody, Format: u.Format}
	}

	result := &Result{}

	if !irReq.Stream {
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		var irResp *translate.Response
		switch u.Format {
		case "openai":
			irResp, err = openai.DecodeResponse(respBody)
		case "anthropic":
			irResp, err = anthropic.DecodeResponse(respBody)
		}
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
				logger.Warn("stream decode error", "format", "openai", "err", err, "data", truncateUpstream(data, 256))
				continue
			}
			for _, ev := range events {
				if ev.Type == "message_delta" {
					u := translate.Usage{InputTokens: ev.InputTokens, OutputTokens: ev.OutputTokens}
					result.setUsage(u)
				}
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
		case "anthropic":
			logger.FileOnly().Info("raw upstream SSE", "format", "anthropic", "data", truncateUpstream(data, 256))
			ev, err := anthropic.DecodeStreamEvent([]byte(data))
			if err != nil {
				logger.Warn("stream decode error", "format", "anthropic", "err", err, "data", truncateUpstream(data, 256))
				continue
			}
			if ev == nil {
				continue
			}
			if ev.Type == "message_start" {
				result.setUsage(translate.Usage{InputTokens: ev.InputTokens})
			} else if ev.Type == "message_delta" {
				prev := result.Usage()
				result.setUsage(translate.Usage{InputTokens: prev.InputTokens, OutputTokens: ev.OutputTokens})
				// Anthropic message_delta carries only output_tokens; input_tokens
				// arrived in message_start. Propagate them onto the event so
				// cross-format encoders (e.g. OpenAI usage chunk) see both values.
				if ev.InputTokens == 0 {
					ev.InputTokens = prev.InputTokens
				}
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		result.streamErr = fmt.Errorf("stream scan error: %w", err)
		logger.Warn("stream scan error", "format", format, "err", err)
	}
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

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("upstream returned %d: %s", e.StatusCode, string(e.Body))
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

func truncateUpstream(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
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
