<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { fetchSummary, fetchRecords, fetchDaily, type UsageSummary, type UsageRecord, type UsageDayStat } from '../../api/usage'
import { formatCompact, formatInt, formatPercent, formatTime, localISO } from '../../utils/format'
import StatCard from '../../components/StatCard.vue'
import AppIcon from '../../components/AppIcon.vue'
import BarChart from '../../components/BarChart.vue'
import DonutChart from '../../components/DonutChart.vue'

const groupBy = ref('model')
const summaries = ref<UsageSummary[]>([])
const records = ref<UsageRecord[]>([])
const daily = ref<UsageDayStat[]>([])
const trendDays = ref(14)
const total = ref(0)
const page = ref(1)
const pageSize = 20
// 汇总时间范围（仅影响汇总统计，明细始终显示最新）
const range = ref<[number, number] | null>(null)

const totals = computed(() =>
  summaries.value.reduce(
    (acc, s) => ({
      requests: acc.requests + s.request_count,
      tokens: acc.tokens + s.total_tokens,
      prompt: acc.prompt + s.prompt_tokens,
      completion: acc.completion + s.completion_tokens,
      ok: acc.ok + s.ok_count,
      error: acc.error + s.error_count,
    }),
    { requests: 0, tokens: 0, prompt: 0, completion: 0, ok: 0, error: 0 },
  ),
)

function rangeParams(): { from?: string; to?: string } {
  if (!range.value) return {}
  const from = new Date(range.value[0])
  from.setHours(0, 0, 0, 0)
  const to = new Date(range.value[1])
  to.setHours(23, 59, 59, 0)
  return { from: localISO(from), to: localISO(to) }
}

async function load() {
  const { from, to } = rangeParams()
  summaries.value = await fetchSummary(groupBy.value, from, to)
  const r = await fetchRecords(page.value, pageSize)
  records.value = r.data
  total.value = r.total
}

async function loadDaily(from?: string, to?: string, days?: number) {
  daily.value = await fetchDaily(days ?? trendDays.value, from, to)
}

// 选中日期范围（不超过窗口上限）时，趋势图对齐该范围；清除则回到近 N 天
function selectRangeAsTrend() {
  const { from, to } = rangeParams()
  if (!from || !to) {
    loadDaily()
    return
  }
  const days = Math.round((range.value![1] - range.value![0]) / 86400000) + 1
  if (days <= 90) loadDaily(from, to, days)
}

/* ---- 趋势图 ---- */
const trendLabels = computed(() =>
  daily.value.map((d) => {
    const dt = new Date(d.day)
    return `${dt.getMonth() + 1}/${dt.getDate()}`
  }),
)
const showPrompt = ref(true)
const showCompletion = ref(true)
const tokenSeries = computed(() => {
  const out = []
  if (showPrompt.value) out.push({ name: '输入 Token', color: '#5b8cff', values: daily.value.map((d) => d.prompt_tokens) })
  if (showCompletion.value) out.push({ name: '输出 Token', color: '#22d3ee', values: daily.value.map((d) => d.completion_tokens) })
  return out
})
const reqSeries = computed(() => [
  { name: '成功', color: '#34d399', values: daily.value.map((d) => d.ok_count) },
  { name: '失败', color: '#fb7185', values: daily.value.map((d) => d.error_count) },
])
const rateLine = computed(() => ({
  name: '成功率',
  color: '#fbbf24',
  values: daily.value.map((d) => (d.request_count > 0 ? (d.ok_count / d.request_count) * 100 : null)),
}))

