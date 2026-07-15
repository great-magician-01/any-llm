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
	Response *translate.Response
	Stream   <-chan *translate.StreamEvent
	usage    translate.Usage
	usageMux sync.Mutex
}

func (r *Result) Usage() translate.Usage {
	r.usageMux.Lock()
	defer r.usageMux.Unlock()
	return r.usage
}

func (r *Result) setUsage(u translate.Usage) {
	r.usageMux.Lock()
	r.usage = u
	r.usageMux.Unlock()
}

func (c *Client) HTTP() *http.Client { return c.http }

func (c *Client) Call(ctx context.Context, u *model.Upstream, irReq *translate.Request) (*Result, error) {
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
		reqHeaders = map[string]string{
			"x-api-key":         u.APIKey,
			"anthropic-version": "2023-06-01",
		}
	default:
		return nil, fmt.Errorf("unknown upstream format: %s", u.Format)
	}

	url := strings.TrimRight(u.BaseURL, "/") + path
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	for k, v := range reqHeaders {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call upstream: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
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
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}

		switch format {
		case "openai":
			events, err := oaiDec.Decode([]byte(data))
			if err != nil {
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
			ev, err := anthropic.DecodeStreamEvent([]byte(data))
			if err != nil || ev == nil {
				continue
			}
			if ev.Type == "message_start" {
				result.setUsage(translate.Usage{InputTokens: ev.InputTokens})
			} else if ev.Type == "message_delta" {
				prev := result.Usage()
				result.setUsage(translate.Usage{InputTokens: prev.InputTokens, OutputTokens: ev.OutputTokens})
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}
}

func injectStreamOptions(body []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	m["stream_options"] = map[string]any{"include_usage": true}
	out, err := json.Marshal(m)
	if err != nil {
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
