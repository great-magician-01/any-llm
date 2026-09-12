/** 余额/额度快照的展示逻辑（经典/毛玻璃两套 Upstreams 视图共用） */

import type { BalancePayload, BalanceSnapshot } from '../api/balances'
import { formatMoney, formatTime } from './format'

export const QUOTA_WINDOW_LABELS: Record<string, string> = { five_hour: '5h', weekly: '周', monthly: '月' }

// 窗口的固定展示顺序；未知 id 按原顺序排在后面
const WINDOW_ORDER = ['five_hour', 'weekly', 'monthly']

// 厂商会省略当前无活动的窗口（kimi 的 5h 窗口只在使用期返回）；这些窗口缺省时
// 单元格显示占位而不是彻底消失，避免"只剩周用量"看起来像 bug。
// 注意：只用于最新快照单元格；历史弹窗展示快照实际内容，不补占位。
const PLACEHOLDER_WINDOWS: Record<string, string[]> = { 'kimi-coding': ['five_hour'] }

export function parsePayload(s: BalanceSnapshot): BalancePayload | null {
  if (!s?.payload) return null
  if (typeof s.payload === 'string') {
    try { return JSON.parse(s.payload) as BalancePayload } catch { return null }
  }
  return s.payload
}

/** 重置时间紧凑格式：5h 窗只有时刻 "03:02"，周/月窗带日期 "9-17"；非法/空返回 '' */
function compactReset(resetAt: string | undefined, windowId: string): string {
  if (!resetAt) return ''
  const d = new Date(resetAt)
  if (isNaN(d.getTime())) return ''
  const p = (x: number) => String(x).padStart(2, '0')
  const hm = `${p(d.getHours())}:${p(d.getMinutes())}`
  return windowId === 'five_hour' ? hm : `${d.getMonth() + 1}-${d.getDate()}`
}

/** 快照抓取时间：当天只显示 "HH:mm:ss"，跨天带 "M-d " 前缀 */
export function formatFetchedAt(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  const p = (x: number) => String(x).padStart(2, '0')
  const hm = `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
  return d.toDateString() === new Date().toDateString() ? hm : `${d.getMonth() + 1}-${d.getDate()} ${hm}`
}

/** 单元格展示用的单窗口视图；percent 为 null 表示厂商本次未返回该窗口（占位行） */
export interface QuotaWindowView {
  id: string
  label: string
  percent: string | null
  reset: string
  missing: boolean
}

export type BalanceView =
  | { kind: 'balance'; text: string }
  | { kind: 'quota'; windows: QuotaWindowView[] }

/** 最新快照单元格的结构化视图；无快照/无法解析返回 null */
export function balanceView(s: BalanceSnapshot | undefined): BalanceView | null {
  if (!s) return null
  const p = parsePayload(s)
  if (!p) return null
  if (p.kind === 'balance') {
    const b = p.balances?.[0]
    if (!b) return null
    return { kind: 'balance', text: formatMoney(b.total, b.currency) }
  }
  const windows = (p.windows || [])
    .map((w): QuotaWindowView => ({
      id: w.id,
      label: QUOTA_WINDOW_LABELS[w.id] ?? w.id,
      percent: w.used_percent,
      reset: compactReset(w.reset_at, w.id),
      missing: false,
    }))
    .sort((a, b) => orderIndex(a.id) - orderIndex(b.id))
  // 一个窗口都没有说明厂商响应异常，不补占位（避免把异常伪装成"无活动窗口"）
  if (!windows.length) return null
  for (const id of PLACEHOLDER_WINDOWS[s.vendor] ?? []) {
    if (!windows.some(w => w.id === id)) {
      windows.splice(orderIndex(id), 0, { id, label: QUOTA_WINDOW_LABELS[id] ?? id, percent: null, reset: '', missing: true })
    }
  }
  return { kind: 'quota', windows }
}

function orderIndex(id: string): number {
  const i = WINDOW_ORDER.indexOf(id)
  return i === -1 ? WINDOW_ORDER.length : i
}

/** 单行摘要（历史弹窗用，只含快照实际返回的窗口）："5h 12%（03:02 重置）· 周 34%（9-17 重置）" */
export function balanceSummary(s: BalanceSnapshot | undefined): string | null {
  const v = balanceView(s)
  if (!v) return null
  if (v.kind === 'balance') return v.text
  const parts = v.windows.flatMap(w => {
    if (w.percent === null) return [] // 占位窗口不进单行摘要
    return [w.reset ? `${w.label} ${w.percent}%（${w.reset} 重置）` : `${w.label} ${w.percent}%`]
  })
  return parts.length ? parts.join(' · ') : null
}

/** 单元格 tooltip：每个窗口的完整重置时间 + 抓取时间 */
export function balanceTooltip(s: BalanceSnapshot): string {
  const lines: string[] = []
  const p = parsePayload(s)
  if (p?.kind === 'quota') {
    for (const w of p.windows || []) {
      const label = QUOTA_WINDOW_LABELS[w.id] ?? w.id
      lines.push(w.reset_at ? `${label} ${w.used_percent}%，重置于 ${formatTime(w.reset_at)}` : `${label} ${w.used_percent}%`)
    }
  }
  if (lines.some(l => l.includes('5h'))) {
    lines.push('（厂商仅在有活跃用量时返回 5h 窗口）')
  } else if (s.vendor === 'kimi-coding') {
    lines.push('5h 窗口本次未返回，通常表示当前无活跃的 5 小时用量窗口')
  }
  lines.push(`抓取于 ${formatTime(s.created_at)}`, '点击查看历史')
  return lines.join('\n')
}
