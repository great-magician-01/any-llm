import { describe, it, expect } from 'vitest'
import { buildDshYaml } from './dshConfig'

// 生成器头部注释（与 dshConfig.ts 内 HEADER 保持一致的副本，用于全量比对）
const HEADER = `# ============================================================
# dsh (DeepSeek Harness) 配置 — 由 any-llm 生成（指向本网关）
# dsh 把路由与凭据分放在两个文件，两段都要粘贴：
# 1. settings 段合并进 ~/.dsh/settings.yaml（已有 llm-pi-ai 段则
#    只把 providers 下的 any-llm 提供方合并进去）
# 2. credentials 行追加到 ~/.dsh/.credentials.yaml（该文件是扁平的
#    引用: 密钥 映射，不要包进任何层级）
# 模型 id 格式：upstream-name/model-name，与网关 /v1/models 一致
# ============================================================
`

describe('buildDshYaml', () => {
  it('生成指向网关的中转配置：settings 提供方段 + credentials 凭据行', () => {
    const yaml = buildDshYaml({
      baseUrl: 'http://localhost:6718/v1', // 含 /v1，dsh 自拼 /chat/completions
      apiKey: 'all-sk-test0000000000000000000000000',
      models: [
        { id: 'deepseek/deepseek-v4-pro', contextWindow: 1000000, maxTokens: 384000 },
        { id: 'openai/gpt-4o', contextWindow: 128000, maxTokens: 16384 },
      ],
    })
    expect(yaml).toBe(
      HEADER + `
# ----- ~/.dsh/settings.yaml -----
llm-pi-ai:
  providers:
    any-llm:
      displayName: any-llm
      apiKeyEnv: ANY_LLM_API_KEY
      api: openai-completions
      baseURL: http://localhost:6718/v1
      models:
        - id: deepseek/deepseek-v4-pro
          name: deepseek/deepseek-v4-pro
          contextWindow: 1000000
          maxTokens: 384000
        - id: openai/gpt-4o
          name: openai/gpt-4o
          contextWindow: 128000
          maxTokens: 16384

# ----- ~/.dsh/.credentials.yaml -----
ANY_LLM_API_KEY: all-sk-test0000000000000000000000000
`,
    )
  })

  it('无模型时整段 settings 保持注释（dsh 拒绝空模型提供方）', () => {
    const yaml = buildDshYaml({ baseUrl: 'http://localhost:6718', apiKey: 'all-sk-x', models: [] })
    expect(yaml).toContain('# 暂无模型：dsh 拒绝不含模型的自定义提供方')
    expect(yaml).toContain('# llm-pi-ai:')
    expect(yaml).not.toContain('\nllm-pi-ai:')
    // 凭据行不受模型有无影响
    expect(yaml).toContain('ANY_LLM_API_KEY: all-sk-x')
  })

  it('特殊字符安全引用', () => {
    const yaml = buildDshYaml({
      baseUrl: 'http://localhost:6718',
      apiKey: 'all-sk-x',
      models: [{ id: '[beta] m', contextWindow: 1, maxTokens: 1 }],
    })
    expect(yaml).toContain('        - id: "[beta] m"')
  })

  it('网关未录容量（0）时不输出 contextWindow / maxTokens，交给 dsh 路由回退值', () => {
    const yaml = buildDshYaml({
      baseUrl: 'http://localhost:6718',
      apiKey: 'all-sk-x',
      models: [{ id: 'a/b', contextWindow: 0, maxTokens: 0 }],
    })
    expect(yaml).not.toContain('contextWindow')
    expect(yaml).not.toContain('maxTokens')
  })
})