/* ---- 分布图（跟随上方筛选维度与时间范围） ---- */
const PALETTE = ['#5b8cff', '#22d3ee', '#a78bfa', '#fbbf24', '#fb7185', '#34d399', '#f472b6', '#94a3b8']
const distTitle = computed(() => (groupBy.value === 'key' ? 'Key Token 占比' : groupBy.value === 'upstream' ? '上游 Token 占比' : '模型 Token 占比'))
const distSlices = computed(() => {
  const sorted = [...summaries.value].sort((a, b) => b.total_tokens - a.total_tokens)
  const top = sorted.slice(0, 6)
  const rest = sorted.slice(6)
  const slices = top.map((s, i) => ({ name: s.group_key, value: s.total_tokens, color: PALETTE[i % PALETTE.length] }))
  if (rest.length) {
    slices.push({ name: `其他 ${rest.length} 项`, value: rest.reduce((a, s) => a + s.total_tokens, 0), color: '#475569' })
  }
  return slices
})
const compSlices = computed(() => {
  const uncached = Math.max(0, totals.value.prompt - cacheTokens.value.read)
  return [
    { name: '输入（非缓存）', value: uncached, color: '#5b8cff' },
    { name: '输出', value: totals.value.completion, color: '#22d3ee' },
    { name: '缓存读取', value: cacheTokens.value.read, color: '#34d399' },
    { name: '推理', value: reasoningTokens.value, color: '#a78bfa' },
  ].filter((s) => s.value > 0)
})
const cacheTokens = computed(() => {
  // 汇总接口不含缓存/推理字段，按当前范围从汇总无法得出 —— 见下方 loadRangeExtras
  return rangeExtras.value.cache
})
const reasoningTokens = computed(() => rangeExtras.value.reasoning)
const statusSlices = computed(() => [
  { name: '成功', value: totals.value.ok, color: '#34d399' },
  { name: '失败', value: totals.value.error, color: '#fb7185' },
].filter((s) => s.value > 0))

// 汇总接口不含缓存/推理 token，按当前范围从日聚合叠加得到
const rangeExtras = ref({ cache: { read: 0 }, reasoning: 0 })
async function loadRangeExtras() {
  const { from, to } = rangeParams()
  const all = await fetchDaily(90, from, to)
  rangeExtras.value = {
    cache: { read: all.reduce((a, d) => a + d.cache_read_tokens, 0) },
    reasoning: all.reduce((a, d) => a + d.reasoning_tokens, 0),
  }
}

function loadAll() {
  load()
  loadDaily()
  loadRangeExtras()
}

const summaryColumns = computed<DataTableColumns<UsageSummary>>(() => [
  {
    title: groupBy.value === 'key' ? 'Key ID' : groupBy.value === 'upstream' ? '上游' : '模型',
    key: 'group_key',
    render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px; font-weight: 600; color: var(--text)' }, row.group_key),
  },
  { title: '请求数', key: 'request_count', render: (row) => h('span', { class: 'mono' }, formatInt(row.request_count)) },
  { title: '总 Token', key: 'total_tokens', render: (row) => h('span', { class: 'mono', style: 'color: var(--brand-hover); font-weight: 600' }, formatInt(row.total_tokens)) },
  { title: '输入 Token', key: 'prompt_tokens', render: (row) => h('span', { class: 'mono' }, formatInt(row.prompt_tokens)) },
  { title: '输出 Token', key: 'completion_tokens', render: (row) => h('span', { class: 'mono' }, formatInt(row.completion_tokens)) },
  { title: '成功', key: 'ok_count', render: (row) => h('span', { class: 'mono', style: 'color: #34d399' }, formatInt(row.ok_count)) },
  { title: '失败', key: 'error_count', render: (row) => h('span', { class: 'mono', style: row.error_count > 0 ? 'color: #fb7185' : '' }, formatInt(row.error_count)) },
  {
    title: '成功率',
    key: 'rate',
    render: (row) => h('span', { class: 'mono' }, formatPercent(row.ok_count, row.request_count)),
  },
])

const recordColumns: DataTableColumns<UsageRecord> = [
  { title: '时间', key: 'created_at', width: 165, render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, formatTime(row.created_at)) },
  { title: '上游', key: 'upstream_name' },
  { title: '模型', key: 'model', render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, row.model) },
  { title: '入格式', key: 'in_format', width: 96, render: (row) => h(NTag, { size: 'small', bordered: false }, { default: () => row.in_format }) },
  { title: '出格式', key: 'up_format', width: 96, render: (row) => h(NTag, { size: 'small', bordered: false, type: 'info' }, { default: () => row.up_format }) },
  { title: '总 Token', key: 'total_tokens', width: 90, render: (row) => h('span', { class: 'mono', style: 'font-weight: 600' }, formatInt(row.total_tokens)) },
  { title: '输入', key: 'prompt_tokens', width: 80, render: (row) => h('span', { class: 'mono' }, formatInt(row.prompt_tokens)) },
  { title: '输出', key: 'completion_tokens', width: 80, render: (row) => h('span', { class: 'mono' }, formatInt(row.completion_tokens)) },
  { title: '缓存命中', key: 'cache_read_tokens', width: 90, render: (row) => h('span', { class: 'mono', style: row.cache_read_tokens > 0 ? 'color: #34d399' : '' }, formatInt(row.cache_read_tokens)) },
  { title: '缓存写入', key: 'cache_creation_tokens', width: 90, render: (row) => h('span', { class: 'mono' }, formatInt(row.cache_creation_tokens)) },
  { title: '推理', key: 'reasoning_tokens', width: 80, render: (row) => h('span', { class: 'mono', style: row.reasoning_tokens > 0 ? 'color: var(--brand-hover)' : '' }, formatInt(row.reasoning_tokens)) },
  {
    title: '状态',
    key: 'status',
    width: 110,
    render: (row) =>
      h('div', { style: 'display: flex; gap: 4px; align-items: center' }, [
        h(NTag, { size: 'small', bordered: false, type: row.status === 'ok' ? 'success' : 'error' }, { default: () => (row.status === 'ok' ? '成功' : '失败') }),
        row.stream ? h(NTag, { size: 'tiny', bordered: false, quaternary: true, type: 'info' }, { default: () => '流式' }) : null,
      ]),
  },
]

