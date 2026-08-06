import client from './client'

export interface UsageSummary {
  group_key: string; request_count: number; total_tokens: number
  prompt_tokens: number; completion_tokens: number; ok_count: number; error_count: number
}

export interface UsageRecord {
  id: number; upstream_name: string; model: string; in_format: string; up_format: string
  prompt_tokens: number; completion_tokens: number; total_tokens: number
  cache_read_tokens: number; cache_creation_tokens: number; reasoning_tokens: number
  stream: boolean; status: string; created_at: string
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

export async function fetchRecords(page: number, size: number) {
  const { data } = await client.get('/usage/records', { params: { page, size } })
  return data as { data: UsageRecord[]; total: number }
}
