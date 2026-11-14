import client from './client'

export interface AliasBinding {
  id?: number
  alias_id?: number
  upstream_id: number
  upstream_name?: string
  upstream_enabled?: boolean
  model_name: string
  priority: number
  /** 所属配置名（同一别名可存多套命名配置）；旧接口/旧数据可能缺省，按 default 处理 */
  profile?: string
}

export interface ModelAlias {
  id: number
  name: string
  /** 当前生效的配置名，网关只解析这一套绑定；旧接口/旧数据缺省时按 default 处理 */
  active_profile?: string
  bindings: AliasBinding[]
  created_at?: string
  updated_at?: string
}

export interface AliasBindingInput {
  upstream_id: number
  model_name: string
  profile: string
}

export async function listAliases() {
  const { data } = await client.get('/aliases')
  return data.data as ModelAlias[]
}

export async function createAlias(name: string, activeProfile: string, bindings: AliasBindingInput[]) {
  const { data } = await client.post('/aliases', { name, active_profile: activeProfile, bindings })
  return data as ModelAlias
}

export async function updateAlias(id: number, patch: { name?: string; active_profile?: string; bindings?: AliasBindingInput[] }) {
  const { data } = await client.put(`/aliases/${id}`, patch)
  return data as ModelAlias
}

/** 一键切换别名的生效配置（只改指针，不重写绑定） */
export async function activateAliasProfile(id: number, profile: string) {
  const { data } = await client.post(`/aliases/${id}/activate`, { profile })
  return data as ModelAlias
}

export async function deleteAlias(id: number) {
  await client.delete(`/aliases/${id}`)
}
