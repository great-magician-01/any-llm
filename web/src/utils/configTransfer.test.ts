import { describe, it, expect } from 'vitest'
import { configFileName, parseConfigFile, describeConfigFile, describeImportResult } from './configTransfer'
import type { ConfigFile, ImportResult } from '../api/config'

describe('configFileName', () => {
  it('生成带零填充时间戳的文件名', () => {
    // 2026-09-02 03:04:05（本地时间）→ 各段两位补零
    expect(configFileName(new Date(2026, 8, 2, 3, 4, 5))).toBe('any-llm-config-20260902-030405.json')
    expect(configFileName(new Date(2026, 11, 31, 23, 59, 59))).toBe('any-llm-config-20261231-235959.json')
  })
})

describe('parseConfigFile', () => {
  it('解析合法配置文件', () => {
    const f = parseConfigFile('{"version":1,"upstreams":[],"aliases":[]}')
    expect(f.version).toBe(1)
    expect(f.upstreams).toEqual([])
    expect(f.aliases).toEqual([])
  })

  it('拒绝非法 JSON', () => {
    expect(() => parseConfigFile('{not json')).toThrow('JSON')
  })

  it('拒绝缺少 upstreams/aliases 的结构', () => {
    expect(() => parseConfigFile('{"version":1}')).toThrow('upstreams')
    expect(() => parseConfigFile('{"upstreams":[]}')).toThrow('aliases')
  })

  it('拒绝超过当前版本的文件', () => {
    expect(() => parseConfigFile('{"version":99,"upstreams":[],"aliases":[]}')).toThrow('版本')
  })

  it('缺省版本视为 v1 接受（手写文件）', () => {
    expect(() => parseConfigFile('{"upstreams":[],"aliases":[]}')).not.toThrow()
  })
})

describe('describeConfigFile', () => {
  it('描述文件内容与覆盖语义', () => {
    const f = { version: 1, exported_at: '', upstreams: [{ name: 'a' }], aliases: [{ name: 'x' }, { name: 'y' }] } as unknown as ConfigFile
    expect(describeConfigFile(f)).toContain('1 个上游')
    expect(describeConfigFile(f)).toContain('2 个别名')
    expect(describeConfigFile(f)).toContain('同名配置将被覆盖')
  })

  it('提醒基础字段以文件为准（缺失即清空）', () => {
    const f = { version: 1, upstreams: [], aliases: [] } as unknown as ConfigFile
    expect(describeConfigFile(f)).toContain('地址')
    expect(describeConfigFile(f)).toContain('API Key')
  })
})

describe('describeImportResult', () => {
  it('汇总导入计数', () => {
    const r: ImportResult = { upstreams_created: 2, upstreams_updated: 1, aliases_created: 0, aliases_updated: 3, aliases_skipped: 1, bindings_dropped: 2 }
    const s = describeImportResult(r)
    expect(s).toContain('上游新建 2')
    expect(s).toContain('覆盖 1')
    expect(s).toContain('别名新建 0')
    expect(s).toContain('覆盖 3')
    expect(s).toContain('跳过 1')
    expect(s).toContain('丢弃绑定 2')
  })
})
