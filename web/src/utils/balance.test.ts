import { describe, it, expect } from 'vitest'
import { parsePayload, balanceView, balanceSummary, balanceTooltip, formatFetchedAt } from './balance'
import type { BalanceSnapshot } from '../api/balances'

function snap(payload: unknown, vendor = 'kimi-coding'): BalanceSnapshot {
  return { id: 1, upstream_id: 1, upstream_name: 'kimi', vendor, payload: payload as any, created_at: '2026-09-12T21:14:23+08:00' }
}

describe('parsePayload', () => {
  it('接受对象或 JSON 字符串', () => {
    expect(parsePayload(snap({ kind: 'quota', windows: [] }))).toEqual({ kind: 'quota', windows: [] })
    expect(parsePayload(snap('{"kind":"quota","windows":[]}'))).toEqual({ kind: 'quota', windows: [] })
  })
  it('非法字符串返回 null', () => {
    expect(parsePayload(snap('not json'))).toBeNull()
  })
})

describe('balanceView', () => {
  it('balance 快照格式化为金额', () => {
    const v = balanceView(snap({ kind: 'balance', is_available: true, balances: [{ currency: 'CNY', total: '155.70', granted: '0.00', topped_up: '155.70' }] }, 'deepseek'))
    expect(v).toEqual({ kind: 'balance', text: '¥155.70' })
  })

  it('quota 快照按固定顺序输出窗口，重置时间转本地紧凑格式', () => {
    const v = balanceView(snap({
      kind: 'quota',
      windows: [
        { id: 'weekly', used_percent: '13.0', reset_at: '2026-09-17T01:02:51Z' },
        { id: 'five_hour', used_percent: '15.0', reset_at: '2026-09-11T19:02:51Z' },
      ],
    }))
    expect(v?.kind).toBe('quota')
    if (v?.kind !== 'quota') return
    // five_hour 排在 weekly 前；UTC 转本地（测试环境时区未知，只校验格式）
    expect(v.windows.map(w => w.id)).toEqual(['five_hour', 'weekly'])
    expect(v.windows[0]).toMatchObject({ label: '5h', percent: '15.0', missing: false })
    expect(v.windows[0].reset).toMatch(/^\d{2}:\d{2}$/)
    expect(v.windows[1].reset).toMatch(/^\d{1,2}-\d{1,2}$/)
  })

  it('kimi-coding 缺 five_hour 窗口时补占位行', () => {
    const v = balanceView(snap({ kind: 'quota', windows: [{ id: 'weekly', used_percent: '13.0', reset_at: '2026-09-17T01:02:51Z' }] }))
    if (v?.kind !== 'quota') throw new Error('expected quota')
    expect(v.windows).toHaveLength(2)
    expect(v.windows[0]).toMatchObject({ id: 'five_hour', percent: null, missing: true })
  })

  it('非 kimi-coding 不补占位行', () => {
    const v = balanceView(snap({ kind: 'quota', windows: [{ id: 'weekly', used_percent: '13.0' }] }, 'other'))
    if (v?.kind !== 'quota') throw new Error('expected quota')
    expect(v.windows).toHaveLength(1)
  })

  it('空 windows 返回 null', () => {
    expect(balanceView(snap({ kind: 'quota', windows: [] }))).toBeNull()
    expect(balanceView(undefined)).toBeNull()
  })
})

describe('balanceSummary', () => {
  it('quota 摘要带紧凑重置时间', () => {
    const s = balanceSummary(snap({
      kind: 'quota',
      windows: [
        { id: 'five_hour', used_percent: '15.0', reset_at: '2026-09-11T19:02:51Z' },
        { id: 'weekly', used_percent: '13.0', reset_at: '2026-09-17T01:02:51Z' },
      ],
    }))
    expect(s).toMatch(/^5h 15\.0%（\d{2}:\d{2} 重置） · 周 13\.0%（\d{1,2}-\d{1,2} 重置）$/)
  })

  it('无重置时间的窗口不带后缀；占位窗口不进摘要', () => {
    const s = balanceSummary(snap({ kind: 'quota', windows: [{ id: 'monthly', used_percent: '50.0' }] }))
    expect(s).toBe('月 50.0%')
  })
})

describe('balanceTooltip', () => {
  it('包含完整重置时间与抓取时间', () => {
    const t = balanceTooltip(snap({ kind: 'quota', windows: [{ id: 'weekly', used_percent: '13.0', reset_at: '2026-09-17T01:02:51Z' }] }))
    expect(t).toContain('周 13.0%，重置于 ')
    expect(t).toContain('抓取于 2026-09-12 21:14:23')
    expect(t).toContain('5h 窗口本次未返回')
  })
})

describe('formatFetchedAt', () => {
  it('当天只显示时刻，跨天带日期', () => {
    const today = formatFetchedAt(new Date().toISOString())
    expect(today).toMatch(/^\d{2}:\d{2}:\d{2}$/)
    expect(formatFetchedAt('2000-01-01T08:09:10Z')).toMatch(/^1-1 \d{2}:\d{2}:\d{2}$/)
  })
})
