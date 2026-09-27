import { describe, it, expect, vi, afterEach } from 'vitest'
import { configFileName, parseConfigFile, describeConfigFile, describeImportResult, downloadJSON } from './configTransfer'
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

  it('拒绝数组 / null / 基本类型（合法 JSON 但不是配置对象）', () => {
    // 手选错文件（比如选了个 JSON 数组）时必须给出和「结构不符」同一句人话
    expect(() => parseConfigFile('[]')).toThrow('配置文件格式不正确')
    expect(() => parseConfigFile('null')).toThrow('配置文件格式不正确')
    expect(() => parseConfigFile('123')).toThrow('配置文件格式不正确')
    expect(() => parseConfigFile('"x"')).toThrow('配置文件格式不正确')
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

/**
 * downloadJSON 守的规则：导出的是给人看/手改的 JSON（缩进 2 空格）、文件名带时间戳、
 * 触发浏览器下载后立刻回收 object URL（不回收就是一次导出泄漏一个 blob）。
 */
describe('downloadJSON', () => {
  const originalCreate = URL.createObjectURL
  const originalRevoke = URL.revokeObjectURL

  afterEach(() => {
    URL.createObjectURL = originalCreate
    URL.revokeObjectURL = originalRevoke
    vi.restoreAllMocks()
  })

  it('用 pretty JSON 建 blob 并触发下载，结束后回收 URL', async () => {
    const blobs: Blob[] = []
    const createObjectURL = vi.fn((b: Blob) => {
      blobs.push(b)
      return 'blob:any-llm-config'
    })
    const revokeObjectURL = vi.fn()
    URL.createObjectURL = createObjectURL as unknown as typeof URL.createObjectURL
    URL.revokeObjectURL = revokeObjectURL

    const clicked: HTMLAnchorElement[] = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this)
    })

    const payload = { version: 1, upstreams: [{ name: 'a' }], aliases: [] }
    downloadJSON(payload, 'any-llm-config-20260902-030405.json')

    expect(createObjectURL).toHaveBeenCalledTimes(1)
    expect(blobs[0].type).toBe('application/json')
    // 缩进 2 空格：导出文件是给人看/手改的
    expect(await blobs[0].text()).toBe(JSON.stringify(payload, null, 2))
    expect(clicked).toHaveLength(1)
    expect(clicked[0].download).toBe('any-llm-config-20260902-030405.json')
    expect(clicked[0].href).toContain('blob:any-llm-config')
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:any-llm-config')
  })

  it('没有可序列化内容时也不抛错（导出空配置）', () => {
    // 新装实例没有上游/别名，导出空配置也必须能下载
    URL.createObjectURL = vi.fn(() => 'blob:empty') as unknown as typeof URL.createObjectURL
    URL.revokeObjectURL = vi.fn()
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

    expect(() => downloadJSON({ version: 1, upstreams: [], aliases: [] }, 'x.json')).not.toThrow()
  })
})
