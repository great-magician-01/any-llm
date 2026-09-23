package model

import "time"

// b2i 把布尔列落成 0/1。三种方言的布尔列都是整数存储；这个转换此前在每个写入
// 站点手写一遍「x := 0; if b { x = 1 }」，漏写一处就静默把旧值存进去。
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type Upstream struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	BaseURL           string `json:"base_url"`
	APIKey            string `json:"api_key"`
	Format            string `json:"format"`
	Enabled           bool   `json:"enabled"`
	DailyTokenLimit   int    `json:"daily_token_limit"`
	MonthlyTokenLimit int    `json:"monthly_token_limit"`
	// MaxConcurrent 该上游允许的在途请求并发上限；0 = 不限，新建默认
	// DefaultMaxConcurrent。由网关在 dispatch 前按上游 ID 的信号量强制执行。
	MaxConcurrent int       `json:"max_concurrent"`
	ModelCount    int       `json:"model_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// ExpiresAt 有效期截止时刻；nil = 永久有效。到点后该上游在网关侧等同禁用
	// （直连 404、别名候选跳过、/v1/models 隐藏），续期即恢复——不改 enabled，
	// 两个维度互相独立。
	ExpiresAt *time.Time `json:"expires_at"`
}

// Expired 到点即失效（含等于截止时刻的那一刻）。now 由调用方传入，便于测试边界。
func (u *Upstream) Expired(now time.Time) bool {
	return u.ExpiresAt != nil && !now.Before(*u.ExpiresAt)
}

type UpstreamModel struct {
	ID              int64  `json:"id"`
	UpstreamID      int64  `json:"upstream_id"`
	ModelName       string `json:"model_name"`
	Manual          bool   `json:"manual"`
	ContextLength   int    `json:"context_length"`
	MaxOutputLength int    `json:"max_output_length"`
	// Multimodal 标记该模型是否支持图片等非文本输入；仅管理端配置与展示，
	// 网关不在请求路径上校验它。
	Multimodal bool `json:"multimodal"`
}

type ExtKey struct {
	ID                int64  `json:"id"`
	Key               string `json:"key"`
	Label             string `json:"label"`
	Remark            string `json:"remark"`
	Enabled           bool   `json:"enabled"`
	DailyTokenLimit   int    `json:"daily_token_limit"`
	MonthlyTokenLimit int    `json:"monthly_token_limit"`
	// AllowedModels 限定该 key 可用的对外模型名（别名或 upstream/model）。
	// nil/空 = 不限制。仅精确匹配。
	AllowedModels []string   `json:"allowed_models"`
	CreatedAt     time.Time  `json:"created_at"`
	LastUsedAt    *time.Time `json:"last_used_at"`
}

type UsageRecord struct {
	ID                  int64     `json:"id"`
	ExtKeyID            *int64    `json:"ext_key_id"`
	UpstreamID          *int64    `json:"upstream_id"`
	UpstreamName        string    `json:"upstream_name"`
	Model               string    `json:"model"`
	InFormat            string    `json:"in_format"`
	UpFormat            string    `json:"up_format"`
	PromptTokens        int       `json:"prompt_tokens"`
	CompletionTokens    int       `json:"completion_tokens"`
	TotalTokens         int       `json:"total_tokens"`
	CacheReadTokens     int       `json:"cache_read_tokens"`
	CacheCreationTokens int       `json:"cache_creation_tokens"`
	ReasoningTokens     int       `json:"reasoning_tokens"`
	DurationMs          int64     `json:"duration_ms"`
	Stream              bool      `json:"stream"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"created_at"`
}
