# 通用 harness 兜底:未知 UA 按 `名称/版本` 通用特征自动提取

## 行为设计(三层,前两层之间保持现有优先级)

`detectHarness` 改为:

1. **规则表(不动)**:现有 16 条 `{substr, canonical}` 规则原样保留、原顺序优先——已知客户端与别名归一(claude-cli→claude-code 等)不受影响。
2. **通用正则兜底(新增)**:表未命中时,把 UA 小写化后按空白切成 token,**取第一个匹配 `^([a-z][a-z0-9._-]*)/v?[0-9]` 的 token**,提取名称部分作为 harness 名。即"产品名/版本号"形态:`ZCode/3.16.2 ai-sdk/provider-utils/4.0.27 runtime/node.js/24` → `zcode`。
   - **按 token 锚定匹配**而非对整串 FindString:避免从 `runtime/node.js/24` 这种 token 的中段误提取出 `node.js`(`runtime/` 后面不是版本数字,该 token 整体不匹配)。
   - 名称字符集收紧为 `[a-z0-9._-]` 且字母开头;`123/4.5`、无版本形态的垃圾 UA 仍落 `unknown`。
   - **截断到 64 字节**(harness 列在 MySQL 上是 VARCHAR(64),PG/SQLite 是 TEXT;提取名都是 ASCII,直接按字节截断安全)。
3. **兜底 `unknown`**:空 UA 或无任何 `名称/版本` token 时不变。

## 已知取舍(如实记录,不解决)

- 浏览器形态 `Mozilla/5.0 (...)` 会提取为 `mozilla`——可接受,归档只发生在 `/v1` 补全请求,且比 unknown 信息量大。
- 表的子串优先级保持现状:未知客户端 UA 里若恰好含 `openai`/`curl` 等字样仍会被表先截走(现有既有行为,通用兜底不改变它)。

## 改动文件

1. **`internal/gateway/harness.go`**:包级 `regexp.MustCompile`(调用点在 dispatch 同步路径,编译一次);`detectHarness` 表循环后加 token 扫描;注释同步更新三层逻辑。
2. **`internal/gateway/harness_test.go`**(保持现有扁平表风格):
   - 动机用例:ZCode 真实 UA → `zcode`;大小写不敏感 `ZCODE/1.0` → `zcode`。
   - 通用形态:`SomeClient/1.2.3 (compat)`、`my_agent/0.9`、`a.b.c/2`、`foo/v2.1` → 各自名称;`hello zcode/3.1` → `zcode`(非首 token 也能扫到);`runtime/node.js/24 zcode/3.16.2` → `zcode`(锚定验证)。
   - 截断:70 字符名称 → 恰好 64。
   - 负例保持 `unknown`:空 UA、`no version here`、`123/4.5`。
   - **修改两个既有断言(行为翻转,属预期)**:`deepseek/1.0` → `deepseek`、`some-random-client/9.9` → `some-random-client`——这正是本次要自动识别的形态。
3. **`AGENTS.md:99`**:detectHarness 段落补一句两层机制("规则表未命中时,按通用 `name/version` 首 token 提取小写产品名(≤64 字符),仍无则 unknown")。

## 流程与验证

- 按仓库规范走 **`feat/harness-generic-fallback`** 分支,提交后 push 并用 gh 开 PR(merge 由你决定)。
- 验证:`gofmt -l`、`go vet ./...`、`go test ./internal/gateway -run TestDetectHarness -v`、`go test ./...`(cmd/any-llm 需要 web/dist 桩,缺失则先建桩文件)。
- 不做历史数据回填(如需,可后续用 `UPDATE ... WHERE harness='unknown' AND user_agent ~ 正则` 单独处理,PR 描述里附上思路)。