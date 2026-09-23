import { describe, it, expect } from 'vitest'
import { buildCalendarGrid, levelFor } from './calendar'

describe('levelFor', () => {
  it('无用量或窗口全零时为 0', () => {
    expect(levelFor(0, 100)).toBe(0)
    expect(levelFor(50, 0)).toBe(0)
  })

  it('按占峰值比例分四桶，任意正用量至少 1 级', () => {
    expect(levelFor(1, 100)).toBe(1)
    expect(levelFor(25, 100)).toBe(1)
    expect(levelFor(26, 100)).toBe(2)
    expect(levelFor(50, 100)).toBe(2)
    expect(levelFor(51, 100)).toBe(3)
    expect(levelFor(75, 100)).toBe(3)
    expect(levelFor(76, 100)).toBe(4)
    expect(levelFor(100, 100)).toBe(4)
  })
})

describe('buildCalendarGrid', () => {
  // 2026-09-24 是周四；回推 364 天是 2025-09-25（同为周四），再回退到周日 2025-09-21
  const today = new Date(2026, 8, 24)

  it('以今天收尾、首格为周日、共 53 列', () => {
    const g = buildCalendarGrid([], today)
    expect(g.cols).toBe(53)
    expect(g.cells[0].date).toBe('2025-09-21')
    expect(g.cells[0].row).toBe(0)
    const last = g.cells[g.cells.length - 1]
    expect(last.date).toBe('2026-09-24')
    expect(last.col).toBe(52)
    expect(last.row).toBe(4)
  })

  it('stats 按日期字符串分桶（不受时区影响），缺失日补 0', () => {
    const g = buildCalendarGrid(
      [{ day: '2026-09-24T00:00:00+08:00', total_tokens: 100, request_count: 3, error_count: 1 }],
      today,
    )
    const last = g.cells[g.cells.length - 1]
    expect(last.tokens).toBe(100)
    expect(last.requests).toBe(3)
    expect(last.errors).toBe(1)
    expect(last.level).toBe(4) // 唯一有量的一天即峰值
    const empty = g.cells.find((c) => c.date === '2026-09-23')!
    expect(empty.tokens).toBe(0)
    expect(empty.level).toBe(0)
  })

  it('月份标签：1 号落在哪列标哪列，首列补当月', () => {
    const g = buildCalendarGrid([], today)
    // 首列 2025-09-21…27 不含 1 号 → 补「9月」
    expect(g.monthLabels[0]).toEqual({ col: 0, text: '9月' })
    // 2025-10-01（周三）所在列的周日是 2025-09-28，即第 1 列
    expect(g.monthLabels[1]).toEqual({ col: 1, text: '10月' })
    // 2026-09-01 在窗口内，且没有 10 月（今天 9/24 之后不生成格子）
    const texts = g.monthLabels.map((m) => m.text)
    expect(texts).toContain('9月')
    expect(texts).not.toContain('13月')
    expect(g.monthLabels[g.monthLabels.length - 1].text).toBe('9月')
  })
})
