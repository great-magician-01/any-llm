package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/model"
)

func FetchModels(ctx context.Context, httpClient *http.Client, u *model.Upstream) ([]string, error) {
	url := strings.TrimRight(u.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create fetch request: %w", err)
	}
	switch u.Format {
	case "openai":
		req.Header.Set("Authorization", "Bearer "+u.APIKey)
	case "anthropic":
		req.Header.Set("x-api-key", u.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		return nil, fmt.Errorf("unknown format: %s", u.Format)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upstream %d: %s", resp.StatusCode, string(body))
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}
	out := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		out = append(out, m.ID)
	}
	return out, nil
}
