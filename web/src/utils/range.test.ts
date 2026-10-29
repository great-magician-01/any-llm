import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { currentMonthRange, currentYear, lastNDays } from './range'

const originalTZ = process.env.TZ

beforeEach(() => {
  process.env.TZ = 'America/New_York'
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  process.env.TZ = originalTZ
})

function ymd(ts: number): string {
  const d = new Date(ts)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

describe('日期范围快捷档', () => {
  it('近 N 天跨春季夏令时仍按日历日计算（窗口可为 23 小时/天）', () => {
    vi.setSystemTime(new Date(2024, 2, 11, 12))
    const [start, end] = lastNDays(2)
    expect(ymd(start)).toBe('2024-03-10')
    expect(ymd(end)).toBe('2024-03-11')
    expect(end - start).toBe(23 * 60 * 60 * 1000)
  })

  it('近 N 天跨秋季夏令时仍按日历日计算（窗口可为 25 小时/天）', () => {
    vi.setSystemTime(new Date(2024, 10, 4, 12))
    const [start, end] = lastNDays(2)
    expect(ymd(start)).toBe('2024-11-03')
    expect(ymd(end)).toBe('2024-11-04')
    expect(end - start).toBe(25 * 60 * 60 * 1000)
  })

  it('本月和今年都截止到今天本地零点', () => {
    vi.setSystemTime(new Date(2024, 2, 11, 12))
    const month = currentMonthRange()
    const year = currentYear()
    expect(ymd(month[0])).toBe('2024-03-01')
    expect(ymd(month[1])).toBe('2024-03-11')
    expect(ymd(year[0])).toBe('2024-01-01')
    expect(ymd(year[1])).toBe('2024-03-11')
  })
})
