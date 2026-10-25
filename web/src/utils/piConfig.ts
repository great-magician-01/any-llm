/**
 * Pi agent（pi coding agent，~/.pi/agent/models.json）配置生成器。
 *
 * 与 Keys.vue 的 opencode / OMP / dsh 复制功能配套：生成指向本网关
 * （any-llm）的 pi 自定义提供方配置 —— 模型 id 为 upstream-name/model-name
 * （与 /v1/models 一致），ext key 明文嵌入 apiKey（pi 的 apiKey 字段支持
 * 字面值，也可改成环境变量名或 `!命令`）。
 *
 * 输出是纯 JSON、不带注释头：models.json 会被 pi 整体解析，注释会让
 * JSON.parse 直接失败（与 opencode 导出同理）。已有 models.json 时把
 * providers 下的 any-llm 段合并进去即可。
 *
 * 按约定不做按模型名的启发式识别：与 OMP 一致，所有模型统一按推理模型
 * 处理（reasoning: true，思考档位走 pi 默认映射，reasoning_effort 会被
 * 网关透传给上游）。api 固定 openai-completions：pi-ai 的该实现用 OpenAI
 * SDK 发请求，apiKey 会自动带 Authorization: Bearer，无需 authHeader。
 * contextWindow / maxTokens 只在网关录了值（> 0）时输出，缺省交给 pi 的
 * 默认值（128000 / 16384）。
 */

export interface PiModel {
  id: string
  contextWindow: number
  maxTokens: number
}

export interface PiConfigOptions {
  /** 网关地址，如 http://localhost:6718/v1（含 /v1；openai-completions 只会拼接 /chat/completions） */
  baseUrl: string
  /** ext key 明文（apiKey 字段支持字面值） */
  apiKey: string
  models: PiModel[]
}

interface PiModelEntry {
  id: string
  name: string
  reasoning: boolean
  input: string[]
  contextWindow?: number
  maxTokens?: number
}

/** 生成可粘贴进 ~/.pi/agent/models.json 的完整 JSON（provider 固定为 any-llm） */
export function buildPiJson(opts: PiConfigOptions): string {
  const models: PiModelEntry[] = opts.models.map((m) => {
    const entry: PiModelEntry = {
      id: m.id,
      name: m.id,
      reasoning: true,
      input: ['text'],
    }
    if (m.contextWindow > 0) entry.contextWindow = m.contextWindow
    if (m.maxTokens > 0) entry.maxTokens = m.maxTokens
    return entry
  })
  const cfg = {
    providers: {
      'any-llm': {
        baseUrl: opts.baseUrl,
        api: 'openai-completions',
        apiKey: opts.apiKey,
        models,
      },
    },
  }
  return JSON.stringify(cfg, null, 2) + '\n'
}
