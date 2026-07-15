package model

import "time"

type Upstream struct {
	ID        int64
	Name      string
	BaseURL   string
	APIKey    string
	Format    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type UpstreamModel struct {
	ID         int64
	UpstreamID int64
	ModelName  string
	Manual     bool
}

type ExtKey struct {
	ID         int64
	Key        string
	Label      string
	Enabled    bool
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

type UsageRecord struct {
	ID               int64
	ExtKeyID         *int64
	UpstreamID       *int64
	UpstreamName     string
	Model            string
	InFormat         string
	UpFormat         string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Stream           bool
	Status           string
	CreatedAt        time.Time
}
