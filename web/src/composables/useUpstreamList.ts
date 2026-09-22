/** 上游管理页的列表状态（经典/毛玻璃两套 Upstreams 视图共用） */

import { ref } from 'vue'
import { useMessage } from 'naive-ui'
import { listUpstreams, updateUpstream, type Upstream, type UpstreamStatusFilter } from '../api/upstreams'
import { listLatestBalances, type BalanceSnapshot } from '../api/balances'

function errText(e: any): string {
  const r = e?.response
  if (r?.data) {
    if (typeof r.data === 'string') return r.data
    if (r.data.error) return String(r.data.error)
    return JSON.stringify(r.data)
  }
  return e?.message || String(e)
}

/**
 * 上游列表 + 启用状态过滤 + 最新余额快照。
 *
 * 两个要点：
 *   - 切过滤档位只重查上游列表（loadList）：余额快照与启用过滤无关，而
 *     listLatestSnapshots 要对 balance_snapshots 做全表 GROUP BY，不该跟着
 *     每次点击档位走。
 *   - loadList 带请求序号守卫：快速切档时迟到的旧档位响应不能覆盖新档位的
 *     列表（表格显示与单选按钮各说各话）；失败只在仍是最新请求时报错。
 */
export function useUpstreamList() {
  const message = useMessage()
  const upstreams = ref<Upstream[]>([])
  // 列表启用状态过滤：本页默认只看启用中；其余页面（Dashboard/Keys/Aliases）
  // 不传参仍拿全量。切换档位即重新查询。
  const statusFilter = ref<UpstreamStatusFilter>('enabled')
  const balancesByUpstream = ref<Record<number, BalanceSnapshot>>({})

  let listSeq = 0
  async function loadList() {
    const seq = ++listSeq
    try {
      const ups = await listUpstreams(statusFilter.value)
      if (seq !== listSeq) return // 已有更新的查询发出，丢弃迟到响应
      upstreams.value = ups
    } catch (e) {
      if (seq === listSeq) message.error('加载上游列表失败：' + errText(e))
    }
  }

  async function loadBalances() {
    // 余额快照接口不可用时（如后端未升级）不阻塞上游列表
    const snaps = await listLatestBalances().catch(() => [] as BalanceSnapshot[])
    const map: Record<number, BalanceSnapshot> = {}
    for (const s of snaps) map[s.upstream_id] = s
    balancesByUpstream.value = map
  }

  // 列表 + 余额一起刷：打开页面、手动刷新按钮、增删改上游走这里。
  async function load() {
    await Promise.all([loadList(), loadBalances()])
  }

  // 显式赋值再查询：不依赖 v-model 与 @update:value 的处理顺序
  function setStatusFilter(v: UpstreamStatusFilter) {
    statusFilter.value = v
    loadList()
  }

  // 切换启用状态后整列重查：「该行是否还属于当前档位」的唯一事实源是后端
  // 过滤口径，客户端不再另写一份（写一个就得跟着档位词汇表改一个）。
  async function toggleEnabled(row: Upstream, v: boolean) {
    try {
      await updateUpstream(row.id as number, { enabled: v })
      row.enabled = v // 响应式行对象，直接改即可；失败时不改，开关弹回原状态
      await loadList()
    } catch (e) {
      message.error((v ? '启用失败：' : '禁用失败：') + errText(e))
    }
  }

  return { upstreams, statusFilter, balancesByUpstream, load, loadList, loadBalances, setStatusFilter, toggleEnabled }
}
