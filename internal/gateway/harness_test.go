package gateway

import "testing"

func TestDetectHarness(t *testing.T) {
	cases := []struct {
		ua   string
		want string
	}{
		{"claude-code/1.0.71 (external, cli)", "claude-code"},
		{"Claude-Code/2.0", "claude-code"}, // 大小写不敏感
		{"codex/0.10.0 (linux)", "codex"},
		{"aider/v0.40.0", "aider"},
		{"Cursor/1.2.3", "cursor"},
		{"continue/0.9", "continue"},
		{"windsurf/1.0", "windsurf"},
		{"gemini-cli/0.1", "gemini-cli"},
		{"OpenAI/Python 1.52.0", "openai-sdk"},
		{"Anthropic/JS 0.30", "anthropic-sdk"},
		{"python-requests/2.31.0", "python-requests"},
		{"Go-http-client/1.1", "go-http-client"},
		{"curl/8.4.0", "curl"},
		{"some-random-client/9.9", "unknown"},
		{"", "unknown"},

		// DeepSeek Harness（dsh）真实 UA：attribution 模块拼的
		// product/version (+repo url)。
		{"deepseek-harness/0.9.0 (+https://github.com/deepseek-ai/deepseek-harness)", "deepseek-harness"},
		{"DeepSeek-Harness/1.2.3", "deepseek-harness"}, // 大小写不敏感
		{"dsh/0.9.0 (+https://example.com/fork)", "deepseek-harness"},
		// 具体客户端名优先于通用 SDK 字样：即便 UA 里带 "openai"
		// （fork/白牌部署换了产品名），deepseek-harness 也不该被记成 openai-sdk。
		{"deepseek-harness/0.9.0 openai-sdk-wrapper", "deepseek-harness"},
		// 不带连字符的 deepseek 字样不是本产品，保持 unknown。
		{"deepseek/1.0", "unknown"},
	}
	for _, c := range cases {
		if got := detectHarness(c.ua); got != c.want {
			t.Errorf("detectHarness(%q) = %q, want %q", c.ua, got, c.want)
		}
	}
}
