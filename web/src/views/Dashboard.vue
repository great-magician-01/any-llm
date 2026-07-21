<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import { fetchSummary, type UsageSummary } from '../api/usage'
import { listUpstreams } from '../api/upstreams'
import { listKeys } from '../api/keys'
import { formatCompact, formatInt, formatPercent, localISO } from '../utils/format'
import StatCard from '../components/StatCard.vue'
import AppIcon from '../components/AppIcon.vue'

const router = useRouter()
const message = useMessage()

const loading = ref(false)
const today = ref({ requests: 0, tokens: 0, ok: 0, error: 0 })
const month = ref({ requests: 0, tokens: 0 })
const allTime = ref({ requests: 0, tokens: 0 })
const topModels = ref<UsageSummary[]>([])
const upstreamCount = ref(0)
const modelCount = ref(0)
const keyCount = ref(0)
const enabledKeyCount = ref(0)

function sum(list: UsageSummary[]) {
  return list.reduce(
    (acc, s) => ({
      requests: acc.requests + s.request_count,
      tokens: acc.tokens + s.total_tokens,
      ok: acc.ok + s.ok_count,
      error: acc.error + s.error_count,
    }),
    { requests: 0, tokens: 0, ok: 0, error: 0 },
  )
}

function dayStart(): string {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  return localISO(d)
}
function monthStart(): string {
  const d = new Date()
  d.setDate(1)
  d.setHours(0, 0, 0, 0)
  return localISO(d)
}

async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    const [todayList, monthList, allList, ups, ks] = await Promise.all([
      fetchSummary('model', dayStart()),
      fetchSummary('model', monthStart()),
      fetchSummary('model'),
      listUpstreams(),
      listKeys(),
    ])
    today.value = sum(todayList)
    month.value = sum(monthList)
    allTime.value = sum(allList)
    topModels.value = [...monthList].sort((a, b) => b.total_tokens - a.total_tokens).slice(0, 8)
    upstreamCount.value = ups.length
    modelCount.value = ups.reduce((n, u) => n + (u.model_count ?? 0), 0)
    keyCount.value = ks.length
    enabledKeyCount.value = ks.filter((k) => k.enabled).length
  } catch (e: any) {
    if (!silent) message.error('加载概览失败：' + (e?.message || String(e)))
  } finally {
    loading.value = false
  }
}

const todayRate = computed(() => formatPercent(today.value.ok, today.value.requests))
const maxModelTokens = computed(() => topModels.value[0]?.total_tokens || 1)

