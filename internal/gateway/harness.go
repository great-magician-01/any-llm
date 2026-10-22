package gateway

import (
	"regexp"
	"strings"
)

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
//   - 规则表未命中的 UA 再走通用兜底（见 detectHarness）；空 UA 或没有
//     "名称/版本" 形态 token 的 UA 落到 default，返回 "unknown"（原始 UA
//     仍 verbatim 存在 user_agent 列，这里只产出规范名）。
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

// genericHarnessRe 匹配单个 UA token 的 "产品名/版本号" 开头：名称字母
// 开头、[a-z0-9._-] 组成，版本以数字开始（可带 v 前缀）。未知客户端的 UA
// 几乎都是这个形态（ZCode/3.16.2、foo/1.0、foo/v2、a.b.c/2 …），规则表
// 未命中时用它提取产品名，而不是一律记 unknown。
var genericHarnessRe = regexp.MustCompile(`^([a-z][a-z0-9._-]*)/v?[0-9]`)

// maxHarnessLen 是 harness 列在 MySQL 上的宽度（VARCHAR(64)），
// 通用提取的名字一律截断到该长度（提取名只含 ASCII，按字节截断安全）。
const maxHarnessLen = 64

// detectHarness 把 User-Agent 映射为规范的客户端（harness）名称。
// 大小写不敏感，三层判定：
//  1. 规则表子串匹配，特异性从高到低，先中先得；
//  2. 通用兜底：按空白切 token，取第一个整体呈 "名称/版本" 形态的 token
//     的产品名（正则在 token 内锚定，runtime/node.js/24 这种
//     版本位不是数字的复合 token 不会被拆出 node.js）；
//  3. 兜底返回 "unknown"（空 UA、无产品名 token、名称非法）。
// 原始 UA 仍 verbatim 存于 user_agent 列，这里只产出规范名。
func detectHarness(ua string) string {
	u := strings.ToLower(ua)
	for _, r := range harnessRules {
		if strings.Contains(u, r.substr) {
			return r.canonical
		}
	}
	for _, tok := range strings.Fields(u) {
		if m := genericHarnessRe.FindStringSubmatch(tok); m != nil {
			if len(m[1]) > maxHarnessLen {
				return m[1][:maxHarnessLen]
			}
			return m[1]
		}
	}
	return "unknown"
}
