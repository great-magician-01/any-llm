import { describe, it, expect } from 'vitest'
import { isExpired, expiryLabel, expiryToISO, isoToExpiry, expiryTime } from './upstreamStatus'
import type { Upstream } from '../api/upstreams'

function up(expires_at: string | null | undefined): Upstream {
  return { name: 'u', base_url: 'https://x', api_key: 'k', format: 'openai', enabled: true, daily_token_limit: 0, monthly_token_limit: 0, max_concurrent: 100, expires_at }
}

describe('isExpired', () => {
  it('未设置有效期 = 永久有效', () => {
    expect(isExpired(up(null))).toBe(false)
    expect(isExpired(up(undefined))).toBe(false)
  })

  it('到点即失效，含等于截止时刻的那一刻', () => {
    const at = '2026-09-21T12:00:00+08:00'
    const t = new Date(at).getTime()
    expect(isExpired(up(at), t - 1)).toBe(false)
    expect(isExpired(up(at), t)).toBe(true)
    expect(isExpired(up(at), t + 1)).toBe(true)
  })

  it('非法日期串按未过期处理', () => {
    expect(isExpired(up('not-a-date'))).toBe(false)
    expect(expiryTime(up('not-a-date'))).toBeNull()
  })
})

describe('expiryLabel', () => {
  it('三种展示态', () => {
    expect(expiryLabel(up(null))).toEqual({ text: '永久', tone: 'muted' })
    const at = '2026-09-21T12:00:00+08:00'
    const t = new Date(at).getTime()
    expect(expiryLabel(up(at), t - 1).tone).toBe('ok')
    expect(expiryLabel(up(at), t + 1)).toEqual({ text: '已过期', tone: 'error' })
  })
})

describe('expiryToISO / isoToExpiry', () => {
  it('null 往返 null', () => {
    expect(expiryToISO(null)).toBeNull()
    expect(isoToExpiry(null)).toBeNull()
    expect(isoToExpiry(undefined)).toBeNull()
  })

  it('epoch ms -> ISO -> epoch ms 保持同一绝对时刻', () => {
    const ms = new Date('2026-09-21T12:00:00+08:00').getTime()
    const iso = expiryToISO(ms)
    expect(iso).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2}$/)
    expect(new Date(iso as string).getTime()).toBe(ms)
    expect(isoToExpiry(iso)).toBe(ms)
  })

  it('截断到秒（后端同口径），亚秒不参与往返', () => {
    const ms = new Date('2026-09-21T12:00:00.789+08:00').getTime()
    expect(isoToExpiry(expiryToISO(ms))).toBe(ms - 789)
  })
})
