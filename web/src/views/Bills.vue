<script setup lang="ts">
import { ref, computed, onMounted, h, reactive } from 'vue'
import { NButton, NPopconfirm, NTag, NTooltip, useMessage } from 'naive-ui'
import type { DataTableColumns, SelectOption } from 'naive-ui'
import {
  fetchBills, fetchBillSummary, createBill, updateBill, deleteBill,
  type Bill, type BillSummary, type BillUpstreamTotal, type BillBucket,
} from '@/api/bills'
import { listUpstreams, type Upstream } from '@/api/upstreams'
import { formatInt, formatPercent } from '@/utils/format'
import { formatBillMoney, microsToAmount, bucketLabel, dateParam, amountInputError, billDetailText, perCurrencyLabel } from '@/utils/bill'
import { currentMonthRange, lastNDays, currentYear } from '@/utils/range'
import StatCard from '@/components/StatCard.vue'
import AppIcon from '@/components/AppIcon.vue'
import BarChart from '@/components/BarChart.vue'
import { errText } from '@/utils/errText'

const message = useMessage()

/* ---- 筛选状态：趋势图 / 按上游汇总 / 明细共用同一时间范围与上游过滤 ---- */
// 清空范围 = 后端的缺省回看窗口（summary 三份聚合共用同一个窗口，口径一致）
const range = ref<[number, number] | null>(currentMonthRange())
const rangeShortcuts: Record<string, () => [number, number]> = {
  '本月': currentMonthRange,
  '近 30 天': () => lastNDays(30),
  '今年': currentYear,
}
const upstreamFilter = ref<number | null>(null)
// 趋势桶档位；日期范围与上游过滤对三块数据都生效
const bucket = ref<BillBucket>('day')

const upstreams = ref<Upstream[]>([])
const upstreamOptions = computed<SelectOption[]>(() =>
  upstreams.value.map((u) => ({ label: u.enabled ? u.name : `${u.name}（已禁用）`, value: u.id })),
)

function rangeDates(): { from?: string; to?: string } {
  if (!range.value) return {}
  return { from: dateParam(range.value[0]), to: dateParam(range.value[1]) }
}
function filterParams() {
  const { from, to } = rangeDates()
  return { upstream_id: upstreamFilter.value ?? undefined, from, to }
}

/* ---- 汇总（趋势桶 + 按上游 + 按来源，后端保证同一窗口） ---- */
const summary = ref<BillSummary>({ buckets: [], upstreams: [], kinds: [] })

// 请求序号守卫（useUpstreamList 同款）：快速改筛选时迟到的旧响应不得覆盖新数据
let summarySeq = 0
async function loadSummary() {
  const seq = ++summarySeq
  try {
    const s = await fetchBillSummary({ bucket: bucket.value, ...filterParams() })
    if (seq === summarySeq) summary.value = s
  } catch (e) {
    if (seq === summarySeq) message.error('加载账单汇总失败：' + errText(e))
  }
}

const upstreamTotals = computed(() => summary.value.upstreams)
const totalCount = computed(() => upstreamTotals.value.reduce((a, u) => a + u.count, 0))
// 合计按币种各自归并（后端拆行正是因为跨币种加总没有意义）
const grandTotalLabel = computed(() => perCurrencyLabel(upstreamTotals.value))
// 每币种小计，占比在其币种内部计算
const totalsByCurrency = computed(() => {
  const m = new Map<string, number>()
  for (const u of upstreamTotals.value) m.set(u.currency, (m.get(u.currency) ?? 0) + u.amount_micros)
  return m
})
function kindLabel(kind: Bill['kind']): { count: number; label: string } {
  const rows = summary.value.kinds.filter((k) => k.kind === kind)
  return { count: rows.reduce((a, k) => a + k.count, 0), label: perCurrencyLabel(rows) }
}

