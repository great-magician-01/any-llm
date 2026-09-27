/**
 * format.ts 的 7 个格式化函数。
 *
 * 这些函数是页面文案的最后一道：上游用量、余额、速度、时间都从它们过。它们同时
 * 还是 balance.test.ts 的「oracle」——本身没测试的话，oracle 与被测代码一起错也
 * 不会被发现。
 *
 * 时间相关的断言都用「本地构造 → 转 ISO → 再解析」的往返方式，避免绑死运行时时区。
 */
import { describe, expect, it } from 'vitest'
import { formatCompact, formatInt, formatMoney, formatPercent, formatSpeed, formatTime, localISO } from './format'

describe('formatInt', () => {
  it('千分位分隔并四舍五入到整数', () => {
    expect(formatInt(1234567)).toBe('1,234,567')
    expect(formatInt(999)).toBe('999')
    expect(formatInt(0)).toBe('0')
    // 表格里的计数列不接受小数（0.5 也要进位成 1）
    expect(formatInt(1.4)).toBe('1')
    expect(formatInt(1.5)).toBe('2')
  })

  it('负数照常带千分位', () => {
    expect(formatInt(-1234)).toBe('-1,234')
  })
})

describe('formatCompact', () => {
  it('按量级选 B / M / k，一位小数且去掉结尾的 .0', () => {
    expect(formatCompact(999)).toBe('999')
    expect(formatCompact(9999)).toBe('9,999')
    expect(formatCompact(10000)).toBe('10k')
    expect(formatCompact(12345)).toBe('12.3k')
    expect(formatCompact(1000000)).toBe('1M')
    expect(formatCompact(1234567)).toBe('1.2M')
    expect(formatCompact(1000000000)).toBe('1B')
    expect(formatCompact(1234567890)).toBe('1.2B')
  })

  it('四舍五入会跨档（999999 → 1000k 而不是 1M）', () => {
    // 档位按原值选、小数在选完档之后才取整：不能出现 "1000k" 之外的怪值
    expect(formatCompact(999999)).toBe('1000k')
    expect(formatCompact(9999999)).toBe('10M')
  })

  it('负数按绝对值选档', () => {
    // token 差值等场景会出现负数，展示上不能丢掉量级
    expect(formatCompact(-12345)).toBe('-12.3k')
    expect(formatCompact(-1234567)).toBe('-1.2M')
  })
})

describe('formatPercent', () => {
  it('保留一位小数的百分比', () => {
    expect(formatPercent(853, 1000)).toBe('85.3%')
    expect(formatPercent(1, 3)).toBe('33.3%')
    expect(formatPercent(0, 5)).toBe('0.0%')
  })

  it('没有分母时给占位符而不是 NaN/Infinity', () => {
    expect(formatPercent(5, 0)).toBe('—')
    expect(formatPercent(5, -1)).toBe('—')
  })

  it('超过 100% 也如实显示（用于发现配额异常）', () => {
    // 不 clamp：真实超标要能被看出来，而不是悄悄显示成 100%
    expect(formatPercent(12, 10)).toBe('120.0%')
  })
})

describe('formatSpeed', () => {
  it('token/s 保留一位小数', () => {
    expect(formatSpeed(452, 10000)).toBe('45.2')
    expect(formatSpeed(100, 1000)).toBe('100.0')
  })

  it('耗时为 0 或没有输出时给占位符', () => {
    // 耗时缺失（0/负）时不能算出 Infinity，没有输出也不能算出 0.0
    expect(formatSpeed(100, 0)).toBe('—')
    expect(formatSpeed(100, -5)).toBe('—')
    expect(formatSpeed(0, 1000)).toBe('—')
  })
})

describe('localISO', () => {
  it('输出带时区偏移的本地 RFC3339', () => {
    const d = new Date(2026, 0, 2, 3, 4, 5)
    expect(localISO(d)).toMatch(/^2026-01-02T03:04:05[+-]\d{2}:\d{2}$/)
  })

  it('解析回来是同一时刻（后端按绝对时刻入库）', () => {
    const d = new Date(2026, 8, 17, 23, 59, 58)
    expect(Date.parse(localISO(d))).toBe(d.getTime())
  })

  it('月/日/时/分/秒都补零', () => {
    // 后端按绝对时刻解析，个位数的月/日/时/分/秒漏补零会导致解析歧义
    const s = localISO(new Date(2026, 0, 1, 0, 0, 0))
    expect(s.startsWith('2026-01-01T00:00:00')).toBe(true)
  })
})

describe('formatMoney', () => {
  it('CNY/USD 用符号，其他币种用代码前缀', () => {
    expect(formatMoney('110.00', 'CNY')).toBe('¥110.00')
    expect(formatMoney('5', 'USD')).toBe('$5')
    expect(formatMoney('10', 'EUR')).toBe('EUR 10')
  })

  it('币种缺失时不加前缀', () => {
    // 老快照可能没有 currency 字段，不能显示成 "undefined110"
    expect(formatMoney('12.5', '')).toBe('12.5')
  })

  it('金额按原字符串拼接（不经过浮点，避免精度丢失）', () => {
    // 后端给的是字符串金额：转 Number 再格式化会改写事实
    expect(formatMoney('0.10000000000000001', 'USD')).toBe('$0.10000000000000001')
  })
})

describe('formatTime', () => {
  it('转成本地 YYYY-MM-DD HH:mm:ss 并补零', () => {
    const d = new Date(2026, 0, 2, 3, 4, 5)
    expect(formatTime(d.toISOString())).toBe('2026-01-02 03:04:05')
  })

  it('无法解析时原样返回（宁可显示原串也不要 Invalid Date）', () => {
    // 后端时间格式变动时，用户至少还能看到原始值而不是 "Invalid Date"
    expect(formatTime('not-a-date')).toBe('not-a-date')
    expect(formatTime('')).toBe('')
  })
})
