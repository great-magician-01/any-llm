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

// 连通性测试结果（与后端 upstream.TestResult 对应）：reachable=false 是网络层
// 失败；reachable=true 而 ok=false 是收到了应答但非 2xx（401/403 多为 key 无效，
// 404 多为该端点没有模型列表）；ok=true 时 models 为模型数。
export interface ConnectivityTestResult {
  ok: boolean
  reachable: boolean
  latency_ms: number
  status?: number
  models?: number
  detail?: string
}

// 测未保存的表单配置（新增上游时用，后端不查库）
export async function testUpstreamConfig(u: { base_url: string; api_key: string; format: string }) {
  const { data } = await client.post('/upstreams/test', u)
  return data as ConnectivityTestResult
}

// 测已保存的上游；override 带表单里的当前值（api_key 为空或掩码时后端沿用库存真 key）
export async function testUpstream(id: number, override?: { base_url?: string; api_key?: string; format?: string }) {
  const { data } = await client.post(`/upstreams/${id}/test`, override ?? {})
  return data as ConnectivityTestResult
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