/* ---- 趋势图 ---- */
const trendLabels = computed(() => summary.value.buckets.map((b) => bucketLabel(b.bucket)))
const trendSeries = computed(() => [
  { name: '消费金额', color: 'var(--brand)', values: summary.value.buckets.map((b) => b.amount_micros / 1_000_000) },
])
// 图表数值按 1e6 还原成金额串（micros → 元，仅用于展示）
function chartMoney(n: number): string {
  return formatBillMoney(Math.round(n * 1_000_000), upstreamTotals.value[0]?.currency ?? 'CNY')
}

/* ---- 明细 ---- */
const bills = ref<Bill[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20

let billsSeq = 0
async function loadBills() {
  const seq = ++billsSeq
  try {
    const r = await fetchBills({ page: page.value, size: pageSize, ...filterParams() })
    if (seq !== billsSeq) return
    bills.value = r.data
    total.value = r.total
  } catch (e) {
    if (seq === billsSeq) message.error('加载账单明细失败：' + errText(e))
  }
}

// 改筛选（范围/上游/档位）回第 1 页，汇总与明细一起重查
function onFilterChange() {
  page.value = 1
  loadAll()
}

function loadAll() {
  loadSummary()
  loadBills()
}

/* ---- 表格列 ---- */
// billed_at 的 JSON 串自带服务端本地日期前缀（手动记账是当日零点）：直接取
// 字符串前缀而不是 new Date() 换算——后者按浏览器时区解释，跨时区的浏览器会
// 把日期看偏一天（而且与后端按服务端本地日分桶的口径对不上）。
// 手动记账只到日；自动记账来自快照，展示到分钟帮助对账。
function billDateLabel(b: Bill): string {
  const s = b.billed_at ?? ''
  return b.kind === 'auto' && s.length >= 16 ? s.slice(0, 16).replace('T', ' ') : s.slice(0, 10)
}

const billColumns: DataTableColumns<Bill> = [
  { title: '账单日期', key: 'billed_at', width: 150, render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, billDateLabel(row)) },
  { title: '上游', key: 'upstream_name', width: 150, ellipsis: { tooltip: true } },
  {
    title: '类型',
    key: 'kind',
    width: 92,
    render: (row) => {
      const tag = h(NTag, { size: 'small', bordered: false, type: row.kind === 'auto' ? 'info' : 'default' }, { default: () => (row.kind === 'auto' ? '自动' : '手动') })
      const text = billDetailText(row.detail)
      // 自动账单悬浮展示推导依据（前后余额），金额怎么来的可审计
      if (row.kind !== 'auto' || !text) return tag
      return h(NTooltip, null, { trigger: () => tag, default: () => text })
    },
  },
  {
    title: '金额',
    key: 'amount_micros',
    width: 130,
    render: (row) => h('span', { class: 'mono', style: 'font-weight: 600; color: var(--brand-hover)' }, formatBillMoney(row.amount_micros, row.currency)),
  },
  {
    title: '备注',
    key: 'note',
    minWidth: 180,
    ellipsis: { tooltip: true },
    render: (row) => h('span', { style: row.note ? '' : 'color: var(--text-4)' }, row.note || '—'),
  },
  {
    title: '操作',
    key: 'actions',
    width: 130,
    render: (row) =>
      h('div', { style: 'display: flex; gap: 4px' }, [
        row.kind === 'manual'
          ? h(NButton, { size: 'tiny', quaternary: true, type: 'primary', onClick: () => openEdit(row) }, { default: () => '编辑' })
          : null,
        h(NPopconfirm, { onPositiveClick: () => removeBill(row) }, {
          trigger: () => h(NButton, { size: 'tiny', quaternary: true, type: 'error' }, { default: () => '删除' }),
          default: () => '删除这条账单？',
        }),
      ]),
  },
]

const upstreamColumns: DataTableColumns<BillUpstreamTotal> = [
  { title: '上游', key: 'upstream_name', minWidth: 160, ellipsis: { tooltip: true } },
  { title: '币种', key: 'currency', width: 80, render: (row) => h('span', { class: 'mono' }, row.currency) },
  { title: '消费金额', key: 'amount_micros', width: 140, render: (row) => h('span', { class: 'mono', style: 'font-weight: 600; color: var(--brand-hover)' }, formatBillMoney(row.amount_micros, row.currency)) },
  { title: '笔数', key: 'count', width: 80, render: (row) => h('span', { class: 'mono' }, formatInt(row.count)) },
  // 占比在该行币种内部计算：跨币种的分母没有意义
  { title: '占比', key: 'share', width: 90, render: (row) => h('span', { class: 'mono' }, formatPercent(row.amount_micros, totalsByCurrency.value.get(row.currency) ?? 0)) },
]

