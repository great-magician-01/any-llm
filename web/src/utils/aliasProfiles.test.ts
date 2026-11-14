import { describe, expect, it } from 'vitest'
import {
  DEFAULT_ALIAS_PROFILE,
  flattenAliasProfiles,
  groupBindingsByProfile,
  normalizeProfileName,
  profileLabel,
  resolveActiveProfile,
  uniqueProfileName,
} from './aliasProfiles'

describe('aliasProfiles', () => {
  it('空配置名归一为 default，展示为“默认”', () => {
    expect(normalizeProfileName('  ')).toBe(DEFAULT_ALIAS_PROFILE)
    expect(normalizeProfileName(' 峰期 ')).toBe('峰期')
    expect(profileLabel(DEFAULT_ALIAS_PROFILE)).toBe('默认')
    expect(profileLabel('峰期')).toBe('峰期')
  })

  it('按 profile 分组：组顺序按首次出现，组内保持原顺序', () => {
    const groups = groupBindingsByProfile([
      { model_name: 'a', profile: '峰期' },
      { model_name: 'b', profile: '谷期' },
      { model_name: 'c', profile: '峰期' },
      { model_name: 'd', profile: '' },
    ])
    expect(groups.map((g) => g.name)).toEqual(['峰期', '谷期', DEFAULT_ALIAS_PROFILE])
    expect(groups[0].bindings.map((b) => b.model_name)).toEqual(['a', 'c'])
    expect(groups[1].bindings.map((b) => b.model_name)).toEqual(['b'])
    expect(groups[2].bindings.map((b) => b.model_name)).toEqual(['d'])
  })

  it('摊平后每条绑定带归一后的配置名，顺序跨配置稳定', () => {
    const out = flattenAliasProfiles([
      {
        name: '峰期',
        bindings: [
          { upstream_id: 1, model_name: ' a ' },
          { upstream_id: 2, model_name: 'b' },
        ],
      },
      { name: '', bindings: [{ upstream_id: 3, model_name: 'c' }] },
    ])
    expect(out).toEqual([
      { upstream_id: 1, model_name: 'a', profile: '峰期' },
      { upstream_id: 2, model_name: 'b', profile: '峰期' },
      { upstream_id: 3, model_name: 'c', profile: DEFAULT_ALIAS_PROFILE },
    ])
  })

  it('新配置名避让已有配置', () => {
    expect(uniqueProfileName([])).toBe('新配置')
    expect(uniqueProfileName(['新配置'])).toBe('新配置2')
    expect(uniqueProfileName(['新配置', '新配置2'])).toBe('新配置3')
    expect(uniqueProfileName(['峰期'], '新配置')).toBe('新配置')
  })

  it('生效配置不存在时回退到第一套', () => {
    const profiles = [
      { name: '峰期', bindings: [] },
      { name: '谷期', bindings: [] },
    ]
    expect(resolveActiveProfile('谷期', profiles)).toBe('谷期')
    expect(resolveActiveProfile('不存在', profiles)).toBe('峰期')
    expect(resolveActiveProfile('', [])).toBe(DEFAULT_ALIAS_PROFILE)
  })
})
