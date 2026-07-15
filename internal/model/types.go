package model

import "time"

type Upstream struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	BaseURL   string    `json:"base_url"`
	APIKey    string    `json:"api_key"`
	Format    string    `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UpstreamModel struct {
	ID         int64  `json:"id"`
	UpstreamID int64  `json:"upstream_id"`
	ModelName  string `json:"model_name"`
	Manual     bool   `json:"manual"`
}

type ExtKey struct {
	ID         int64      `json:"id"`
	Key        string     `json:"key"`
	Label      string     `json:"label"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

type UsageRecord struct {
	ID               int64      `json:"id"`
	ExtKeyID         *int64     `json:"ext_key_id"`
	UpstreamID       *int64     `json:"upstream_id"`
	UpstreamName     string     `json:"upstream_name"`
	Model            string     `json:"model"`
	InFormat         string     `json:"in_format"`
	UpFormat         string     `json:"up_format"`
	PromptTokens     int        `json:"prompt_tokens"`
	CompletionTokens int        `json:"completion_tokens"`
	TotalTokens      int        `json:"total_tokens"`
	Stream           bool       `json:"stream"`
	Status           string     `json:"status"`
	CreatedAt        time.Time  `json:"created_at"`
}
