/**
 * Claude Code（Linux）配置生成器：shell 环境变量段，变量集对齐 cc-switch。
 *
 * 与 Keys.vue 的 opencode / OMP / dsh 复制功能配套：生成指向本网关
 * （any-llm）的 Claude Code 接入配置，模型 id 为 upstream-name/model-name
 * （与 /v1/models 一致），ext key 明文嵌入 ANTHROPIC_AUTH_TOKEN。
 *
 * 导出物是可整段追加到 ~/.bashrc / ~/.zshrc 的 export 段（source 后启动
 * claude 即用）。变量集与输出顺序对齐 cc-switch 的 Claude Code 配置：
 * - ANTHROPIC_AUTH_TOKEN：ext key 明文（Claude Code 以 Authorization: Bearer
 *   发送，网关同时认 x-api-key）；
 * - ANTHROPIC_BASE_URL：网关根地址（不带 /v1，Claude Code 自拼 /v1/messages）；
 * - 模型槽位（官方变量，见 model-config 文档）：
 *   ANTHROPIC_MODEL（主会话）、ANTHROPIC_DEFAULT_{FABLE,OPUS,SONNET,HAIKU}_MODEL
 *   （别名解析目标，haiku 兼作标题/摘要等后台任务模型）、
 *   CLAUDE_CODE_SUBAGENT_MODEL（子代理默认模型）；
 * - 四个 DEFAULT_* 槽位各带一个 *_MODEL_NAME（/model 选择器里的显示名），
 *   取裸模型 id（与 cc-switch 输出一致）；
 * - 槽位可勾选 [1M] 后缀：1M 上下文变体标记，仅客户端侧生效（Claude Code
 *   读取 id 时会剥离该后缀并按 1M 窗口处理，发给网关的仍是裸模型 id）。
 */

/** 一个模型槽位的选择：模型 id + 是否带 1M 上下文后缀 */
export interface ClaudeCodeSlot {
  /** 模型 id（upstream-name/model-name 或别名） */
  model: string
  /** 是否附加 [1M] 后缀（声明 1M 上下文窗口，仅客户端侧生效） */
  oneM?: boolean
}

/** 各槽位 → 网关模型的映射；未设置的槽位不导出对应变量 */
export interface ClaudeCodeMapping {
  /** 主会话模型 → ANTHROPIC_MODEL */
  main?: ClaudeCodeSlot
  /** fable 别名 → ANTHROPIC_DEFAULT_FABLE_MODEL(+_NAME) */
  fable?: ClaudeCodeSlot
  /** opus 别名（兼 opusplan 计划阶段）→ ANTHROPIC_DEFAULT_OPUS_MODEL(+_NAME) */
  opus?: ClaudeCodeSlot
  /** sonnet 别名（兼 opusplan 执行阶段）→ ANTHROPIC_DEFAULT_SONNET_MODEL(+_NAME) */
  sonnet?: ClaudeCodeSlot
  /** haiku 别名（兼后台任务小模型）→ ANTHROPIC_DEFAULT_HAIKU_MODEL(+_NAME) */
  haiku?: ClaudeCodeSlot
  /** 子代理默认模型 → CLAUDE_CODE_SUBAGENT_MODEL */
  subagent?: ClaudeCodeSlot
}

export interface ClaudeCodeConfigOptions {
  /** 网关根地址，如 http://localhost:6718（不含 /v1） */
  baseUrl: string
  /** ext key 明文（ANTHROPIC_AUTH_TOKEN 的值） */
  apiKey: string
  mapping: ClaudeCodeMapping
  /** 导出 CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1（/model 从网关 /v1/models 拉取可选模型） */
  gatewayModelDiscovery?: boolean
  /** 导出 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1（关闭遥测/自动更新等非网关流量） */
  disableNonessentialTraffic?: boolean
}

const HEADER = `# ============================================================
# Claude Code 配置（Linux）— 由 any-llm 生成（指向本网关）
# 用法：整段追加到 ~/.bashrc（或 ~/.zshrc），然后执行
#   source ~/.bashrc
# 再正常启动 claude 即可。切换模型：会话内用 /model。
# ============================================================
`

/** POSIX shell 单引号引用：任意值都安全（值内的 ' 转为 '\'' 序列） */
export function shQuote(v: string): string {
  return "'" + v.replace(/'/g, `'\\''`) + "'"
}

/** 槽位的实际模型 id：按需附 [1M] 后缀（官方文档里大小写均可，这里对齐 cc-switch 的大写） */
function slotModelId(slot: ClaudeCodeSlot): string {
  return slot.oneM ? `${slot.model}[1M]` : slot.model
}

/**
 * 生成 env 键值映射（有序）。返回普通对象：JS 对象保持字符串键的插入顺序。
 * 键序对齐 cc-switch：AUTH_TOKEN / BASE_URL / FABLE / HAIKU / OPUS / SONNET /
 * MODEL / SUBAGENT。
 */
export function buildClaudeCodeEnv(opts: ClaudeCodeConfigOptions): Record<string, string> {
  const env: Record<string, string> = {
    ANTHROPIC_AUTH_TOKEN: opts.apiKey,
    ANTHROPIC_BASE_URL: opts.baseUrl,
  }
  // 四个别名槽位：MODEL 是实际请求的模型 id，_NAME 是 /model 选择器的显示名
  // （取裸 id，与 cc-switch 一致）。顺序 FABLE/HAIKU/OPUS/SONNET 对齐 cc-switch。
  const aliasSlots = [
    ['fable', 'ANTHROPIC_DEFAULT_FABLE_MODEL'],
    ['haiku', 'ANTHROPIC_DEFAULT_HAIKU_MODEL'],
    ['opus', 'ANTHROPIC_DEFAULT_OPUS_MODEL'],
    ['sonnet', 'ANTHROPIC_DEFAULT_SONNET_MODEL'],
  ] as const
  for (const [key, envName] of aliasSlots) {
    const slot = opts.mapping[key]
    if (!slot?.model) continue
    env[envName] = slotModelId(slot)
    env[`${envName}_NAME`] = slot.model
  }
  if (opts.mapping.main?.model) env.ANTHROPIC_MODEL = slotModelId(opts.mapping.main)
  if (opts.mapping.subagent?.model) env.CLAUDE_CODE_SUBAGENT_MODEL = slotModelId(opts.mapping.subagent)
  if (opts.gatewayModelDiscovery) env.CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY = '1'
  if (opts.disableNonessentialTraffic) env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = '1'
  return env
}

/** 生成可追加到 ~/.bashrc / ~/.zshrc 的 export 段（含用法头注释） */
export function buildClaudeCodeSh(opts: ClaudeCodeConfigOptions): string {
  const env = buildClaudeCodeEnv(opts)
  const lines = Object.entries(env).map(([k, v]) => `export ${k}=${shQuote(v)}`)
  return HEADER + lines.join('\n') + '\n'
}
