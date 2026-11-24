import client from './client'
import type { UsageTotals } from './types'

export interface UsageOverviewTotals {
  request_count: number; total_tokens: number; ok_count: number; error_count: number
}

export interface UsageOverviewModel extends UsageOverviewTotals {
  group_key: string
}

export interface UsageOverviewUpstream extends UsageTotals {
  upstream_id: number
}

export interface UsageOverview {
  today: UsageOverviewTotals
  month: UsageOverviewTotals
  all_time: UsageOverviewTotals
  models: UsageOverviewModel[]
  daily: UsageDayStat[]
  upstream_totals: UsageOverviewUpstream[]
}

export interface UsageSummary {
  group_key: string; request_count: number; total_tokens: number
  prompt_tokens: number; completion_tokens: number; ok_count: number; error_count: number
  avg_tokens_per_sec: number
  // 仅 group_by=key 时存在：同名 key（软删后重建）会产生相同的 group_key，
  // 靠它区分行/回链密钥
  ext_key_id?: number | null
}

export interface UsageRecord {
  id: number; upstream_name: string; model: string; in_format: string; up_format: string
  prompt_tokens: number; completion_tokens: number; total_tokens: number
  cache_read_tokens: number; cache_creation_tokens: number; reasoning_tokens: number
  duration_ms: number; stream: boolean; status: string; created_at: string
  error_code: number; error_body: string
}

export interface UsageDayStat {
  day: string; request_count: number; total_tokens: number
  prompt_tokens: number; completion_tokens: number
  cache_read_tokens: number; cache_creation_tokens: number; reasoning_tokens: number
  ok_count: number; error_count: number
}

export async function fetchSummary(groupBy: string, from?: string, to?: string) {
  const { data } = await client.get('/usage/summary', { params: { group_by: groupBy, from, to } })
  return data.data as UsageSummary[]
}

export async function fetchDaily(days: number, from?: string, to?: string) {
  const { data } = await client.get('/usage/daily', { params: { days, from, to } })
  return data.data as UsageDayStat[]
}

/** 概览专用聚合：三窗口汇总 + 本月模型 Top + 370 天日历 + 全部上游日/月用量。 */
export async function fetchUsageOverview() {
  const { data } = await client.get('/usage/overview')
  return data as UsageOverview
}

export async function fetchRecords(page: number, size: number, model?: string) {
  const { data } = await client.get('/usage/records', { params: { page, size, model } })
  return data as { data: UsageRecord[]; total: number }
}

// 请求明细「按模型筛选」下拉框的选项：usage_records 里实际出现过的模型名
// （去重有序，含历史模型——旧记录也要能被筛出来）
export async function fetchUsageModels() {
  const { data } = await client.get('/usage/models')
  return data.data as string[]
}
