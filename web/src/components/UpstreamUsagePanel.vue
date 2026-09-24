<script setup lang="ts">
import { computed } from 'vue'
import type { Upstream, UsageTotals } from '@/api/upstreams'
import { isExpired } from '@/utils/upstreamStatus'
import { formatInt } from '@/utils/format'

const props = defineProps<{
  upstreams: Upstream[]
  /** upstream id -> 今日/本月 token；未加载完成的行显示占位 */
  usage: Record<number, UsageTotals>
}>()

function usageOf(u: Upstream): UsageTotals | undefined {
  return u.id != null ? props.usage[u.id] : undefined
}

// 本月用量多的排前面，概览一眼看到消耗大头
const rows = computed(() =>
  [...props.upstreams].sort((a, b) => (usageOf(b)?.monthly_tokens ?? 0) - (usageOf(a)?.monthly_tokens ?? 0)),
)

interface Quota {
  label: string
  used: number
  limit: number
  text: string
}

function quotas(u: Upstream): Quota[] {
  const t = usageOf(u)
  const mk = (label: string, used: number | undefined, limit: number): Quota => ({
    label,
    used: used ?? 0,
    limit,
    text: t ? `${label} ${formatInt(used ?? 0)}${limit > 0 ? ' / ' + formatInt(limit) : ''}` : `${label} —`,
  })
  return [mk('日', t?.daily_tokens, u.daily_token_limit), mk('月', t?.monthly_tokens, u.monthly_token_limit)]
}

// 进度条状态阈值与 API 密钥页的「今日 / 本月用量」列一致
function pct(q: Quota): number {
  return Math.min(100, Math.round((q.used / q.limit) * 100))
}
function statusOf(q: Quota): 'success' | 'warning' | 'error' {
  if (q.used >= q.limit) return 'error'
  return q.used / q.limit >= 0.8 ? 'warning' : 'success'
}
</script>

<template>
  <n-card title="上游用量" class="panel">
    <div v-if="rows.length === 0" class="empty-hint">暂无上游</div>
    <div v-else class="up-list">
      <div
        v-for="u in rows"
        :key="u.id"
        class="up-row"
        :class="{ dim: !u.enabled || isExpired(u) }"
      >
        <div class="up-head">
          <span class="up-name">{{ u.name }}</span>
          <n-tag v-if="!u.enabled" size="tiny" :bordered="false">已禁用</n-tag>
          <n-tag v-else-if="isExpired(u)" size="tiny" type="error" :bordered="false">已过期</n-tag>
        </div>
        <div class="up-quotas">
          <div v-for="q in quotas(u)" :key="q.label" class="up-quota">
            <span class="q-text mono">{{ q.text }}</span>
            <n-progress
              v-if="q.limit > 0"
              type="line"
              :percentage="pct(q)"
              :status="statusOf(q)"
              :height="5"
              :show-indicator="false"
              border-radius="3px"
            />
          </div>
        </div>
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
.up-row {
  padding: 10px 0;
}
.up-row + .up-row {
  border-top: 1px solid var(--border-soft);
}
.up-row:first-child {
  padding-top: 2px;
}
.up-row:last-child {
  padding-bottom: 2px;
}
.up-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}
.up-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dim .up-name {
  color: var(--text-4);
}
.up-quotas {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 4px 18px;
}
@media (max-width: 640px) {
  .up-quotas {
    grid-template-columns: 1fr;
  }
}
.q-text {
  display: block;
  font-size: 12px;
  color: var(--text-3);
  margin-bottom: 3px;
}
.dim .q-text {
  color: var(--text-4);
}
</style>
