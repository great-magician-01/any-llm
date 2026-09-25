<script setup lang="ts">
/** 概览页的余额/额度看板（经典/毛玻璃两套 Dashboard 共用）：每个上游一行，
 * quota 厂商（kimi-coding）展示 5h/周/月窗口的已用百分比进度条，balance 厂商
 * （DeepSeek/阶跃）展示余额。数据来自后端定期抓取的快照（listLatestBalances）。 */
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import type { Upstream } from '@/api/upstreams'
import type { BalanceSnapshot } from '@/api/balances'
import { balanceView, balanceTooltip, formatFetchedAt, type BalanceView, type QuotaWindowView } from '@/utils/balance'
import { isExpired } from '@/utils/upstreamStatus'

const props = defineProps<{
  upstreams: Upstream[]
  /** upstream id -> 最新余额/额度快照；无快照的上游不进看板 */
  snapshots: Record<number, BalanceSnapshot>
  /** 点击行跳转的上游管理页路由名（经典/毛玻璃两套不同） */
  to: string
}>()

const router = useRouter()

interface BoardRow {
  id: number
  name: string
  disabled: boolean
  expired: boolean
  view: BalanceView
  fetchedAt: string
  tooltip: string
}

const rows = computed<BoardRow[]>(() =>
  props.upstreams.flatMap((u) => {
    const s = u.id != null ? props.snapshots[u.id] : undefined
    const view = s ? balanceView(s) : null
    if (!s || !view) return []
    return [
      {
        id: u.id as number,
        name: u.name,
        disabled: !u.enabled,
        expired: u.enabled && isExpired(u),
        view,
        fetchedAt: formatFetchedAt(s.created_at),
        tooltip: balanceTooltip(s, '点击查看上游'),
      },
    ]
  }),
)

// 进度条状态阈值与上游用量面板一致：跑满为错误，>=80% 警告
function pctOf(w: QuotaWindowView): number {
  const p = parseFloat(w.percent ?? '')
  return Number.isNaN(p) ? 0 : Math.min(100, Math.round(p))
}
function statusOf(w: QuotaWindowView): 'success' | 'warning' | 'error' {
  const p = parseFloat(w.percent ?? '')
  if (Number.isNaN(p)) return 'success'
  if (p >= 100) return 'error'
  return p >= 80 ? 'warning' : 'success'
}
</script>

<template>
  <n-card title="余额 / 额度" class="panel">
    <div v-if="rows.length === 0" class="empty-hint">暂无余额/额度数据，支持的上游会定期自动抓取</div>
    <div v-else class="bal-list">
      <div
        v-for="r in rows"
        :key="r.id"
        class="bal-row"
        :class="{ dim: r.disabled || r.expired }"
        :title="r.tooltip"
        @click="router.push({ name: to })"
      >
        <div class="bal-head">
          <span class="bal-name">{{ r.name }}</span>
          <n-tag v-if="r.disabled" size="tiny" :bordered="false">已禁用</n-tag>
          <n-tag v-else-if="r.expired" size="tiny" type="error" :bordered="false">已过期</n-tag>
          <span class="bal-time">更新于 {{ r.fetchedAt }}</span>
        </div>
        <div v-if="r.view.kind === 'quota'" class="bal-windows">
          <div v-for="w in r.view.windows" :key="w.id" class="bal-window">
            <span v-if="w.missing" class="w-text mono w-missing">{{ w.label }} —</span>
            <template v-else>
              <span class="w-text mono">
                {{ w.label }} 已用 {{ w.percent }}%<span v-if="w.reset" class="w-reset">（{{ w.reset }} 重置）</span>
              </span>
              <n-progress
                type="line"
                :percentage="pctOf(w)"
                :status="statusOf(w)"
                :height="5"
                :show-indicator="false"
                border-radius="3px"
              />
            </template>
          </div>
        </div>
        <div v-else class="bal-amount mono">{{ r.view.text }}</div>
      </div>
    </div>
  </n-card>
</template>

<style scoped>
.empty-hint {
  padding: 36px 0;
  text-align: center;
  color: var(--text-4);
  font-size: 13px;
}
.bal-row {
  padding: 10px 0;
  cursor: pointer;
}
.bal-row + .bal-row {
  border-top: 1px solid var(--border-soft);
}
.bal-row:first-child {
  padding-top: 2px;
}
.bal-row:last-child {
  padding-bottom: 2px;
}
.bal-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}
.bal-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dim .bal-name {
  color: var(--text-4);
}
.bal-time {
  margin-left: auto;
  flex: none;
  font-size: 11px;
  color: var(--text-4);
}
.bal-windows {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 4px 18px;
}
@media (max-width: 640px) {
  .bal-windows {
    grid-template-columns: 1fr;
  }
}
.w-text {
  display: block;
  font-size: 12px;
  color: var(--text-3);
  margin-bottom: 3px;
}
.dim .w-text {
  color: var(--text-4);
}
.w-reset,
.w-missing {
  color: var(--text-4);
}
.bal-amount {
  font-size: 15px;
  font-weight: 600;
  color: var(--text);
}
.dim .bal-amount {
  color: var(--text-4);
}
</style>
