/** 账单金额/桶标识的展示换算（后端金额一律是 1e-6 币种单位的整数 micros） */

import { formatMoney } from './format'

const MICROS = 1_000_000

/**
 * micros 整数 → 十进制金额串：至少两位小数，多余尾零去掉但保留精度
 * （12340000 → "12.34"，12345000 → "12.345"，1200000 → "1.20"）。
 * 全程整数运算，不走浮点除法。
 */
export function microsToAmount(micros: number): string {
  const neg = micros < 0
  const abs = Math.abs(micros)
  const int = Math.floor(abs / MICROS)
  const frac = String(abs % MICROS).padStart(6, '0').replace(/0+$/, '')
  const fracPadded = frac.length === 0 ? '00' : frac.length === 1 ? frac + '0' : frac
  return (neg ? '-' : '') + int + '.' + fracPadded
}

/** micros + 币种 → "¥12.34"（复用余额面板的币种符号规则） */
export function formatBillMoney(micros: number, currency: string): string {
  return formatMoney(microsToAmount(micros), currency)
}

/**
 * 桶标识 → 图表横轴短标签："2026-10-29" → "10/29"；"2026-10" / "2026" 桶宽
 * 足够宽，原样展示。
 */
export function bucketLabel(bucket: string): string {
  if (bucket.length === 10) {
    const [, m, d] = bucket.split('-')
    return `${Number(m)}/${Number(d)}`
  }
  return bucket
}

/** 本地时间戳 → "YYYY-MM-DD"（账单接口的日期参数格式，按本地日） */
export function dateParam(ts: number): string {
  const d = new Date(ts)
  const p = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

/** 记账表单的金额校验：合法正金额回 null，否则回错误文案（与后端口径一致） */
export function amountInputError(s: string): string | null {
  const t = s.trim()
  if (!/^\d+(\.\d{1,6})?$/.test(t)) return '金额格式不正确（最多 6 位小数）'
  if (Number(t) === 0) return '金额必须大于 0'
  return null
}

/**
 * 自动账单 detail JSON 的可读摘要："余额 110.00 → 108.35"。
 * 解析失败（历史/异常数据）回空串，调用方按无 detail 处理。
 */
export function billDetailText(detail?: string): string {
  if (!detail) return ''
  try {
    const d = JSON.parse(detail) as { prev_total?: string; new_total?: string }
    if (!d.prev_total || !d.new_total) return ''
    return `余额 ${d.prev_total} → ${d.new_total}`
  } catch {
    return ''
  }
}
