import { describe, it, expect } from 'vitest'
import { expandExportModels, filterAllowedModels } from './exportModels'
import { DEFAULT_MODEL_CONTEXT_LENGTH, DEFAULT_MODEL_MAX_OUTPUT_LENGTH } from '../api/upstreams'
import type { Upstream, UpstreamModel } from '../api/upstreams'
import type { ModelAlias } from '../api/aliases'

const up = (id: number, name: string, over: Partial<Upstream> = {}): Upstream => ({
  id,
  name,
  base_url: '',
  api_key: '',
  format: 'openai',
  enabled: true,
  daily_token_limit: 0,
  monthly_token_limit: 0,
  max_concurrent: 100,
  expires_at: null,
  ...over,
})

const mod = (name: string, context_length = 1000, max_output_length = 100): UpstreamModel => ({
  id: 0,
  upstream_id: 0,
  model_name: name,
  manual: false,
  context_length,
  max_output_length,
  multimodal: false,
})

const alias = (name: string, bindings: Array<[number, string]>): ModelAlias => ({
  id: 0,
  name,
  bindings: bindings.map(([upstream_id, model_name], i) => ({ upstream_id, model_name, priority: i + 1 })),
})

const past = '2020-01-01T00:00:00+08:00'
const future = '2999-01-01T00:00:00+08:00'

describe('expandExportModels', () => {
  it('列出启用上游的直连名（upstream/model）', () => {
    const out = expandExportModels([up(1, 'deepseek')], new Map([[1, [mod('deepseek-chat', 128000, 8192)]]]), [])
    expect(out).toEqual([{ id: 'deepseek/deepseek-chat', contextLength: 128000, maxOutputLength: 8192 }])
  })

  it('禁用/过期上游的直连名不导出（与 /v1/models 一致）', () => {
    const ups = [up(1, 'on'), up(2, 'off', { enabled: false }), up(3, 'gone', { expires_at: past }), up(4, 'ok-until', { expires_at: future })]
    const models = new Map<number, UpstreamModel[]>([
      [1, [mod('m1')]],
      [2, [mod('m2')]],
      [3, [mod('m3')]],
      [4, [mod('m4')]],
    ])
    const ids = expandExportModels(ups, models, []).map((m) => m.id)
    expect(ids).toEqual(['on/m1', 'ok-until/m4'])
  })

  it('别名收录，长度取第一个可用绑定的模型配置', () => {
    const ups = [up(1, 'a', { enabled: false }), up(2, 'b')]
    const models = new Map<number, UpstreamModel[]>([[2, [mod('m', 64000, 4096)]]])
    // 第一绑定的上游已禁用 → 落到第二绑定（网关的候选跳过口径）
    const out = expandExportModels(ups, models, [alias('fast', [[1, 'm'], [2, 'm']])])
    expect(out).toEqual([
      { id: 'b/m', contextLength: 64000, maxOutputLength: 4096 },
      { id: 'fast', contextLength: 64000, maxOutputLength: 4096 },
    ])
  })

  it('全绑定不可用的别名不收录（/v1/models 同样不列出）', () => {
    const ups = [up(1, 'a', { enabled: false }), up(2, 'gone', { expires_at: past })]
    const models = new Map<number, UpstreamModel[]>([
      [1, [mod('m')]],
      [2, [mod('m')]],
    ])
    const aliases = [alias('dead', [[1, 'm'], [2, 'm']]), alias('ghost', [[99, 'm']])]
    expect(expandExportModels(ups, models, aliases)).toEqual([])
  })

  it('绑定指向的模型行不存在时回落网关默认长度', () => {
    const out = expandExportModels([up(1, 'a')], new Map([[1, []]]), [alias('x', [[1, 'deleted-model']])])
    expect(out).toEqual([{ id: 'x', contextLength: DEFAULT_MODEL_CONTEXT_LENGTH, maxOutputLength: DEFAULT_MODEL_MAX_OUTPUT_LENGTH }])
  })

  it('别名与直连名撞同 id 时别名覆盖（网关别名优先解析）', () => {
    const ups = [up(1, 'a'), up(2, 'b')]
    const models = new Map<number, UpstreamModel[]>([
      [1, [mod('m', 1000, 100)]],
      [2, [mod('real', 2000, 200)]],
    ])
    // 别名就叫 "a/m"：网关会按别名路由到 b/real，导出应以别名配置为准且只有一条
    const out = expandExportModels(ups, models, [alias('a/m', [[2, 'real']])])
    expect(out).toEqual([
      { id: 'a/m', contextLength: 2000, maxOutputLength: 200 },
      { id: 'b/real', contextLength: 2000, maxOutputLength: 200 },
    ])
  })
})

describe('filterAllowedModels', () => {
  const models = expandExportModels(
    [up(1, 'a')],
    new Map([[1, [mod('m1'), mod('m2')]]]),
    [alias('fast', [[1, 'm1']])],
  )

  it('空/null 白名单 = 不限', () => {
    expect(filterAllowedModels(models, null)).toHaveLength(3)
    expect(filterAllowedModels(models, [])).toHaveLength(3)
    expect(filterAllowedModels(models)).toHaveLength(3)
  })

  it('白名单精确匹配直连名与别名', () => {
    expect(filterAllowedModels(models, ['fast', 'a/m2']).map((m) => m.id)).toEqual(['a/m2', 'fast'])
    expect(filterAllowedModels(models, ['nope'])).toEqual([])
  })
})
