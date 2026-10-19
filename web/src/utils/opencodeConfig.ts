/**
 * opencode 配置生成器。
 *
 * 与 Keys 页的其余客户端导出器（OMP / pi / dsh / claude-code，见各 utils 文件）
 * 配套：生成指向本网关（any-llm）的 opencode 自定义提供方配置 —— 模型 id 为
 * upstream-name/model-name（与 /v1/models 一致），ext key 明文嵌入
 * options.apiKey。此前是 Keys.vue 里的本地实现，AgentConfigDrawer 也要用，
 * 抽到这里共用一份。
 *
 * 输出是纯 JSON、不带注释头：opencode.json 会被整体解析，注释会让
 * JSON.parse 直接失败（与 pi 导出同理）。已有配置时把 provider 下的
 * any-llm 段合并进去即可。模型统一收进 models 映射并附 limit
 * （context / output）：opencode 用它做上下文裁剪，网关未录容量时原样
 * 输出 0 交给 opencode 的默认行为。
 */
import type { ExportModel } from './exportModels'

export interface OpencodeConfigOptions {
  /** 网关地址，如 http://localhost:6718/v1（含 /v1；SDK 自拼 /chat/completions） */
  baseUrl: string
  /** ext key 明文（options.apiKey 字段支持字面值） */
  apiKey: string
  models: ExportModel[]
}

/** 生成可合并进 ~/.config/opencode/opencode.json 的完整 JSON（provider 固定为 any-llm） */
export function buildOpencodeJson(opts: OpencodeConfigOptions): string {
  const models: Record<string, { name: string; limit: { context: number; output: number } }> = {}
  for (const m of opts.models) {
    models[m.id] = { name: m.id, limit: { context: m.contextLength, output: m.maxOutputLength } }
  }
  const cfg = {
    $schema: 'https://opencode.ai/config.json',
    provider: {
      'any-llm': {
        npm: '@ai-sdk/openai-compatible',
        name: 'any-llm',
        options: {
          baseURL: opts.baseUrl,
          apiKey: opts.apiKey,
        },
        models,
      },
    },
  }
  return JSON.stringify(cfg, null, 2)
}
