import { describe, it, expect } from 'vitest'
import { aliasUpstreamOptions } from './aliasOptions'
import type { Upstream } from '../api/upstreams'

function up(id: number, name: string, enabled: boolean, format = 'openai'): Upstream {
  return { id, name, base_url: '', api_key: '', format, enabled, daily_token_limit: 0, monthly_token_limit: 0, max_concurrent: 0 }
}

const ALL = [up(1, 'a', true), up(2, 'b', false), up(3, 'c', true)]

describe('aliasUpstreamOptions', () => {
  it('只列启用中的上游', () => {
    expect(aliasUpstreamOptions(ALL, [])).toEqual([
      { label: 'a（openai）', value: 1, disabled: false },
      { label: 'c（openai）', value: 3, disabled: false },
    ])
  })

  it('旧绑定引用的禁用上游补成不可选项，否则不出现', () => {
    const opts = aliasUpstreamOptions(ALL, [2])
    expect(opts).toEqual([
      { label: 'a（openai）', value: 1, disabled: false },
      { label: 'b（openai），已禁用', value: 2, disabled: true },
      { label: 'c（openai）', value: 3, disabled: false },
    ])
    // 引用关系一解除（换了上游或删了该行），禁用上游就从下拉里消失
    expect(aliasUpstreamOptions(ALL, [null]).map((o) => o.value)).toEqual([1, 3])
  })

  it('禁用的上游不可选，且标签带已禁用后缀', () => {
    const opts = aliasUpstreamOptions([up(1, 'off', false, 'anthropic')], [1])
    expect(opts[0].disabled).toBe(true)
    expect(opts[0].label).toBe('off（anthropic），已禁用')
  })
})