onMounted(loadAll)
</script>

<template>
  <div>
    <header class="page-header">
      <div>
        <h1>用量统计</h1>
        <p>按模型 / 上游 / Key 查看请求量与 Token 消耗</p>
      </div>
      <div class="page-header-side">
        <n-button quaternary circle @click="loadAll">
          <template #icon><AppIcon name="refresh" :size="16" /></template>
        </n-button>
      </div>
    </header>

    <div class="stat-grid">
      <StatCard label="总请求数" :value="formatCompact(totals.requests)" icon="chart" accent="blue" />
      <StatCard label="总 Token" :value="formatCompact(totals.tokens)" :sub="`输入 ${formatCompact(totals.prompt)} · 输出 ${formatCompact(totals.completion)}`" icon="bolt" accent="cyan" />
      <StatCard label="成功率" :value="formatPercent(totals.ok, totals.requests)" :sub="`成功 ${formatInt(totals.ok)} 次`" icon="check" accent="green" />
      <StatCard label="失败请求" :value="formatInt(totals.error)" sub="按当前筛选范围统计" icon="alert" :accent="totals.error > 0 ? 'amber' : 'violet'" />
    </div>

    <div class="chart-grid">
      <n-card class="panel chart-card">
        <template #header>
          <div class="chart-head">
            <span>Token 趋势</span>
            <div class="legend-toggle">
              <button
                v-for="t in [
                  { key: 'prompt', name: '输入', color: '#5b8cff', on: showPrompt },
                  { key: 'completion', name: '输出', color: '#22d3ee', on: showCompletion },
                ]"
                :key="t.key"
                class="legend-chip"
                :class="{ off: !t.on }"
                @click="t.key === 'prompt' ? (showPrompt = !showPrompt) : (showCompletion = !showCompletion)"
              >
                <span class="legend-dot" :style="{ background: t.color }"></span>{{ t.name }}
              </button>
            </div>
          </div>
        </template>
        <template #header-extra>
          <n-radio-group v-model:value="trendDays" size="small" @update:value="loadDaily">
            <n-radio-button :value="7">7 天</n-radio-button>
            <n-radio-button :value="14">14 天</n-radio-button>
            <n-radio-button :value="30">30 天</n-radio-button>
          </n-radio-group>
        </template>
        <BarChart
          v-if="tokenSeries.length"
          :labels="trendLabels"
          :series="tokenSeries"
          mode="stacked"
          :format-value="formatCompact"
        />
        <div v-else class="empty-hint">已隐藏全部系列，点击上方图例恢复</div>
      </n-card>

      <n-card title="请求量与成功率" class="panel chart-card">
        <BarChart
          :labels="trendLabels"
          :series="reqSeries"
          mode="grouped"
          :format-value="formatInt"
          :line="rateLine"
          :format-line="(n: number) => n.toFixed(0) + '%'"
        />
      </n-card>
    </div>

    <div class="chart-grid chart-grid-3">
      <n-card :title="distTitle" class="panel chart-card">
        <div v-if="distSlices.length" class="donut-row">
          <DonutChart :slices="distSlices" :size="168" center-label="总 Token" :format-value="formatCompact" />
          <ul class="donut-legend">
            <li v-for="s in distSlices" :key="s.name">
              <span class="legend-dot" :style="{ background: s.color }"></span>
              <span class="legend-name mono">{{ s.name }}</span>
              <span class="legend-val mono">{{ formatPercent(s.value, totals.tokens) }}</span>
            </li>
          </ul>
        </div>
        <div v-else class="empty-hint">当前范围暂无数据</div>
      </n-card>

      <n-card title="Token 构成" class="panel chart-card">
        <div v-if="compSlices.length" class="donut-row">
          <DonutChart :slices="compSlices" :size="168" center-label="合计" :format-value="formatCompact" />
          <ul class="donut-legend">
            <li v-for="s in compSlices" :key="s.name">
              <span class="legend-dot" :style="{ background: s.color }"></span>
              <span class="legend-name">{{ s.name }}</span>
              <span class="legend-val mono">{{ formatCompact(s.value) }}</span>
            </li>
          </ul>
        </div>
        <div v-else class="empty-hint">当前范围暂无数据</div>
      </n-card>

      <n-card title="请求结果" class="panel chart-card">
        <div v-if="statusSlices.length" class="donut-row">
          <DonutChart
            :slices="statusSlices"
            :size="168"
            :center-value="formatPercent(totals.ok, totals.requests)"
            center-label="成功率"
            :format-value="formatInt"
          />
          <ul class="donut-legend">
            <li v-for="s in statusSlices" :key="s.name">
              <span class="legend-dot" :style="{ background: s.color }"></span>
              <span class="legend-name">{{ s.name }}</span>
              <span class="legend-val mono">{{ formatInt(s.value) }} 次</span>
            </li>
          </ul>
        </div>
        <div v-else class="empty-hint">当前范围暂无数据</div>
      </n-card>
    </div>

    <n-card title="用量汇总" class="panel">
      <template #header-extra>
        <div style="display: flex; align-items: center; gap: 12px">
          <n-date-picker
            v-model:value="range"
            type="daterange"
            size="small"
            clearable
            :is-date-disabled="(ts: number) => ts > Date.now()"
            style="width: 250px"
            @update:value="load(); loadRangeExtras(); selectRangeAsTrend()"
          />
          <n-radio-group v-model:value="groupBy" size="small" @update:value="load">
            <n-radio-button value="model">按模型</n-radio-button>
            <n-radio-button value="upstream">按上游</n-radio-button>
            <n-radio-button value="key">按 Key</n-radio-button>
          </n-radio-group>
        </div>
      </template>
      <n-data-table :bordered="false" :columns="summaryColumns" :data="summaries" />
    </n-card>

    <n-card title="请求明细" class="panel">
      <template #header-extra>
        <span class="toolbar-label">共 {{ formatInt(total) }} 条</span>
      </template>
      <n-data-table
        :bordered="false"
        :columns="recordColumns"
        :data="records"
        :pagination="{ page: page, pageSize, itemCount: total, onChange: (p: number) => { page = p; load() } }"
      />
    </n-card>
  </div>
