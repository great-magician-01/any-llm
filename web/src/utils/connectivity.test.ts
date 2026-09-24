import { describe, it, expect } from 'vitest'
import { connectivityView } from './connectivity'

describe('connectivityView', () => {
  it('ok=true 时给成功文案，含模型数与耗时', () => {
    const v = connectivityView({ ok: true, reachable: true, latency_ms: 123, status: 200, models: 5 })
    expect(v.type).toBe('success')
    expect(v.text).toContain('5 个模型')
    expect(v.text).toContain('123ms')
  })

  it('ok=true 但没有 models 字段时不报模型数', () => {
    const v = connectivityView({ ok: true, reachable: true, latency_ms: 10 })
    expect(v.type).toBe('success')
    expect(v.text).not.toContain('模型')
  })

  it('reachable=false 是网络层失败，展示 detail', () => {
    const v = connectivityView({ ok: false, reachable: false, latency_ms: 3, detail: 'dial tcp: connection refused' })
    expect(v.type).toBe('error')
    expect(v.text).toContain('无法连通')
    expect(v.text).toContain('connection refused')
  })

  it('401/403 提示检查 API Key', () => {
    for (const status of [401, 403]) {
      const v = connectivityView({ ok: false, reachable: true, latency_ms: 50, status })
      expect(v.type).toBe('error')
      expect(v.text).toContain(`HTTP ${status}`)
      expect(v.text).toContain('API Key')
    }
  })

  it('404 等其它非 2xx 按 warning：能连通但可能不支持模型列表', () => {
    const v = connectivityView({ ok: false, reachable: true, latency_ms: 50, status: 404 })
    expect(v.type).toBe('warning')
    expect(v.text).toContain('HTTP 404')
  })

  it('2xx 但应答不是模型列表也按 warning', () => {
    const v = connectivityView({ ok: false, reachable: true, latency_ms: 50, status: 200, detail: '2xx response is not a models list' })
    expect(v.type).toBe('warning')
    expect(v.text).toContain('不是模型列表')
  })
})
