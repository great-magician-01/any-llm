import { describe, expect, it } from 'vitest'
import { quotaPercent, quotaPercentFromString, quotaStatus, quotaStatusFromString } from './quota'

describe('quotaPercent / quotaStatus', () => {
  it('按比例取整并钳到 [0,100]', () => {
    expect(quotaPercent(50, 200)).toBe(25)
    expect(quotaPercent(0, 100)).toBe(0)
    expect(quotaPercent(300, 100)).toBe(100)
    expect(quotaPercent(1, 3)).toBe(33)
  })

  it('limit 为 0（不限）时按 0 处理', () => {
    expect(quotaPercent(123, 0)).toBe(0)
    expect(quotaStatus(123, 0)).toBe('success')
  })

  it('阈值：跑满 error，≥80% warning，其余 success', () => {
    expect(quotaStatus(79, 100)).toBe('success')
    expect(quotaStatus(80, 100)).toBe('warning')
    expect(quotaStatus(100, 100)).toBe('error')
    expect(quotaStatus(150, 100)).toBe('error')
  })
})

describe('字符串百分比版本（厂商返回）', () => {
  it('解析与钳制', () => {
    expect(quotaPercentFromString('82.5')).toBe(83)
    expect(quotaPercentFromString('150')).toBe(100)
    expect(quotaPercentFromString(null)).toBe(0)
    expect(quotaPercentFromString('')).toBe(0)
    expect(quotaPercentFromString('x')).toBe(0)
  })

  it('阈值与数值版一致', () => {
    expect(quotaStatusFromString('79.9')).toBe('success')
    expect(quotaStatusFromString('80')).toBe('warning')
    expect(quotaStatusFromString('100')).toBe('error')
    expect(quotaStatusFromString(null)).toBe('success')
  })
})
