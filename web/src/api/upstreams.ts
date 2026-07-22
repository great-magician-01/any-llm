import client from './client'

export interface Upstream {
  id?: number; name: string; base_url: string; api_key: string; format: string
  daily_token_limit: number; monthly_token_limit: number
  model_count?: number
  created_at?: string; updated_at?: string
}

export interface UpstreamModel {
  id: number; upstream_id: number; model_name: string; manual: boolean
  context_length: number; max_output_length: number
}

export async function listUpstreams() {
  const { data } = await client.get('/upstreams')
  return data.data as Upstream[]
}

export async function createUpstream(u: Upstream & { fetch_models?: boolean }) {
  const { data } = await client.post('/upstreams', u)
  return data as Upstream
}

export async function updateUpstream(id: number, u: Partial<Upstream>) {
  const { data } = await client.put(`/upstreams/${id}`, u)
  return data as Upstream
}

export async function deleteUpstream(id: number) {
  await client.delete(`/upstreams/${id}`)
}

export async function fetchModels(id: number) {
  const { data } = await client.post(`/upstreams/${id}/fetch-models`)
  return data.models as string[]
}

export async function listModels(upstreamId: number) {
  const { data } = await client.get(`/upstreams/${upstreamId}/models`)
  return data.data as UpstreamModel[]
}

export const DEFAULT_MODEL_LENGTH = 200000

export async function addModel(upstreamId: number, model_name: string, context_length = DEFAULT_MODEL_LENGTH, max_output_length = DEFAULT_MODEL_LENGTH) {
  await client.post(`/upstreams/${upstreamId}/models`, { model_name, context_length, max_output_length })
}

export async function updateModel(upstreamId: number, modelId: number, context_length: number, max_output_length: number) {
  await client.put(`/upstreams/${upstreamId}/models/${modelId}`, { context_length, max_output_length })
}

export async function deleteModel(upstreamId: number, modelId: number) {
  await client.delete(`/upstreams/${upstreamId}/models/${modelId}`)
}

export interface UsageTotals { daily_tokens: number; monthly_tokens: number }

export async function getUpstreamUsage(id: number) {
  const { data } = await client.get(`/usage/upstream/${id}`)
  return data as UsageTotals
}
