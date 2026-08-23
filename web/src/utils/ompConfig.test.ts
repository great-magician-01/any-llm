import { describe, it, expect } from 'vitest'
import { buildOmpYaml } from './ompConfig'

// 生成器头部注释（与 ompConfig.ts 内 HEADER 保持一致的副本，用于全量比对）
const HEADER = `# ============================================================
# Oh My Pi (omp) 配置 — 由 any-llm 生成（指向本网关）
# 1. 粘贴到 ~/.omp/agent/models.yml（已有内容则合并进 providers 段）
# 2. 模型 id 格式：upstream-name/model-name，与网关 /v1/models 一致
# Web 搜索：无需单独配置搜索模型，当前选中的模型即可用于搜索。
#   启用：omp plugin install @ollama/pi-web-search（本地 Ollama），
#   并在 ~/.omp/config.yml 中设置 web_search.enabled: true。
# ============================================================
`

describe('buildOmpYaml', () => {
  it('生成指向网关的中转配置：所有模型统一推理、名称原样', () => {
    const yaml = buildOmpYaml({
      baseUrl: 'http://localhost:6718',
      apiKey: 'all-sk-test0000000000000000000000000',
      models: [
        { id: 'deepseek/deepseek-v4-pro', contextWindow: 1000000, maxTokens: 384000 },
        { id: 'openai/gpt-4o', contextWindow: 128000, maxTokens: 16384 },
      ],
    })
    expect(yaml).toBe(
      HEADER + `providers:
  any-llm:
    baseUrl: http://localhost:6718
    api: openai-completions
    apiKey: all-sk-test0000000000000000000000000
    authHeader: true
    models:
      - id: deepseek/deepseek-v4-pro
        name: deepseek/deepseek-v4-pro
        reasoning: true
        thinking:
          minLevel: low
          maxLevel: max
          mode: effort
        input: [text]
        contextWindow: 1000000
        maxTokens: 384000
      - id: openai/gpt-4o
        name: openai/gpt-4o
        reasoning: true
        thinking:
          minLevel: low
          maxLevel: max
          mode: effort
        input: [text]
        contextWindow: 128000
        maxTokens: 16384
`,
    )
  })

  it('无模型时输出 models: [] 与提示注释', () => {
    const yaml = buildOmpYaml({ baseUrl: 'http://localhost:6718', apiKey: 'all-sk-x', models: [] })
    expect(yaml).toContain('    models: []')
    expect(yaml).toContain('    # 暂无模型，请在 any-llm 上游中添加模型后重新复制')
  })

  it('特殊字符安全引用', () => {
    const yaml = buildOmpYaml({
      baseUrl: 'http://localhost:6718',
      apiKey: 'all-sk-x',
      models: [{ id: '[beta] m', contextWindow: 1, maxTokens: 1 }],
    })
    expect(yaml).toContain('      - id: "[beta] m"')
  })
})
