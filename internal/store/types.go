package store

import (
	"fmt"
	"strings"
	"time"
)

// b2i 把布尔列落成 0/1。三种方言的布尔列都是整数存储；这个转换此前在每个写入
// 站点手写一遍「x := 0; if b { x = 1 }」，漏写一处就静默把旧值存进去。
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type Upstream struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Format  string `json:"format"`
	// ExtraEndpoints 该上游支持的其余格式端点（主格式仍是 BaseURL/Format，作兜底）。
	// 网关按入站 endpoint 格式优先命中这里的条目做原生直通（零转译），未命中
	// 走主格式 + IR 转译。空 = 单格式上游（历史行为）。
	ExtraEndpoints []UpstreamEndpoint `json:"extra_endpoints"`
	// Remark 选填备注，纯管理端元数据（展示/导出用），网关路由与转发不读它。
	Remark string `json:"remark"`
	// Tag 上游标记：TagOfficial = 官方源站，TagRelay = 中转站；默认官方，
	// 编辑时可改。与 remark 同地位——纯管理端元数据（列表展示/配置导出），
	// 网关路由、转发、额度与别名解析一律不读它。
	Tag               string `json:"tag"`
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

// UpstreamEndpoint 一个附加格式端点：该上游除主格式外，还支持以 Format 协议
// 在 BaseURL 上接受请求。
type UpstreamEndpoint struct {
	Format  string `json:"format"`
	BaseURL string `json:"base_url"`
}

// ValidateExtraEndpoints 校验附加端点配置：format 必须在已知协议白名单内、
// 列表内不重复、且不得与主格式撞车（同一格式出现两个 URL 会让网关路由产生
// 二义性——撞车配置必须在写入侧拒绝，而不是在读路径上定义隐式优先级）。
func ValidateExtraEndpoints(primaryFormat string, eps []UpstreamEndpoint) error {
	seen := make(map[string]bool, len(eps))
	for i, ep := range eps {
		if ep.Format != "openai" && ep.Format != "anthropic" && ep.Format != "responses" {
			return fmt.Errorf("extra_endpoints[%d]: format must be openai, anthropic or responses", i)
		}
		if strings.TrimSpace(ep.BaseURL) == "" {
			return fmt.Errorf("extra_endpoints[%d]: base_url is required", i)
		}
		if ep.Format == primaryFormat {
			return fmt.Errorf("extra_endpoints[%d]: format %q duplicates the primary format", i, ep.Format)
		}
		if seen[ep.Format] {
			return fmt.Errorf("extra_endpoints[%d]: duplicate format %q", i, ep.Format)
		}
		seen[ep.Format] = true
	}
	return nil
}

// 上游标记的两个取值。TagOfficial 是默认值：没填、显式清空都归一到它。
const (
	TagOfficial = "official"
	TagRelay    = "relay"
)

// NormalizeTag 归一上游标记：裁剪空白后 "" → 默认的 TagOfficial（创建未填、
// 编辑显式清空），两个合法值原样返回，其余响亮拒绝——静默回落会让管理员以为
// 自己存的标记生效了。与 ValidateExtraEndpoints 同地位：写库边界的最后一道关，
// adminapi 提前校验只是体验优化（早 400 比裸 store error 更可操作）。
func NormalizeTag(tag string) (string, error) {
	switch strings.TrimSpace(tag) {
	case "", TagOfficial:
		return TagOfficial, nil
	case TagRelay:
		return TagRelay, nil
	}
	return "", fmt.Errorf("tag must be official or relay")
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