</template>

<style scoped>
.chart-grid {
  margin-top: 20px;
  display: grid;
  grid-template-columns: minmax(0, 1.5fr) minmax(0, 1fr);
  gap: 20px;
  align-items: stretch;
}
.chart-grid-3 {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
@media (max-width: 1100px) {
  .chart-grid,
  .chart-grid-3 {
    grid-template-columns: 1fr;
  }
}
.chart-head {
  display: flex;
  align-items: center;
  gap: 14px;
}
.legend-toggle {
  display: inline-flex;
  gap: 6px;
}
.legend-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px;
  border-radius: 999px;
  border: 1px solid var(--border-soft);
  background: transparent;
  color: var(--text-3);
  font-size: 11.5px;
  cursor: pointer;
  transition: opacity 0.15s ease;
}
.legend-chip.off {
  opacity: 0.35;
}
.legend-dot {
  width: 7px;
  height: 7px;
  border-radius: 2px;
  flex: none;
}
.donut-row {
  display: flex;
  align-items: center;
  gap: 18px;
  min-height: 180px;
}
.donut-legend {
  list-style: none;
  margin: 0;
  padding: 0;
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 7px;
}
.donut-legend li {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 12px;
}
.legend-name {
  color: var(--text-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}
.legend-val {
  color: var(--text-2);
  font-weight: 600;
  flex: none;
}
.empty-hint {
  padding: 36px 0;
  text-align: center;
  color: var(--text-4);
  font-size: 13px;
}
</style>
