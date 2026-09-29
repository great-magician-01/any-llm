package gateway

import "strings"

// harnessRule 是一条 UA 匹配规则：UA（小写化后）出现 substr 即判为
// canonical 这个规范客户端名。
type harnessRule struct {
	substr    string
	canonical string
}

// harnessRules 按特异性从高到低排列，先中先得：
//   - 具体客户端名（claude-code / codex / …）必须排在 "openai"/"anthropic"
//     这两个通用 SDK 字样之前——很多客户端 UA 里会带上它底层用的 SDK，
//     先匹配到具体客户端才不会把它误记成 SDK 流量。
//   - "claude-code" 与 "claude-cli" 并列：Claude Code 真实 UA 是
//     "claude-cli/x.y.z (external, cli)"，而文档/Issue 里常写成
//     "claude-code"，两种写法统一归为规范名 claude-code。
//   - DeepSeek Harness（dsh）的 UA 由 packages/llm/llm 的 attribution
//     模块统一拼装：产品名 + 版本 + 仓库注释，形如
//     "deepseek-harness/1.2.3 (+https://github.com/deepseek-ai/deepseek-harness)"，
//     所以匹配产品名即可；白牌/fork 部署可能换成短名 "dsh/…"，一并归一。
//   - 未知或空 UA 落到 default，返回 "unknown"（原始 UA 仍 verbatim 存在
//     user_agent 列，这里只产出规范名）。
var harnessRules = []harnessRule{
	{"claude-code", "claude-code"},
	{"claude-cli", "claude-code"},
	{"deepseek-harness", "deepseek-harness"},
	{"dsh/", "deepseek-harness"},
	{"codex", "codex"},
	{"aider", "aider"},
	{"cursor", "cursor"},
	{"continue", "continue"},
	{"windsurf", "windsurf"},
	{"gemini-cli", "gemini-cli"},
	{"openai", "openai-sdk"},
	{"anthropic", "anthropic-sdk"},
	{"python-requests", "python-requests"},
	{"go-http-client", "go-http-client"},
	{"curl", "curl"},
}

// detectHarness 把 User-Agent 映射为规范的客户端（harness）名称。
// 大小写不敏感；按特异性从高到低匹配，先中先得。未知或空 UA 返回 "unknown"。
// 原始 UA 仍 verbatim 存于 user_agent 列，这里只产出规范名。
func detectHarness(ua string) string {
	u := strings.ToLower(ua)
	for _, r := range harnessRules {
		if strings.Contains(u, r.substr) {
			return r.canonical
		}
	}
	return "unknown"
}
