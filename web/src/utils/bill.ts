/** 账单金额/桶标识的展示换算（后端金额一律是 1e-6 币种单位的整数 micros） */

import { formatMoney, localISO } from './format'

const MICROS = 1_000_000n

/**
 * micros 十进制整数字符串 → 十进制金额串：至少两位小数，多余尾零去掉但保留精度
 * （"12340000" → "12.34"，"12345000" → "12.345"，"1200000" → "1.20"）。
 * 全程 BigInt 运算，超过 JS number 安全整数范围的 int64 也不丢精度。
 */
export function microsToAmount(micros: string): string {
  const value = BigInt(micros)
  const neg = value < 0n
  const abs = neg ? -value : value
  const int = abs / MICROS
  const frac = String(abs % MICROS).padStart(6, '0').replace(/0+$/, '')
  const fracPadded = frac.length === 0 ? '00' : frac.length === 1 ? frac + '0' : frac
  return (neg ? '-' : '') + int + '.' + fracPadded
}

/** micros + 币种 → "¥12.34"（复用余额面板的币种符号规则） */
export function formatBillMoney(micros: string, currency: string): string {
  return formatMoney(microsToAmount(micros), currency)
}

/**
 * 桶标识/日期 → 图表横轴短标签："2026-10-29"（或带时间的 ISO 串）→ "10/29"；
 * "2026-10" / "2026" 桶宽足够宽，原样展示。直接取字符串前缀而不是 new Date()
 * 换算：桶标识是服务端本地日，过一遍浏览器时区会在跨时区浏览器上偏一天
 * （Usage 页历史上就是这么偏的）。
 */
export function bucketLabel(bucket: string): string {
  const day = bucket.length > 10 ? bucket.slice(0, 10) : bucket
  if (day.length === 10) {
    const [, m, d] = day.split('-')
    return `${Number(m)}/${Number(d)}`
  }
  return day
}

/** 本地时间戳 → "YYYY-MM-DD"（账单接口的日期参数格式，按本地日）。
 * 复用 localISO 的补零/偏移实现，不再养一份；非法时间戳回空串。 */
export function dateParam(ts: number): string {
  const d = new Date(ts)
  if (isNaN(d.getTime())) return ''
  return localISO(d).slice(0, 10)
}

/**
 * 记账表单的金额校验：合法正金额回 null，否则回错误文案。
 * 语法与后端 microsFromDecimal 同口径（'.5'、'12.' 合法）；收紧到 6 位小数是
 * 表单的 UX 决定（后端对更多位会四舍五入到 micros）。
 */
export function amountInputError(s: string): string | null {
  const t = s.trim()
  // 数字部分必填：'.5'、'12.'、'+7' 与后端同口径合法，空串/裸符号/字母拒绝
  if (!/^[+-]?(\d+(\.\d{0,6})?|\.\d{1,6})$/.test(t)) return '金额格式不正确'
  if (t.startsWith('-') || !/[1-9]/.test(t)) return '金额必须大于 0'
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

/** 按币种归并金额行 → "¥31.65"（单币种）或 "¥100.00 + $50.00"（混币种）。
 * 后端按币种拆行正是因为跨币种加总没有意义，展示侧同样不假装能加。 */
export function perCurrencyLabel(rows: { currency: string; amount_micros: string }[]): string {
  const byCur = new Map<string, bigint>()
  for (const r of rows) {
    byCur.set(r.currency, (byCur.get(r.currency) ?? 0n) + BigInt(r.amount_micros))
  }
  return [...byCur.entries()]
    .sort((a, b) => a[1] === b[1] ? 0 : a[1] < b[1] ? 1 : -1)
    .map(([currency, micros]) => formatBillMoney(micros.toString(), currency))
    .join(' + ')
}

/** micros 占比 → 一位小数百分比；BigInt 先放大再取整，避免大 int64 先经 number。 */
export function formatBillPercent(part: string, total: string): string {
  const numerator = BigInt(part)
  const denominator = BigInt(total)
  if (denominator <= 0n) return '—'
  // 放大了 1000 倍（100% × 10），加半个分母实现一位小数的四舍五入。
  const tenths = (numerator * 1000n + denominator / 2n) / denominator
  return `${tenths / 10n}.${tenths % 10n}%`
}
