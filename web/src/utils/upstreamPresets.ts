import type { SelectGroupOption, SelectOption } from 'naive-ui'

export type UpstreamFormat = 'openai' | 'anthropic' | 'responses'

export interface UpstreamPreset {
  /** select 的 value，稳定标识 */
  key: string
  /** 选项展示名 */
  label: string
  /** 选中后带入的 Base URL（带入后仍可手改） */
  baseUrl: string
  /** 选中后带入的协议格式（带入后仍可手改） */
  format: UpstreamFormat
  /** 下拉分组 */
  group: 'api' | 'coding' | 'official'
  /** 选中后展示的小字提示（Key 获取入口、套餐 Key 限制等） */
  hint?: string
}

// 内置上游预设：选中只带出 base_url 和 format 两项，名称/Key/限额仍由用户
// 填写。Base URL 口径与后端 internal/upstream/client.go 的 endpointURL 保持
// 一致：openai/responses 必须自带版本段（/v1、/v4 等），anthropic 不带 /v1
// （网关按 SDK 约定自补）。厂商改地址时改这张表即可。
export const UPSTREAM_PRESETS: UpstreamPreset[] = [
  // —— 按量计费 API ——
  { key: 'deepseek', label: 'DeepSeek', baseUrl: 'https://api.deepseek.com/v1', format: 'openai', group: 'api', hint: 'Key 获取：platform.deepseek.com' },
  { key: 'deepseek-anthropic', label: 'DeepSeek（Anthropic 格式）', baseUrl: 'https://api.deepseek.com/anthropic', format: 'anthropic', group: 'api' },
  { key: 'moonshot', label: 'Kimi（Moonshot）', baseUrl: 'https://api.moonshot.cn/v1', format: 'openai', group: 'api', hint: 'Key 获取：platform.moonshot.cn' },
  { key: 'moonshot-anthropic', label: 'Kimi（Moonshot，Anthropic 格式）', baseUrl: 'https://api.moonshot.cn/anthropic', format: 'anthropic', group: 'api' },
  { key: 'zhipu', label: '智谱 GLM', baseUrl: 'https://open.bigmodel.cn/api/paas/v4', format: 'openai', group: 'api', hint: 'Key 获取：open.bigmodel.cn' },
  { key: 'qwen', label: '通义千问（阿里云百炼）', baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1', format: 'openai', group: 'api', hint: 'Key 获取：bailian.console.aliyun.com' },
  { key: 'doubao', label: '豆包（火山方舟）', baseUrl: 'https://ark.cn-beijing.volces.com/api/v3', format: 'openai', group: 'api', hint: 'Key 获取：console.volcengine.com/ark' },
  { key: 'minimax', label: 'MiniMax', baseUrl: 'https://api.minimaxi.com/v1', format: 'openai', group: 'api', hint: 'Key 获取：platform.minimaxi.com' },
  { key: 'siliconflow', label: '硅基流动 SiliconFlow', baseUrl: 'https://api.siliconflow.cn/v1', format: 'openai', group: 'api', hint: 'Key 获取：cloud.siliconflow.cn' },
  { key: 'qianfan', label: '百度千帆', baseUrl: 'https://qianfan.baidubce.com/v2', format: 'openai', group: 'api' },
  { key: 'modelscope', label: 'ModelScope 魔搭', baseUrl: 'https://api-inference.modelscope.cn/v1', format: 'openai', group: 'api' },
  { key: 'iflow', label: 'iFlow 心流', baseUrl: 'https://apis.iflow.cn/v1', format: 'openai', group: 'api', hint: 'Key 获取：platform.iflow.cn' },
  // —— Coding Plan 订阅（Key 与按量 API 不通用，需在套餐页单独开通） ——
  { key: 'zhipu-coding-anthropic', label: 'GLM Coding Plan（Anthropic 格式）', baseUrl: 'https://open.bigmodel.cn/api/anthropic', format: 'anthropic', group: 'coding', hint: '需 Coding Plan 专属 Key' },
  { key: 'zhipu-coding-openai', label: 'GLM Coding Plan（OpenAI 格式）', baseUrl: 'https://open.bigmodel.cn/api/coding/paas/v4', format: 'openai', group: 'coding', hint: '需 Coding Plan 专属 Key' },
  { key: 'kimi-coding', label: 'Kimi For Coding', baseUrl: 'https://api.moonshot.cn/anthropic', format: 'anthropic', group: 'coding', hint: '需 Kimi For Coding 订阅 Key' },
  { key: 'minimax-coding', label: 'MiniMax Coding Plan', baseUrl: 'https://api.minimaxi.com/anthropic', format: 'anthropic', group: 'coding', hint: '需 Coding Plan 订阅 Key' },
  { key: 'qwen-coding-openai', label: '通义千问 Coding Plan（OpenAI 格式）', baseUrl: 'https://coding.dashscope.aliyuncs.com/v1', format: 'openai', group: 'coding', hint: '需在百炼控制台 Coding Plan 页单独开通' },
  { key: 'qwen-coding-anthropic', label: '通义千问 Coding Plan（Anthropic 格式）', baseUrl: 'https://coding.dashscope.aliyuncs.com/apps/anthropic', format: 'anthropic', group: 'coding', hint: '需在百炼控制台 Coding Plan 页单独开通' },
  // —— 海外官方 ——
  { key: 'openai', label: 'OpenAI', baseUrl: 'https://api.openai.com/v1', format: 'openai', group: 'official' },
  { key: 'openai-responses', label: 'OpenAI（Responses 格式）', baseUrl: 'https://api.openai.com/v1', format: 'responses', group: 'official' },
  { key: 'anthropic', label: 'Anthropic', baseUrl: 'https://api.anthropic.com', format: 'anthropic', group: 'official' },
]

const GROUP_LABELS: Record<UpstreamPreset['group'], string> = {
  api: '按量计费 API',
  coding: 'Coding Plan 订阅',
  official: '海外官方',
}

export function findPreset(key: string | null | undefined): UpstreamPreset | undefined {
  return UPSTREAM_PRESETS.find(p => p.key === key)
}

/** 生成 n-select 的分组 options（分组顺序 = UPSTREAM_PRESETS 中首次出现的顺序） */
export function presetSelectOptions(): (SelectOption | SelectGroupOption)[] {
  const groups: { group: UpstreamPreset['group']; children: SelectOption[] }[] = []
  for (const p of UPSTREAM_PRESETS) {
    let g = groups.find(x => x.group === p.group)
    if (!g) {
      g = { group: p.group, children: [] }
      groups.push(g)
    }
    g.children.push({ label: p.label, value: p.key })
  }
  return groups.map(g => ({ type: 'group', label: GROUP_LABELS[g.group], key: `g-${g.group}`, children: g.children }))
}