/* ---- 记账表单 ---- */
const showForm = ref(false)
const editing = ref<Bill | null>(null)
const form = reactive({
  upstream_id: null as number | null,
  date: null as number | null,
  amount: '',
  note: '',
})

function openCreate() {
  editing.value = null
  form.upstream_id = upstreamFilter.value ?? (upstreams.value[0]?.id ?? null)
  form.date = Date.now()
  form.amount = ''
  form.note = ''
  showForm.value = true
}

function openEdit(b: Bill) {
  editing.value = b
  form.upstream_id = b.upstream_id
  // 日期从 billed_at 的服务端本地日期前缀取（不用 new Date() 换算成浏览器
  // 时区——那会让跨时区浏览器的「保存不改日期」把账单挪一天）；无时区部分的
  // "YYYY-MM-DDT00:00:00" 按本地时区解析，dateParam 能原样还原。
  const day = (b.billed_at ?? '').slice(0, 10)
  const parsed = new Date(`${day}T00:00:00`)
  form.date = isNaN(parsed.getTime()) ? null : parsed.getTime()
  form.amount = microsToAmount(b.amount_micros)
  form.note = b.note
  showForm.value = true
}

async function submitForm() {
  const amtErr = amountInputError(form.amount)
  if (amtErr) {
    message.warning(amtErr)
    return
  }
  if (form.upstream_id == null) {
    message.warning('请选择上游')
    return
  }
  if (form.date == null) {
    message.warning('请选择账单日期')
    return
  }
  const input = {
    upstream_id: form.upstream_id,
    date: dateParam(form.date),
    amount: form.amount.trim(),
    note: form.note.trim() || undefined,
  }
  try {
    if (editing.value) {
      await updateBill(editing.value.id, input)
      message.success('账单已更新')
    } else {
      await createBill(input)
      message.success('已记一笔')
    }
    showForm.value = false
    onFilterChange()
  } catch (e) {
    message.error((editing.value ? '更新失败：' : '记账失败：') + errText(e))
  }
}

async function removeBill(b: Bill) {
  try {
    await deleteBill(b.id)
    message.success('已删除')
    loadAll()
  } catch (e) {
    message.error('删除失败：' + errText(e))
  }
}

async function loadUpstreams() {
  // 账单页选上游要含禁用的：禁用上游的历史账单仍在，也要能继续手动记
  try {
    upstreams.value = await listUpstreams('all')
  } catch (e) {
    message.error('加载上游列表失败：' + errText(e))
  }
}

onMounted(() => {
  loadUpstreams()
  loadAll()
})
</script>

