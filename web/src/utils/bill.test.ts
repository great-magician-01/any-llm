import { describe, expect, it } from 'vitest'
import { microsToAmount, formatBillMoney, bucketLabel, dateParam, amountInputError, billDetailText, perCurrencyLabel } from './bill'

describe('microsToAmount：1e-6 整数金额 → 十进制串', () => {
  it('两位小数（最常见：厂商余额差额）', () => {
    expect(microsToAmount(12_340_000)).toBe('12.34')
    expect(microsToAmount(110_000_000)).toBe('110.00')
    expect(microsToAmount(0)).toBe('0.00')
  })

  it('整数金额补齐两位小数，不为展示丢精度', () => {
    expect(microsToAmount(1_200_000)).toBe('1.20')
    expect(microsToAmount(7_000_000)).toBe('7.00')
  })

  it('保留 micros 粒度（差额有 1e-6 位时原样显示）', () => {
    expect(microsToAmount(12_345_000)).toBe('12.345')
    expect(microsToAmount(5_000)).toBe('0.005')
    expect(microsToAmount(1)).toBe('0.000001')
  })

  it('负数（防御：后端不产负账单，但函数要稳）', () => {
    expect(microsToAmount(-5_000)).toBe('-0.005')
    expect(microsToAmount(-12_340_000)).toBe('-12.34')
  })
})

describe('formatBillMoney：复用余额面板的币种符号规则', () => {
  it('CNY → ¥ 前缀', () => {
    expect(formatBillMoney(12_340_000, 'CNY')).toBe('¥12.34')
  })
  it('USD → $ 前缀', () => {
    expect(formatBillMoney(500_000, 'USD')).toBe('$0.50')
  })
  it('其他币种前缀币种代码', () => {
    expect(formatBillMoney(100_000, 'EUR')).toBe('EUR 0.10')
  })
})

describe('bucketLabel：趋势图横轴短标签', () => {
  it('日桶缩成 M/D', () => {
    expect(bucketLabel('2026-10-29')).toBe('10/29')
    expect(bucketLabel('2026-01-05')).toBe('1/5')
  })
  it('带时间的 ISO 串取日期前缀（服务端本地日，不过浏览器时区）', () => {
    expect(bucketLabel('2026-10-29T08:30:00+08:00')).toBe('10/29')
  })
  it('月/年桶原样展示', () => {
    expect(bucketLabel('2026-10')).toBe('2026-10')
    expect(bucketLabel('2026')).toBe('2026')
  })
})

describe('perCurrencyLabel：按币种各自归并，不跨币种加总', () => {
  it('单币种', () => {
    expect(perCurrencyLabel([{ currency: 'CNY', amount_micros: 12_340_000 }])).toBe('¥12.34')
    expect(
      perCurrencyLabel([
        { currency: 'CNY', amount_micros: 10_000_000 },
        { currency: 'CNY', amount_micros: 2_340_000 },
      ]),
    ).toBe('¥12.34')
  })
  it('混币种：各自成项、金额降序', () => {
    expect(
      perCurrencyLabel([
        { currency: 'USD', amount_micros: 50_000_000 },
        { currency: 'CNY', amount_micros: 100_000_000 },
      ]),
    ).toBe('¥100.00 + $50.00')
  })
  it('空行回空串（调用方显示占位）', () => {
    expect(perCurrencyLabel([])).toBe('')
  })
})

describe('dateParam：本地日 → YYYY-MM-DD', () => {
  it('补零', () => {
    // 用本地时区构造，避免 CI 时区差异
    const ts = new Date(2026, 0, 5).getTime()
    expect(dateParam(ts)).toBe('2026-01-05')
  })
})

describe('amountInputError：记账表单金额校验', () => {
  it('合法正金额回 null（语法与后端 microsFromDecimal 同口径）', () => {
    expect(amountInputError('12.50')).toBeNull()
    expect(amountInputError('0.000001')).toBeNull()
    expect(amountInputError(' 7 ')).toBeNull()
    expect(amountInputError('.5')).toBeNull()
    expect(amountInputError('12.')).toBeNull()
  })
  it('格式错误', () => {
    expect(amountInputError('')).toContain('金额格式')
    expect(amountInputError('abc')).toContain('金额格式')
    expect(amountInputError('1.2345678')).toContain('金额格式')
    expect(amountInputError('1 2')).toContain('金额格式')
  })
  it('非正金额拒绝（负数语法合法但语义拒绝）', () => {
    expect(amountInputError('0')).toContain('大于 0')
    expect(amountInputError('0.00')).toContain('大于 0')
    expect(amountInputError('-5')).toContain('大于 0')
  })
})

describe('billDetailText：自动账单 detail 的可读摘要', () => {
  it('解析前后余额', () => {
    expect(billDetailText('{"prev_total":"110.00","new_total":"108.35"}')).toBe('余额 110.00 → 108.35')
  })
  it('缺字段/坏 JSON/空串回空，调用方按无 detail 处理', () => {
    expect(billDetailText(undefined)).toBe('')
    expect(billDetailText('')).toBe('')
    expect(billDetailText('not-json')).toBe('')
    expect(billDetailText('{"prev_total":"1.00"}')).toBe('')
  })
})
