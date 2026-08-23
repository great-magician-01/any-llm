/**
 * Oh My Pi (omp) 配置生成器。
 *
 * 与 Keys.vue 的 opencode 复制功能配套：生成指向本网关（any-llm）的 OMP 配置，
 * 即「中转后」的配置 —— 模型 id 为 upstream-name/model-name（与 /v1/models 一致），
 * ext key 明文嵌入 apiKey（OMP 的 apiKey 字段支持字面值，也可改成环境变量名）。
 *
 * 按约定不做按模型名的启发式识别：所有模型统一按推理模型处理
 * （reasoning: true + effort 档位），名称原样输出不转换。
 */
export interface OmpModel {
  id: string
  contextWindow: number
  maxTokens: number
}

export interface OmpConfigOptions {
  /** 网关地址，如 http://localhost:6718（不含 /v1，openai-completions 会拼接 /v1/chat/completions） */
  baseUrl: string
  /** ext key 明文（apiKey 字段支持字面值） */
  apiKey: string
  models: OmpModel[]
}

/** 按需双引号转义，其余输出为合法 YAML 纯量 */
function scalar(v: string): string {
  if (v === '') return "''"
  const plain =
    !/^[\s\-?:,\[\]{}#&*!|>'"%@`]/.test(v) && // 不以指示符开头
    !/^\s/.test(v) && // 无前导空白
    !/\s$/.test(v) && // 无尾部空白
    !/[\u0000-\u001f\u007f]/.test(v) && // 无控制字符/换行
    !/[:#] /.test(v) // 无 ': ' / ' #'（行内映射/注释）
  if (plain) return v
  return '"' + v.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n') + '"'
}

const HEADER = `# ============================================================
# Oh My Pi (omp) 配置 — 由 any-llm 生成（指向本网关）
# 1. 粘贴到 ~/.omp/agent/models.yml（已有内容则合并进 providers 段）
# 2. 模型 id 格式：upstream-name/model-name，与网关 /v1/models 一致
# Web 搜索：无需单独配置搜索模型，当前选中的模型即可用于搜索。
#   启用：omp plugin install @ollama/pi-web-search（本地 Ollama），
#   并在 ~/.omp/config.yml 中设置 web_search.enabled: true。
# ============================================================
`

/** 生成可粘贴进 ~/.omp/agent/models.yml 的完整 YAML（provider 固定为 any-llm） */
export function buildOmpYaml(opts: OmpConfigOptions): string {
  const lines = [
    'providers:',
    '  any-llm:',
    `    baseUrl: ${scalar(opts.baseUrl)}`,
    '    api: openai-completions',
    `    apiKey: ${scalar(opts.apiKey)}`,
    '    authHeader: true',
  ]
  if (opts.models.length === 0) {
    lines.push('    models: []')
    lines.push('    # 暂无模型，请在 any-llm 上游中添加模型后重新复制')
  } else {
    lines.push('    models:')
    for (const m of opts.models) {
      lines.push(
        `      - id: ${scalar(m.id)}`,
        `        name: ${scalar(m.id)}`,
        '        reasoning: true',
        '        thinking:',
        '          minLevel: low',
        '          maxLevel: max',
        '          mode: effort',
        '        input: [text]',
        `        contextWindow: ${m.contextWindow}`,
        `        maxTokens: ${m.maxTokens}`,
      )
    }
  }
  return HEADER + lines.join('\n') + '\n'
}
