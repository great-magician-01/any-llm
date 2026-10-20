import { describe, expect, it } from 'vitest'
import { buildOpencodeJson } from './opencodeConfig'

describe('buildOpencodeJson', () => {
  it('生成合法 JSON：provider.any-llm 携带 baseURL / apiKey 与 models 映射', () => {
    const text = buildOpencodeJson({
      baseUrl: 'http://localhost:6718/v1',
      apiKey: 'all-sk-abc123',
      models: [{ id: 'deepseek/deepseek-chat', contextLength: 65536, maxOutputLength: 8192 }],
    })
    const cfg = JSON.parse(text)
    expect(cfg.$schema).toBe('https://opencode.ai/config.json')
    expect(cfg.provider['any-llm']).toMatchObject({
      npm: '@ai-sdk/openai-compatible',
      name: 'any-llm',
      options: { baseURL: 'http://localhost:6718/v1', apiKey: 'all-sk-abc123' },
    })
    expect(cfg.provider['any-llm'].models).toEqual({
      'deepseek/deepseek-chat': { name: 'deepseek/deepseek-chat', limit: { context: 65536, output: 8192 } },
    })
  })

  it('模型 id 原样作为键（含斜杠的直连名与别名都原样保留，不做任何转换）', () => {
    const cfg = JSON.parse(buildOpencodeJson({
      baseUrl: 'http://x/v1',
      apiKey: 'k',
      models: [{ id: 'my-alias', contextLength: 1, maxOutputLength: 1 }],
    }))
    expect(Object.keys(cfg.provider['any-llm'].models)).toEqual(['my-alias'])
  })

  it('无模型时输出空 models 映射（opencode 的 provider 可以先落位，模型后补）', () => {
    const cfg = JSON.parse(buildOpencodeJson({ baseUrl: 'http://x/v1', apiKey: 'k', models: [] }))
    expect(cfg.provider['any-llm'].models).toEqual({})
  })
})
