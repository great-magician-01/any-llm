import { describe, expect, it } from 'vitest'
import { buildPiJson } from './piConfig'

describe('buildPiJson', () => {
  it('生成合法 JSON（models.json 会被 pi 整体解析，不能带注释）', () => {
    const text = buildPiJson({
      baseUrl: 'http://localhost:6718/v1',
      apiKey: 'all-sk-abc123',
      models: [{ id: 'deepseek/deepseek-chat', contextWindow: 65536, maxTokens: 8192 }],
    })
    const cfg = JSON.parse(text)
    expect(cfg.providers['any-llm']).toMatchObject({
      baseUrl: 'http://localhost:6718/v1',
      api: 'openai-completions',
      apiKey: 'all-sk-abc123',
    })
    expect(cfg.providers['any-llm'].models).toEqual([
      {
        id: 'deepseek/deepseek-chat',
        name: 'deepseek/deepseek-chat',
        reasoning: true,
        input: ['text'],
        contextWindow: 65536,
        maxTokens: 8192,
      },
    ])
  })

  it('网关未录容量（0）时不输出 contextWindow / maxTokens，交给 pi 默认值', () => {
    const text = buildPiJson({
      baseUrl: 'http://localhost:6718/v1',
      apiKey: 'k',
      models: [{ id: 'up/m', contextWindow: 0, maxTokens: 0 }],
    })
    const m = JSON.parse(text).providers['any-llm'].models[0]
    expect(m).not.toHaveProperty('contextWindow')
    expect(m).not.toHaveProperty('maxTokens')
  })

  it('无模型时输出空 models 数组', () => {
    const cfg = JSON.parse(buildPiJson({ baseUrl: 'http://x/v1', apiKey: 'k', models: [] }))
    expect(cfg.providers['any-llm'].models).toEqual([])
  })

  it('带别名的模型 id 原样输出（含斜杠，不做任何转换）', () => {
    const cfg = JSON.parse(buildPiJson({
      baseUrl: 'http://x/v1',
      apiKey: 'k',
      models: [{ id: 'my-alias', contextWindow: 1, maxTokens: 1 }],
    }))
    expect(cfg.providers['any-llm'].models[0].id).toBe('my-alias')
  })
})
