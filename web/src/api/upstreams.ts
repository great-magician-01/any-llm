import client from './client'

export interface Upstream {
  id?: number; name: string; base_url: string; api_key: string; format: string
  created_at?: string; updated_at?: string
}

export interface UpstreamModel {
  id: number; upstream_id: number; model_name: string; manual: boolean
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

export async function addModel(upstreamId: number, model_name: string) {
  await client.post(`/upstreams/${upstreamId}/models`, { model_name })
}

export async function deleteModel(modelId: number) {
  await client.delete(`/upstreams/${modelId}/models`)
}
