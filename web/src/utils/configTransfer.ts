import type { ConfigFile, ImportResult } from '../api/config'

const CONFIG_VERSION = 1

/** 导出文件名：any-llm-config-YYYYMMDD-HHmmss.json（本地时间） */
export function configFileName(now: Date): string {
  const p = (n: number) => String(n).padStart(2, '0')
  const date = `${now.getFullYear()}${p(now.getMonth() + 1)}${p(now.getDate())}`
  const time = `${p(now.getHours())}${p(now.getMinutes())}${p(now.getSeconds())}`
  return `any-llm-config-${date}-${time}.json`
}

/** 解析并做基本结构校验，错误信息面向用户（中文）。 */
export function parseConfigFile(text: string): ConfigFile {
  let data: any
  try {
    data = JSON.parse(text)
  } catch {
    throw new Error('不是有效的 JSON 文件')
  }
  if (typeof data !== 'object' || data === null || Array.isArray(data)) {
    throw new Error('配置文件格式不正确')
  }
  if (typeof data.version === 'number' && data.version > CONFIG_VERSION) {
    throw new Error(`不支持的配置文件版本 ${data.version}（当前支持 v${CONFIG_VERSION}）`)
  }
  if (!Array.isArray(data.upstreams) || !Array.isArray(data.aliases)) {
    throw new Error('配置文件缺少 upstreams / aliases 字段')
  }
  return data as ConfigFile
}

/** 导入确认弹窗里的摘要：文件内容 + 覆盖语义提醒。 */
export function describeConfigFile(f: ConfigFile): string {
  return `该文件包含 ${f.upstreams.length} 个上游、${f.aliases.length} 个别名。` +
    '导入后：同名配置将被覆盖，其余现有配置保持不变。' +
    '注意：地址、API Key、格式等基础字段以文件为准（缺省会清空原值），启用状态与限额缺省则保持现状。'
}

/** 导入完成后的结果摘要。 */
export function describeImportResult(r: ImportResult): string {
  return `导入完成：上游新建 ${r.upstreams_created}、覆盖 ${r.upstreams_updated}；` +
    `别名新建 ${r.aliases_created}、覆盖 ${r.aliases_updated}、跳过 ${r.aliases_skipped}、丢弃绑定 ${r.bindings_dropped}`
}

/** 触发浏览器下载一段 JSON（pretty 打印，便于人工查看/编辑）。 */
export function downloadJSON(data: unknown, filename: string): void {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}
