/** 模型别名绑定行的上游下拉选项（经典/毛玻璃两套 Aliases 视图共用） */

import type { SelectOption } from 'naive-ui'
import type { Upstream } from '../api/upstreams'
import { isExpired } from './upstreamStatus'

/**
 * 上游下拉选项：只列可用中的上游。网关解析别名时会跳过指向禁用/已过期上游的
 * 绑定，选中了对该绑定也不生效，所以这两种都不进下拉。
 *
 * 唯一例外是 referencedIDs 里的不可用上游：编辑时已指向它们的旧绑定要能显示
 * 上游名，不能退化成只剩一个 id，于是补成不可选项留在列表里——naive-ui 照旧
 * 渲染 disabled 选项的 label，只是点不动，删不掉也改不走，保存时原样带回。
 */
export function aliasUpstreamOptions(upstreams: Upstream[], referencedIDs: Iterable<number | null>): SelectOption[] {
  const referenced = new Set<number>()
  for (const id of referencedIDs) {
    if (id != null) referenced.add(id)
  }
  return upstreams
    .filter((u) => (u.enabled && !isExpired(u)) || (u.id != null && referenced.has(u.id)))
    .map((u) => {
      const reason = !u.enabled ? '，已禁用' : isExpired(u) ? '，已过期' : ''
      return {
        label: `${u.name}（${u.format}）${reason}`,
        value: u.id as number,
        disabled: reason !== '',
      }
    })
}
