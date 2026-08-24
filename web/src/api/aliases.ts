import client from './client'

export interface AliasBinding {
  id?: number
  alias_id?: number
  upstream_id: number
  upstream_name?: string
  model_name: string
  priority: number
}

export interface ModelAlias {
  id: number
  name: string
  bindings: AliasBinding[]
  created_at?: string
  updated_at?: string
}

export async function listAliases() {
  const { data } = await client.get('/aliases')
  return data.data as ModelAlias[]
}

export async function createAlias(name: string, bindings: Array<{ upstream_id: number; model_name: string }>) {
  const { data } = await client.post('/aliases', { name, bindings })
  return data as ModelAlias
}

export async function updateAlias(id: number, patch: { name?: string; bindings?: Array<{ upstream_id: number; model_name: string }> }) {
  const { data } = await client.put(`/aliases/${id}`, patch)
  return data as ModelAlias
}

export async function deleteAlias(id: number) {
  await client.delete(`/aliases/${id}`)
}
