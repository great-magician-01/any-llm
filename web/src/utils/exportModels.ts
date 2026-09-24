/**
 * 客户端配置导出（opencode / OMP / dsh）共用的模型清单收集。
 *
 * 清单口径与网关 /v1/models 完全一致：
 *   - 直连名 upstream/model：只收启用且未过期的上游——禁用/过期上游的模型
 *     导出来了这个 key 也用不了（与 /v1/models 的隐藏口径一致）；
 *   - 别名：至少有一个可用绑定（上游存在、启用、未过期）才收录——网关对
 *     「全绑定不可用」的别名同样不列出。
 * 别名的长度取链上第一个可用绑定的模型配置（与网关的候选顺序一致）；绑定
 * 指向的模型行已不存在时回落网关默认值。
 */
import {
  listUpstreams,
  listModels,
  DEFAULT_MODEL_CONTEXT_LENGTH,
  DEFAULT_MODEL_MAX_OUTPUT_LENGTH,
  type Upstream,
  type UpstreamModel,
} from '../api/upstreams'
import { listAliases, type ModelAlias } from '../api/aliases'
import { isExpired } from './upstreamStatus'

/** 一个待导出的模型：对外 id（与 /v1/models 列出的一致）+ 容量 */
export interface ExportModel {
  id: string
  contextLength: number
  maxOutputLength: number
}

/**
 * 纯展开逻辑（与 IO 分离，便于测试）。aliases 的 bindings 假定已按 priority
 * 排序——后端 ListAliases 按 (priority, id) 返回，Aliases 视图也这么信任。
 */
export function expandExportModels(
  ups: Upstream[],
  modelsByUpstream: ReadonlyMap<number, UpstreamModel[]>,
  aliases: ModelAlias[],
  now: number = Date.now(),
): ExportModel[] {
  // Map 去重：别名与直连名撞同 id 时别名覆盖（网关也是别名优先解析，
  // 见 handler_openai.go 的模型解析顺序）。
  const out = new Map<string, ExportModel>()
  const upsById = new Map<number, Upstream>()
  for (const u of ups) {
    if (u.id == null) continue
    upsById.set(u.id, u)
    if (!u.enabled || isExpired(u, now)) continue
    for (const m of modelsByUpstream.get(u.id) ?? []) {
      const id = `${u.name}/${m.model_name}`
      out.set(id, { id, contextLength: m.context_length, maxOutputLength: m.max_output_length })
    }
  }
  for (const a of aliases) {
    const live = a.bindings.find((b) => {
      const u = upsById.get(b.upstream_id)
      return u != null && u.enabled && !isExpired(u, now)
    })
    if (!live) continue // 全绑定不可用：/v1/models 不列出，导出同样跳过
    const cfg = modelsByUpstream.get(live.upstream_id)?.find((m) => m.model_name === live.model_name)
    out.set(a.name, {
      id: a.name,
      contextLength: cfg && cfg.context_length > 0 ? cfg.context_length : DEFAULT_MODEL_CONTEXT_LENGTH,
      maxOutputLength: cfg && cfg.max_output_length > 0 ? cfg.max_output_length : DEFAULT_MODEL_MAX_OUTPUT_LENGTH,
    })
  }
  return [...out.values()]
}

/**
 * 拉取并展开：上游 + 别名 + 各上游模型表。别名接口失败时降级为只导直连名
 * （如后端版本还没有别名功能），不让整个复制动作失败。
 */
export async function collectExportModels(): Promise<ExportModel[]> {
  const [ups, aliases] = await Promise.all([listUpstreams(), listAliases().catch(() => [] as ModelAlias[])])
  const modelsByUpstream = new Map<number, UpstreamModel[]>()
  await Promise.all(
    ups.map(async (u) => {
      if (u.id == null) return
      modelsByUpstream.set(u.id, await listModels(u.id).catch(() => []))
    }),
  )
  return expandExportModels(ups, modelsByUpstream, aliases)
}

/** 按 key 的白名单过滤：空/null = 不限；别名与直连名同样精确匹配。 */
export function filterAllowedModels(models: ExportModel[], allowedModels?: string[] | null): ExportModel[] {
  if (!allowedModels || allowedModels.length === 0) return models
  return models.filter((m) => allowedModels.includes(m.id))
}
