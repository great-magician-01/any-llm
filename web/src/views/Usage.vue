<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { fetchSummary, fetchRecords, type UsageSummary, type UsageRecord } from '../api/usage'
import { formatCompact, formatInt, formatPercent, formatTime, localISO } from '../utils/format'
import StatCard from '../components/StatCard.vue'
import AppIcon from '../components/AppIcon.vue'

const groupBy = ref('model')
const summaries = ref<UsageSummary[]>([])
const records = ref<UsageRecord[]>([])
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

onMounted(load)
</script>

<template>
  <div>
    <header class="page-header">
      <div>
        <h1>用量统计</h1>
        <p>按模型 / 上游 / Key 查看请求量与 Token 消耗</p>
      </div>
      <div class="page-header-side">
        <n-button quaternary circle @click="load">
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
            @update:value="load"
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