<template>
  <div>
    <header class="page-header">
      <div>
        <h1>账单</h1>
        <p>上游消费流水：余额类上游按余额变动自动记账，其余手动记一笔</p>
      </div>
      <div class="page-header-side">
        <n-button quaternary circle @click="loadAll">
          <template #icon><AppIcon name="refresh" :size="16" /></template>
        </n-button>
      </div>
    </header>

    <div class="stat-grid">
      <StatCard label="支出合计" :value="grandTotalLabel || '—'" :sub="`共 ${formatInt(totalCount)} 笔 · 按当前范围统计`" icon="wallet" accent="blue" />
      <StatCard label="自动记账" :value="formatInt(kindLabel('auto').count)" :sub="kindLabel('auto').label || '来自余额快照联动'" icon="pulse" accent="cyan" />
      <StatCard label="手动记账" :value="formatInt(kindLabel('manual').count)" :sub="kindLabel('manual').label || '手动记一笔的账单'" icon="doc" accent="violet" />
      <StatCard label="涉及上游" :value="formatInt(upstreamTotals.length)" sub="按当前范围与过滤条件" icon="layers" accent="green" />
    </div>

    <n-card class="panel">
      <template #header>
        <div class="chart-head">
          <span>消费趋势</span>
          <div class="trend-total mono">{{ grandTotalLabel }}</div>
        </div>
      </template>
      <template #header-extra>
        <n-radio-group v-model:value="bucket" size="small" @update:value="loadSummary">
          <n-radio-button value="day">按日</n-radio-button>
          <n-radio-button value="month">按月</n-radio-button>
          <n-radio-button value="year">按年</n-radio-button>
        </n-radio-group>
      </template>
      <BarChart v-if="trendLabels.length" :labels="trendLabels" :series="trendSeries" mode="stacked" :format-value="chartMoney" />
      <div v-else class="empty-hint">当前范围暂无账单</div>
    </n-card>

    <n-card title="按上游汇总" class="panel">
      <n-data-table v-if="upstreamTotals.length" :bordered="false" :columns="upstreamColumns" :data="upstreamTotals" />
      <div v-else class="empty-hint">当前范围暂无账单</div>
    </n-card>

    <n-card title="账单明细" class="panel">
      <template #header-extra>
        <div class="records-toolbar">
          <n-date-picker
            v-model:value="range"
            type="daterange"
            size="small"
            clearable
            :is-date-disabled="(ts: number) => ts > Date.now()"
            :shortcuts="rangeShortcuts"
            style="width: 250px"
            @update:value="onFilterChange"
          />
          <n-select
            :value="upstreamFilter"
            size="small"
            clearable
            filterable
            placeholder="全部上游"
            :options="upstreamOptions"
            :consistent-menu-width="false"
            style="width: 170px"
            @update:value="(v: number | null) => { upstreamFilter = v; onFilterChange() }"
          />
          <n-button size="small" type="primary" @click="openCreate">
            <template #icon><AppIcon name="plus" :size="14" /></template>
            记一笔
          </n-button>
        </div>
      </template>
      <n-data-table
        :bordered="false"
        :columns="billColumns"
        :data="bills"
        :scroll-x="780"
        remote
        :pagination="{ page: page, pageSize, itemCount: total, onChange: (p: number) => { page = p; loadBills() } }"
      />
    </n-card>

    <n-modal :show="showForm" @update:show="(s: boolean) => { showForm = s }">
      <n-card :title="editing ? '编辑账单' : '记一笔'" :bordered="false" style="width: 460px" role="dialog" aria-modal="true">
        <n-form label-placement="top">
          <n-form-item label="上游">
            <n-select v-model:value="form.upstream_id" :options="upstreamOptions" filterable placeholder="选择上游" />
          </n-form-item>
          <n-form-item label="账单日期">
            <n-date-picker
              v-model:value="form.date"
              type="date"
              style="width: 100%"
              :is-date-disabled="(ts: number) => ts > Date.now()"
              clearable
            />
          </n-form-item>
          <n-form-item label="金额">
            <n-input v-model:value="form.amount" placeholder="如 12.50" @keydown.enter="submitForm" />
          </n-form-item>
          <n-form-item label="备注（可选）">
            <n-input v-model:value="form.note" placeholder="如：订阅费、充值赠送扣除等" />
          </n-form-item>
        </n-form>
        <n-alert type="info" :show-icon="false" style="margin-bottom: 4px">
          金额按上游的计费币种填写（余额类上游与其余额同币种）。
        </n-alert>
        <template #footer>
          <div style="text-align: right">
            <n-button @click="showForm = false" style="margin-right: 8px">取消</n-button>
            <n-button type="primary" @click="submitForm">保存</n-button>
          </div>
        </template>
      </n-card>
    </n-modal>
  </div>
</template>

<style scoped>
.chart-head {
  display: flex;
  align-items: center;
  gap: 14px;
}
.trend-total {
  font-size: 13px;
  font-weight: 600;
  color: var(--brand-hover);
}
.records-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
}
.empty-hint {
  padding: 36px 0;
  text-align: center;
  color: var(--text-4);
  font-size: 13px;
}
</style>
