/** 数字格式化工具 */

/** 1234567 -> "1,234,567" */
export function formatInt(n: number): string {
  return Math.round(n).toLocaleString('en-US')
}

/** 1234567 -> "1.2M"，12345 -> "12.3k"，小数字原样带千分位 */
export function formatCompact(n: number): string {
  const abs = Math.abs(n)
  if (abs >= 1_000_000_000) return trim1(n / 1_000_000_000) + 'B'
  if (abs >= 1_000_000) return trim1(n / 1_000_000) + 'M'
  if (abs >= 10_000) return trim1(n / 1_000) + 'k'
  return formatInt(n)
}

function trim1(v: number): string {
  const s = v.toFixed(1)
  return s.endsWith('.0') ? s.slice(0, -2) : s
}

/** 0.853 -> "85.3%"，无样本时返回 "—" */
export function formatPercent(part: number, total: number): string {
  if (total <= 0) return '—'
  return ((part / total) * 100).toFixed(1) + '%'
}

/** Date -> 本地 RFC3339 "YYYY-MM-DDTHH:mm:ss±HH:MM"（带时区偏移，后端按绝对时刻解析） */
export function localISO(d: Date): string {
  const p = (x: number) => String(x).padStart(2, '0')
  const off = -d.getTimezoneOffset()
  const sign = off < 0 ? '-' : '+'
  const abs = Math.abs(off)
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}${sign}${p(Math.floor(abs / 60))}:${p(abs % 60)}`
}

/** 后端 created_at -> "YYYY-MM-DD HH:mm:ss" */
export function formatTime(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  const p = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
