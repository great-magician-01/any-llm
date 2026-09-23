import client from './client'

export interface Upstream {
  id?: number; name: string; base_url: string; api_key: string; format: string
  /** 选填备注，仅管理端元数据（列表展示/配置导出用），网关路由与转发不读它 */
  remark?: string
  enabled: boolean
  daily_token_limit: number; monthly_token_limit: number
  max_concurrent: number
  /** 有效期截止时刻（ISO 带偏移）；null/缺省 = 永久有效。到期后网关侧等同禁用。 */
  expires_at?: string | null
  model_count?: number
  created_at?: string; updated_at?: string
}

export interface UpstreamModel {
  id: number; upstream_id: number; model_name: string; manual: boolean
  context_length: number; max_output_length: number
  /** 是否多模态（接受图片等非文本输入）；默认 false。仅管理端配置/展示。 */
  multimodal: boolean
}

// 上游列表的启用状态过滤；缺省（不传）后端返回全量——Dashboard/Keys/Aliases
// 等页面依赖全量口径，只有上游管理页默认传 'enabled'。
export type UpstreamStatusFilter = 'enabled' | 'disabled' | 'all'

export async function listUpstreams(status?: UpstreamStatusFilter) {
  // axios 的默认序列化器会丢掉 undefined，不缺省判空分支
  const { data } = await client.get('/upstreams', { params: { status } })
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

export const DEFAULT_MODEL_CONTEXT_LENGTH = 1000000
export const DEFAULT_MODEL_MAX_OUTPUT_LENGTH = 200000

export async function addModel(upstreamId: number, model_name: string, context_length = DEFAULT_MODEL_CONTEXT_LENGTH, max_output_length = DEFAULT_MODEL_MAX_OUTPUT_LENGTH, multimodal = false) {
  await client.post(`/upstreams/${upstreamId}/models`, { model_name, context_length, max_output_length, multimodal })
}

export async function updateModel(upstreamId: number, modelId: number, context_length: number, max_output_length: number, multimodal = false) {
  await client.put(`/upstreams/${upstreamId}/models/${modelId}`, { context_length, max_output_length, multimodal })
}

export async function deleteModel(upstreamId: number, modelId: number) {
  await client.delete(`/upstreams/${upstreamId}/models/${modelId}`)
}

export interface UsageTotals { daily_tokens: number; monthly_tokens: number }

export async function getUpstreamUsage(id: number) {
  const { data } = await client.get(`/usage/upstream/${id}`)
  return data as UsageTotals
}
