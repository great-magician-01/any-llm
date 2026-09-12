import client from './client'

// 配置导出/导入：上游（含真实 API Key 与模型列表）+ 模型别名。
// 绑定按上游名引用，文件可跨实例迁移；导入同名覆盖、不同名保留。

export interface ConfigUpstreamModel {
  model_name: string
  manual: boolean
  context_length: number
  max_output_length: number
}

export interface ConfigUpstream {
  name: string
  base_url: string
  api_key: string
  format: string
  enabled?: boolean
  daily_token_limit?: number
  monthly_token_limit?: number
  models?: ConfigUpstreamModel[]
}

export interface ConfigBinding {
  upstream: string
  model_name: string
}

export interface ConfigAlias {
  name: string
  bindings: ConfigBinding[]
}

export interface ConfigFile {
  version: number
  exported_at?: string
  upstreams: ConfigUpstream[]
  aliases: ConfigAlias[]
}

export interface ImportResult {
  upstreams_created: number
  upstreams_updated: number
  aliases_created: number
  aliases_updated: number
  aliases_skipped: number
  bindings_dropped: number
}

export async function exportConfig() {
  const { data } = await client.get('/config/export')
  return data as ConfigFile
}

export async function importConfig(payload: ConfigFile) {
  const { data } = await client.post('/config/import', payload)
  return data as ImportResult
}
