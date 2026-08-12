package gateway

import "strings"

// detectHarness 把 User-Agent 映射为规范的客户端（harness）名称。
// 大小写不敏感；按特异性从高到低匹配，先中先得。未知或空 UA 返回 "unknown"。
// 原始 UA 仍 verbatim 存于 user_agent 列，这里只产出规范名。
func detectHarness(ua string) string {
	u := strings.ToLower(ua)
	switch {
	case strings.Contains(u, "claude-code"), strings.Contains(u, "claude-cli"):
		// Claude Code 真实 UA 是 "claude-cli/x.y.z (external, cli)"，
		// 与文档/测试里常见的 "claude-code" 写法并列匹配，统一归为
		// 规范名 claude-code。
		return "claude-code"
	case strings.Contains(u, "codex"):
		return "codex"
	case strings.Contains(u, "aider"):
		return "aider"
	case strings.Contains(u, "cursor"):
		return "cursor"
	case strings.Contains(u, "continue"):
		return "continue"
	case strings.Contains(u, "windsurf"):
		return "windsurf"
	case strings.Contains(u, "gemini-cli"):
		return "gemini-cli"
	case strings.Contains(u, "openai"):
		return "openai-sdk"
	case strings.Contains(u, "anthropic"):
		return "anthropic-sdk"
	case strings.Contains(u, "python-requests"):
		return "python-requests"
	case strings.Contains(u, "go-http-client"):
		return "go-http-client"
	case strings.Contains(u, "curl"):
		return "curl"
	default:
		return "unknown"
	}
}
