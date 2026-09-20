/** 模型别名绑定行的上游下拉选项（经典/毛玻璃两套 Aliases 视图共用） */

import type { SelectOption } from 'naive-ui'
import type { Upstream } from '../api/upstreams'

/**
 * 上游下拉选项：只列启用中的上游。网关解析别名时会跳过指向禁用上游的绑定，
 * 选中了对该绑定也不生效，所以禁用的不进下拉。
 *
 * 唯一例外是 referencedIDs 里的禁用上游：编辑时已指向它们的旧绑定要能显示
 * 上游名，不能退化成只剩一个 id，于是补成不可选项留在列表里——naive-ui 照旧
 * 渲染 disabled 选项的 label，只是点不动，删不掉也改不走，保存时原样带回。
 */
export function aliasUpstreamOptions(upstreams: Upstream[], referencedIDs: Iterable<number | null>): SelectOption[] {
  const referenced = new Set<number>()
  for (const id of referencedIDs) {
    if (id != null) referenced.add(id)
  }
  return upstreams
    .filter((u) => u.enabled || (u.id != null && referenced.has(u.id)))
    .map((u) => ({
      label: `${u.name}（${u.format}）${u.enabled ? '' : '，已禁用'}`,
      value: u.id as number,
      disabled: !u.enabled,
    }))
}
