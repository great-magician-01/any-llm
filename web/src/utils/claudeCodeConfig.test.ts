import { describe, it, expect } from 'vitest'
import { buildClaudeCodeEnv, buildClaudeCodeSh } from './claudeCodeConfig'

const BASE = { baseUrl: 'http://192.168.0.105:6718', apiKey: 'all-sk-test0000000000000000000000000' }

// 生成器头部注释（与 claudeCodeConfig.ts 内 HEADER 保持一致的副本，用于全量比对）
const HEADER = `# ============================================================
# Claude Code 配置（Linux）— 由 any-llm 生成（指向本网关）
# 用法：整段追加到 ~/.bashrc（或 ~/.zshrc），然后执行
#   source ~/.bashrc
# 再正常启动 claude 即可。切换模型：会话内用 /model。
# ============================================================
`

describe('buildClaudeCodeEnv', () => {
  it('生成对齐 cc-switch 的完整 env：槽位 MODEL + _NAME 成对，键序固定', () => {
    const env = buildClaudeCodeEnv({
      ...BASE,
      mapping: {
        fable: { model: 'expert', oneM: true },
        haiku: { model: 'flash' },
        opus: { model: 'pro', oneM: true },
        sonnet: { model: 'flash', oneM: true },
        main: { model: 'expert', oneM: true },
        subagent: { model: 'flash', oneM: true },
      },
    })
    expect(env).toEqual({
      ANTHROPIC_AUTH_TOKEN: 'all-sk-test0000000000000000000000000',
      ANTHROPIC_BASE_URL: 'http://192.168.0.105:6718',
      ANTHROPIC_DEFAULT_FABLE_MODEL: 'expert[1M]',
      ANTHROPIC_DEFAULT_FABLE_MODEL_NAME: 'expert',
      ANTHROPIC_DEFAULT_HAIKU_MODEL: 'flash',
      ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME: 'flash',
      ANTHROPIC_DEFAULT_OPUS_MODEL: 'pro[1M]',
      ANTHROPIC_DEFAULT_OPUS_MODEL_NAME: 'pro',
      ANTHROPIC_DEFAULT_SONNET_MODEL: 'flash[1M]',
      ANTHROPIC_DEFAULT_SONNET_MODEL_NAME: 'flash',
      ANTHROPIC_MODEL: 'expert[1M]',
      CLAUDE_CODE_SUBAGENT_MODEL: 'flash[1M]',
    })
    // 键序对齐 cc-switch：AUTH_TOKEN / BASE_URL / FABLE / HAIKU / OPUS / SONNET / MODEL / SUBAGENT
    expect(Object.keys(env)).toEqual([
      'ANTHROPIC_AUTH_TOKEN',
      'ANTHROPIC_BASE_URL',
      'ANTHROPIC_DEFAULT_FABLE_MODEL',
      'ANTHROPIC_DEFAULT_FABLE_MODEL_NAME',
      'ANTHROPIC_DEFAULT_HAIKU_MODEL',
      'ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME',
      'ANTHROPIC_DEFAULT_OPUS_MODEL',
      'ANTHROPIC_DEFAULT_OPUS_MODEL_NAME',
      'ANTHROPIC_DEFAULT_SONNET_MODEL',
      'ANTHROPIC_DEFAULT_SONNET_MODEL_NAME',
      'ANTHROPIC_MODEL',
      'CLAUDE_CODE_SUBAGENT_MODEL',
    ])
  })

  it('未设置的槽位不导出；主会话与子代理槽位没有 _NAME 变量', () => {
    const env = buildClaudeCodeEnv({ ...BASE, mapping: { main: { model: 'a/b' } } })
    expect(env).toEqual({
      ANTHROPIC_AUTH_TOKEN: BASE.apiKey,
      ANTHROPIC_BASE_URL: BASE.baseUrl,
      ANTHROPIC_MODEL: 'a/b',
    })
  })

  it('模型 id 为空串的槽位视为未设置', () => {
    const env = buildClaudeCodeEnv({ ...BASE, mapping: { opus: { model: '' }, haiku: { model: 'x/y' } } })
    expect(env).not.toHaveProperty('ANTHROPIC_DEFAULT_OPUS_MODEL')
    expect(env).not.toHaveProperty('ANTHROPIC_DEFAULT_OPUS_MODEL_NAME')
    expect(env.ANTHROPIC_DEFAULT_HAIKU_MODEL).toBe('x/y')
    expect(env.ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME).toBe('x/y')
  })

  it('开关变量按需导出，值为字符串 1', () => {
    const env = buildClaudeCodeEnv({
      ...BASE,
      mapping: {},
      gatewayModelDiscovery: true,
      disableNonessentialTraffic: true,
    })
    expect(env.CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY).toBe('1')
    expect(env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC).toBe('1')
    const off = buildClaudeCodeEnv({ ...BASE, mapping: {} })
    expect(off).not.toHaveProperty('CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY')
    expect(off).not.toHaveProperty('CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC')
  })
})

describe('buildClaudeCodeSh', () => {
  it('输出可追加到 bashrc 的 export 段：头注释 + 每行一个 export', () => {
    const sh = buildClaudeCodeSh({
      ...BASE,
      mapping: {
        main: { model: 'expert', oneM: true },
        haiku: { model: 'flash' },
      },
    })
    expect(sh).toBe(
      HEADER +
        `export ANTHROPIC_AUTH_TOKEN='all-sk-test0000000000000000000000000'
export ANTHROPIC_BASE_URL='http://192.168.0.105:6718'
export ANTHROPIC_DEFAULT_HAIKU_MODEL='flash'
export ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME='flash'
export ANTHROPIC_MODEL='expert[1M]'
`,
    )
  })

  it('特殊字符安全引用（POSIX 单引号，内部 \' 转义）', () => {
    const sh = buildClaudeCodeSh({
      baseUrl: "http://host:6718/it's",
      apiKey: 'all-sk-x',
      mapping: { main: { model: 'a/b c' } },
    })
    expect(sh).toContain(`export ANTHROPIC_BASE_URL='http://host:6718/it'\\''s'`)
    expect(sh).toContain(`export ANTHROPIC_MODEL='a/b c'`)
  })
})