const origin = computed(() => window.location.origin)
async function copyBaseUrl() {
  const text = origin.value + '/v1'
  try {
    await navigator.clipboard.writeText(text)
    message.success('已复制：' + text)
  } catch {
    message.warning('复制失败，请手动复制：' + text)
  }
}

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  load()
  timer = setInterval(() => load(true), 30000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div>
    <header class="page-header">
      <div>
        <h1>概览</h1>
        <p>网关运行状态一览，每 30 秒自动刷新</p>
      </div>
      <div class="page-header-side">
        <n-button quaternary circle :loading="loading" @click="load()">
          <template #icon><AppIcon name="refresh" :size="16" /></template>
        </n-button>
      </div>
    </header>

    <div class="stat-grid">
      <StatCard
        label="今日 Token"
        :value="formatCompact(today.tokens)"
        :sub="`今日请求 ${formatInt(today.requests)} 次`"
        icon="bolt"
        accent="blue"
      />
      <StatCard
        label="本月 Token"
        :value="formatCompact(month.tokens)"
        :sub="`本月请求 ${formatInt(month.requests)} 次`"
        icon="chart"
        accent="cyan"
      />
      <StatCard
        label="今日成功率"
        :value="todayRate"
        :sub="today.requests ? `成功 ${formatInt(today.ok)} · 失败 ${formatInt(today.error)}` : '今日暂无请求'"
        icon="pulse"
        accent="green"
      />
      <StatCard
        label="累计 Token"
        :value="formatCompact(allTime.tokens)"
        :sub="`累计请求 ${formatInt(allTime.requests)} 次`"
        icon="check"
        accent="violet"
      />
    </div>

    <div class="dash-grid">
      <n-card title="本月模型用量 Top" class="panel">
        <div v-if="topModels.length === 0" class="empty-hint">本月暂无用量数据</div>
        <div v-else class="rank-list">
          <div v-for="m in topModels" :key="m.group_key" class="rank-item">
            <div class="rank-head">
              <span class="rank-name mono">{{ m.group_key }}</span>
              <span class="rank-val mono">{{ formatCompact(m.total_tokens) }}</span>
            </div>
            <div class="rank-bar">
              <div
                class="rank-bar-fill"
                :style="{ width: Math.max(2, (m.total_tokens / maxModelTokens) * 100) + '%' }"
              ></div>
            </div>
            <div class="rank-sub">请求 {{ formatInt(m.request_count) }} · 成功率 {{ formatPercent(m.ok_count, m.request_count) }}</div>
          </div>
        </div>
      </n-card>

      <div class="dash-side">
        <n-card title="资源" class="panel">
          <div class="res-row">
            <span class="res-label"><AppIcon name="layers" :size="14" /> 上游</span>
            <span class="res-val mono">{{ upstreamCount }}</span>
          </div>
          <div class="res-row">
            <span class="res-label"><AppIcon name="server" :size="14" /> 模型</span>
            <span class="res-val mono">{{ modelCount }}</span>
          </div>
          <div class="res-row">
            <span class="res-label"><AppIcon name="key" :size="14" /> API 密钥</span>
            <span class="res-val mono">{{ enabledKeyCount }}<span class="res-dim"> / {{ keyCount }} 启用</span></span>
          </div>
        </n-card>

        <n-card title="快捷操作" class="panel">
          <div class="quick-list">
            <n-button block class="quick-btn" @click="router.push({ name: 'upstreams' })">
              <template #icon><AppIcon name="plus" :size="15" /></template>
              配置上游
            </n-button>
            <n-button block class="quick-btn" @click="router.push({ name: 'keys' })">
              <template #icon><AppIcon name="key" :size="15" /></template>
              签发密钥
            </n-button>
            <n-button block class="quick-btn" @click="copyBaseUrl">
              <template #icon><AppIcon name="copy" :size="15" /></template>
              复制 Base URL
            </n-button>
          </div>
          <p class="quick-hint">
            客户端 base_url 填 <code class="mono">{{ origin }}/v1</code>，模型名格式
            <code class="mono">upstream-name/model-name</code>
          </p>
        </n-card>
      </div>
    </div>
  </div>
</template>

<style scoped>
.dash-grid {
  margin-top: 20px;
  display: grid;
  grid-template-columns: minmax(0, 1.6fr) minmax(0, 1fr);
  gap: 20px;
  align-items: start;
}
@media (max-width: 900px) {
  .dash-grid {
    grid-template-columns: 1fr;
  }
}
.dash-side .panel + .panel {
  margin-top: 20px;
}
.empty-hint {
  padding: 36px 0;
  text-align: center;
  color: var(--text-4);
  font-size: 13px;
}
.rank-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.rank-head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 12px;
}
.rank-name {
  font-size: 13px;
  color: var(--text-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rank-val {
  font-size: 13px;
  font-weight: 600;
  color: var(--brand-hover);
  flex: none;
}
.rank-bar {
  margin-top: 6px;
  height: 6px;
  border-radius: 3px;
  background: rgba(148, 163, 184, 0.1);
  overflow: hidden;
}
.rank-bar-fill {
  height: 100%;
  border-radius: 3px;
  background: var(--grad);
  box-shadow: 0 0 8px rgba(91, 140, 255, 0.5);
  transition: width 0.5s ease;
}
.rank-sub {
  margin-top: 4px;
  font-size: 12px;
  color: var(--text-4);
}
.res-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 0;
}
.res-row + .res-row {
  border-top: 1px solid var(--border-soft);
}
.res-label {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--text-3);
}
.res-val {
  font-size: 16px;
  font-weight: 700;
  color: var(--text);
}
.res-dim {
  font-size: 12px;
  font-weight: 400;
  color: var(--text-4);
}
.quick-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.quick-btn {
  justify-content: flex-start;
}
.quick-hint {
  margin: 14px 0 0;
  font-size: 12px;
  line-height: 1.7;
  color: var(--text-4);
}
.quick-hint code {
  padding: 1px 5px;
  border-radius: 4px;
  background: rgba(148, 163, 184, 0.12);
  color: var(--text-2);
  font-size: 11px;
}
</style>
