import client from './client'

export interface BalanceEntry {
  currency: string; total: string; granted: string; topped_up: string
}

export interface QuotaWindow {
  id: string; used_percent: string; reset_at?: string
}

/** 归一化余额/额度 payload（后端存 JSON 文本，序列化后可能是对象或字符串） */
export type BalancePayload =
  | { kind: 'balance'; is_available: boolean; balances: BalanceEntry[] }
  | { kind: 'quota'; windows: QuotaWindow[] }

export interface BalanceSnapshot {
  id: number; upstream_id: number; upstream_name: string; vendor: string
  payload: BalancePayload | string
  created_at: string
}

/** 各 upstream 最新一条余额/额度快照 */
export async function listLatestBalances() {
  const { data } = await client.get('/balances')
  return data.data as BalanceSnapshot[]
}

/** 单 upstream 的历史快照（分页） */
export async function listBalanceHistory(upstreamId: number, page = 1, size = 10) {
  const { data } = await client.get(`/upstreams/${upstreamId}/balances`, { params: { page, size } })
  return { data: data.data as BalanceSnapshot[], total: data.total as number }
}

/** 手动触发一次厂商余额/额度抓取，返回新快照 */
export async function refreshBalance(upstreamId: number) {
  const { data } = await client.post(`/upstreams/${upstreamId}/balances/refresh`)
  return data.data as BalanceSnapshot
}

/** 刷新所有受支持 upstream 的余额/额度（页面打开时的后台自动刷新），返回新快照列表 */
export async function refreshAllBalances() {
  const { data } = await client.post('/balances')
  return data.data as BalanceSnapshot[]
}
