package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
)

func FetchModels(ctx context.Context, httpClient *http.Client, u *model.Upstream) ([]string, error) {
	// Anthropic's models endpoint is /v1/models; endpointURL inserts the /v1
	// when the base URL doesn't already carry it. Some providers (e.g.
	// DeepSeek) do not expose a models listing on their anthropic-compat
	// path; fetch will simply 404 there.
	url := endpointURL(u, "/models")
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		logger.Error("fetch models: create request", "url", url, "err", err)
		return nil, fmt.Errorf("create fetch request: %w", err)
	}
	switch u.Format {
	case "openai", "responses":
		req.Header.Set("Authorization", "Bearer "+u.APIKey)
	case "anthropic":
		req.Header.Set("x-api-key", u.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		return nil, fmt.Errorf("unknown format: %s", u.Format)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Error("fetch models: request failed", "url", url, "upstream", u.Name, "err", err)
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		logger.Error("fetch models: upstream error",
			"url", url,
			"upstream", u.Name,
			"status", resp.StatusCode,
			"body", truncateFetch(string(body), 512),
		)
		return nil, fmt.Errorf("upstream %d: %s", resp.StatusCode, string(body))
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logger.Error("fetch models: decode failed", "url", url, "upstream", u.Name, "err", err)
		return nil, fmt.Errorf("decode models response: %w", err)
	}
	out := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		out = append(out, m.ID)
	}
	logger.Info("fetched models", "upstream", u.Name, "count", len(out))
	return out, nil
}

func truncateFetch(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
