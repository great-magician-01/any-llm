package gateway

import (
	"strings"
	"testing"
)

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
		{"", "unknown"},

		// DeepSeek Harness（dsh）真实 UA：attribution 模块拼的
		// product/version (+repo url)。
		{"deepseek-harness/0.9.0 (+https://github.com/deepseek-ai/deepseek-harness)", "deepseek-harness"},
		{"DeepSeek-Harness/1.2.3", "deepseek-harness"}, // 大小写不敏感
		{"dsh/0.9.0 (+https://example.com/fork)", "deepseek-harness"},
		// 具体客户端名优先于通用 SDK 字样：即便 UA 里带 "openai"
		// （fork/白牌部署换了产品名），deepseek-harness 也不该被记成 openai-sdk。
		{"deepseek-harness/0.9.0 openai-sdk-wrapper", "deepseek-harness"},
		// 不带连字符的 deepseek 字样不走规则表，通用兜底记为独立名
		// deepseek（而不是 deepseek-harness，也不会被 SDK 字样截走）。
		{"deepseek/1.0", "deepseek"},

		// 通用兜底：规则表未命中时按 "名称/版本" 形态 token 提取产品名。
		{"ZCode/3.16.2 ai-sdk/provider-utils/4.0.27 runtime/node.js/24", "zcode"}, // 真实 ZCode UA
		{"ZCODE/1.0", "zcode"}, // 大小写不敏感
		{"SomeClient/1.2.3 (compat; golang)", "someclient"},
		{"my_agent/0.9", "my_agent"},
		{"a.b.c/2", "a.b.c"},
		{"foo/v2.1", "foo"},
		{"hello zcode/3.1", "zcode"}, // 产品名不在首个 token 也能扫到
		// token 内锚定：runtime/node.js/24 版本位不是数字，不会从其中段
		// 拆出 node.js，继续扫到后面的 zcode/3.16.2 才命中。
		{"runtime/node.js/24 zcode/3.16.2", "zcode"},
		// 通用提取名截断到 harness 列的 MySQL 宽度（64）。
		{strings.Repeat("a", 70) + "/1.0", strings.Repeat("a", 64)},

		// 没有 "名称/版本" 形态 token 的 UA 仍落 unknown。
		{"no version here", "unknown"},
		{"123/4.5", "unknown"}, // 名称必须字母开头
	}
	for _, c := range cases {
		if got := detectHarness(c.ua); got != c.want {
			t.Errorf("detectHarness(%q) = %q, want %q", c.ua, got, c.want)
		}
	}
}
