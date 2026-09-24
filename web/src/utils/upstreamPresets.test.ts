import { describe, it, expect } from 'vitest'
import type { SelectGroupOption } from 'naive-ui'
import { UPSTREAM_PRESETS, findPreset, presetSelectOptions } from './upstreamPresets'

describe('UPSTREAM_PRESETS 数据完整性', () => {
  it('key 全局唯一', () => {
    const keys = UPSTREAM_PRESETS.map(p => p.key)
    expect(new Set(keys).size).toBe(keys.length)
  })

  it('format 只取三种协议值', () => {
    for (const p of UPSTREAM_PRESETS) {
      expect(['openai', 'anthropic', 'responses']).toContain(p.format)
    }
  })

  it('baseUrl 一律 https 且无尾斜杠（endpointURL 直接拼路径）', () => {
    for (const p of UPSTREAM_PRESETS) {
      expect(p.baseUrl).toMatch(/^https:\/\//)
      expect(p.baseUrl.endsWith('/')).toBe(false)
    }
  })

  // 后端 endpointURL 约定：anthropic 上游的 base 不带 /v1，由网关自补；
  // openai/responses 必须自带版本段。这张表违反约定会让预设带出打不通的地址。
  it('anthropic 预设不含 /v1 版本段', () => {
    for (const p of UPSTREAM_PRESETS.filter(p => p.format === 'anthropic')) {
      expect(p.baseUrl).not.toMatch(/\/v1(\/|$)/)
    }
  })

  it('openai/responses 预设自带版本段', () => {
    for (const p of UPSTREAM_PRESETS.filter(p => p.format !== 'anthropic')) {
      expect(p.baseUrl).toMatch(/\/v\d+(\/|$)/)
    }
  })

  it('group 限定在 api/coding/official 且 label 非空', () => {
    for (const p of UPSTREAM_PRESETS) {
      expect(['api', 'coding', 'official']).toContain(p.group)
      expect(p.label.length).toBeGreaterThan(0)
    }
  })
})

describe('findPreset / presetSelectOptions', () => {
  it('按 key 命中；null/未知 key 返回 undefined', () => {
    expect(findPreset('deepseek')?.baseUrl).toBe('https://api.deepseek.com/v1')
    expect(findPreset(null)).toBeUndefined()
    expect(findPreset('no-such-preset')).toBeUndefined()
  })

  it('分组选项覆盖全部预设，且每组 label 已填', () => {
    const groups = presetSelectOptions().filter((o): o is SelectGroupOption => 'children' in o)
    const children = groups.flatMap(g => g.children ?? [])
    expect(children.map(c => c.value).sort()).toEqual(UPSTREAM_PRESETS.map(p => p.key).sort())
    for (const g of groups) {
      expect(typeof g.label).toBe('string')
      expect((g.label as string).length).toBeGreaterThan(0)
    }
  })
})
