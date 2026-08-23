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
	}
	for _, c := range cases {
		if got := detectHarness(c.ua); got != c.want {
			t.Errorf("detectHarness(%q) = %q, want %q", c.ua, got, c.want)
		}
	}
}
