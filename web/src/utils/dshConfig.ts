/**
 * dsh (DeepSeek Harness) 配置生成器。
 *
 * 与 Keys.vue 的 opencode / OMP 复制功能配套：生成指向本网关（any-llm）的
 * dsh 自定义提供方配置 —— 模型 id 为 upstream-name/model-name（与 /v1/models 一致）。
 *
 * dsh 把路由与凭据分放在两个文件（凭据不内联进路由）：
 * - ~/.dsh/settings.yaml：llm-pi-ai.providers 下的提供方段，密钥只写 apiKeyEnv 引用；
 * - ~/.dsh/.credentials.yaml：扁平的「引用: 密钥」映射，ext key 明文落在这一行。
 * 因此一份剪贴板文本装两个片段，靠段头注释指明各自的粘贴位置。
 *
 * 不声明 reasoningEfforts：网关 IR 不承载 reasoning_effort 一类的推理档位参数，
 * 声明了只会让 dsh 发出被网关静默丢弃的字段；手工声明的模型在 dsh 里不暴露
 * 推理控件，上游模型按自身默认行为推理。contextWindow / maxTokens 只在网关
 * 录了值（> 0）时输出，缺省交给 dsh 的路由回退值（262144 / 32768）。
 */
import { yamlScalar } from './yaml'

export interface DshModel {
  id: string
  contextWindow: number
  maxTokens: number
}

export interface DshConfigOptions {
  /** 网关地址，如 http://localhost:6718/v1（含 /v1；openai-completions 只会拼接 /chat/completions） */
  baseUrl: string
  /** ext key 明文（写入 .credentials.yaml 的那一行） */
  apiKey: string
  models: DshModel[]
}

/** settings.yaml 的 apiKeyEnv 与 .credentials.yaml 的条目名共用的凭据引用 */
export const DSH_API_KEY_ENV = 'ANY_LLM_API_KEY'

const HEADER = `# ============================================================
# dsh (DeepSeek Harness) 配置 — 由 any-llm 生成（指向本网关）
# dsh 把路由与凭据分放在两个文件，两段都要粘贴：
# 1. settings 段合并进 ~/.dsh/settings.yaml（已有 llm-pi-ai 段则
#    只把 providers 下的 any-llm 提供方合并进去）
# 2. credentials 行追加到 ~/.dsh/.credentials.yaml（该文件是扁平的
#    引用: 密钥 映射，不要包进任何层级）
# 模型 id 格式：upstream-name/model-name，与网关 /v1/models 一致
# ============================================================
`

/** 生成 dsh 配置文本（settings.yaml 提供方段 + .credentials.yaml 凭据行） */
export function buildDshYaml(opts: DshConfigOptions): string {
  const provider = [
    'llm-pi-ai:',
    '  providers:',
    '    any-llm:',
    '      displayName: any-llm',
    `      apiKeyEnv: ${DSH_API_KEY_ENV}`,
    '      api: openai-completions',
    `      baseURL: ${yamlScalar(opts.baseUrl)}`,
  ]
  let settings: string[]
  if (opts.models.length === 0) {
    // dsh 在写入时拒绝不含模型的自定义提供方，整段保持注释，粘贴后是 no-op。
    settings = [
      '# 暂无模型：dsh 拒绝不含模型的自定义提供方，以下整段保持注释。',
      '# 请先在 any-llm 上游添加模型，然后重新复制本配置。',
      ...provider.map((l) => '# ' + l),
      '#       models:',
      '#         - id: upstream-name/model-name',
    ]
  } else {
    settings = [...provider, '      models:']
    for (const m of opts.models) {
      settings.push(`        - id: ${yamlScalar(m.id)}`, `          name: ${yamlScalar(m.id)}`)
      if (m.contextWindow > 0) settings.push(`          contextWindow: ${m.contextWindow}`)
      if (m.maxTokens > 0) settings.push(`          maxTokens: ${m.maxTokens}`)
    }
  }
  return (
    HEADER +
    '\n# ----- ~/.dsh/settings.yaml -----\n' +
    settings.join('\n') +
    '\n\n# ----- ~/.dsh/.credentials.yaml -----\n' +
    `${DSH_API_KEY_ENV}: ${yamlScalar(opts.apiKey)}\n`
  )
}
