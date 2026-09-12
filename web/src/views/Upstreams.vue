<script setup lang="ts">
import { ref, onMounted, h } from 'vue'
import { NButton, NSpace, NTag, NPopconfirm, NInput, NInputNumber, NSwitch, NText, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { listUpstreams, createUpstream, updateUpstream, deleteUpstream, fetchModels as fetchUpsModels, listModels, addModel, updateModel, deleteModel, DEFAULT_MODEL_LENGTH, type Upstream, type UpstreamModel } from '../api/upstreams'
import { listLatestBalances, listBalanceHistory, refreshBalance, refreshAllBalances, type BalanceSnapshot, type BalancePayload } from '../api/balances'
import { exportConfig, importConfig, type ConfigFile } from '../api/config'
import { configFileName, parseConfigFile, describeConfigFile, describeImportResult, downloadJSON } from '../utils/configTransfer'
import { formatInt, formatTime, formatMoney } from '../utils/format'
import AppIcon from '../components/AppIcon.vue'

const message = useMessage()
const upstreams = ref<Upstream[]>([])
const showForm = ref(false)
const form = ref<Upstream & { fetch_models?: boolean }>({ name: '', base_url: '', api_key: '', format: 'openai', enabled: true, daily_token_limit: 0, monthly_token_limit: 0, fetch_models: true })
const editing = ref<Upstream | null>(null)
const expandedRowKeys = ref<number[]>([])
const modelsByUpstream = ref<Record<number, UpstreamModel[]>>({})
const newModelByUpstream = ref<Record<number, string>>({})
const newModelOpts = ref<Record<number, { context_length: number; max_output_length: number }>>({})
const showModelForm = ref(false)
const modelFormUpstreamId = ref(0)
const modelForm = ref<UpstreamModel | null>(null)
const fetchingId = ref<number | null>(null)
const balancesByUpstream = ref<Record<number, BalanceSnapshot>>({})
const refreshingId = ref<number | null>(null)
const showHistory = ref(false)
const historyUpstream = ref<Upstream | null>(null)
const historyRows = ref<BalanceSnapshot[]>([])
const historyTotal = ref(0)
const historyPage = ref(1)
const historyPageSize = 10
const historyLoading = ref(false)
// 配置导出/导入：导入先选文件、确认摘要后再执行（同名覆盖、不同名保留）
const importInput = ref<HTMLInputElement | null>(null)
const pendingImport = ref<ConfigFile | null>(null)
const importing = ref(false)

function modelOptsFor(id: number) {
  if (!newModelOpts.value[id]) {
    newModelOpts.value[id] = { context_length: DEFAULT_MODEL_LENGTH, max_output_length: DEFAULT_MODEL_LENGTH }
  }
  return newModelOpts.value[id]
}

function fmtK(n: number): string {
  return n >= 1000 ? `${Math.round(n / 1000)}k` : String(n)
}

const CTX_PRESETS = [
  { label: '200k', value: 200000 },
  { label: '400k', value: 400000 },
  { label: '1M', value: 1000000 },
]

function errMsg(e: any): string {
  const r = e?.response
  if (r?.data) {
    if (typeof r.data === 'string') return r.data
    if (r.data.error) return String(r.data.error)
    return JSON.stringify(r.data)
  }
  return e?.message || String(e)
}

// Heuristic: detect cases where the upstream likely does not expose a
// models-listing endpoint (e.g. anthropic-compat providers that only
// implement /messages). Returns true when we should show a friendly
// "add manually" hint rather than a hard error.
function isModelsEndpointUnsupported(e: any): boolean {
  const r = e?.response
  if (!r) return false
  const status = r.status
  const text = (typeof r.data === 'string' ? r.data : r.data?.error) || ''
  // 404 from the gateway's fetch handler means upstream returned 404/empty
  if (status === 502) {
    if (/upstream\s+404/i.test(text)) return true
    // empty/blank upstream body also suggests endpoint doesn't exist
    if (/upstream\s+\d+\s*:\s*(\s|$)/i.test(text)) return true
  }
  if (status === 404) return true
  return false
}

async function load() {
  // 余额快照接口不可用时（如后端未升级）不阻塞上游列表
  const [ups, snaps] = await Promise.all([listUpstreams(), listLatestBalances().catch(() => [] as BalanceSnapshot[])])
  upstreams.value = ups
  const map: Record<number, BalanceSnapshot> = {}
  for (const s of snaps) map[s.upstream_id] = s
  balancesByUpstream.value = map
}
async function doExport() {
  try {
    const data = await exportConfig()
    downloadJSON(data, configFileName(new Date()))
    message.success(`已导出 ${data.upstreams.length} 个上游、${data.aliases.length} 个别名的配置（文件含 API Key，请妥善保管）`)
  } catch (e) {
    message.error('导出失败：' + errMsg(e))
  }
}
function chooseImportFile() { importInput.value?.click() }
function onImportFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // 清空选择，同一文件可重复导入
  if (!file) return
  file.text().then((text) => {
    try {
      pendingImport.value = parseConfigFile(text)
    } catch (err) {
      message.error('导入失败：' + (err instanceof Error ? err.message : String(err)))
    }
  }).catch((err) => {
    message.error('读取文件失败：' + (err instanceof Error ? err.message : String(err)))
  })
}
async function doImport() {
  const payload = pendingImport.value
  if (!payload || importing.value) return
  importing.value = true
  try {
    const res = await importConfig(payload)
    pendingImport.value = null
    message.success(describeImportResult(res))
    await load()
  } catch (e) {
    message.error('导入失败：' + errMsg(e))
  } finally {
    importing.value = false
  }
}
async function save() {
  try {
    if (editing.value?.id) {
      await updateUpstream(editing.value.id, form.value)
    } else {
      await createUpstream(form.value)
    }
    showForm.value = false
    editing.value = null
    resetForm()
    await load()
  } catch (e) {
    message.error('保存失败：' + errMsg(e))
  }
}
function resetForm() { form.value = { name: '', base_url: '', api_key: '', format: 'openai', enabled: true, daily_token_limit: 0, monthly_token_limit: 0, fetch_models: true } }
// When editing, keep the masked key returned by the list endpoint as the
// field value. The backend detects the masked placeholder and skips
// overwriting the stored secret; if the user types a new key, it gets saved.
function edit(u: Upstream) { editing.value = u; form.value = { ...u }; showForm.value = true }
function add() { editing.value = null; resetForm(); showForm.value = true }
async function del(id: number) { await deleteUpstream(id); await load() }
async function toggleEnabled(row: Upstream, v: boolean) {
  try {
    await updateUpstream(row.id as number, { enabled: v })
    row.enabled = v // 响应式行对象，直接改即可；失败时不改，开关弹回原状态
  } catch (e) {
    message.error((v ? '启用失败：' : '禁用失败：') + errMsg(e))
  }
}
async function fetchM(id: number) {
  if (fetchingId.value !== null) return
  fetchingId.value = id
  try {
    await fetchUpsModels(id)
    await load()
    if (expandedRowKeys.value.includes(id)) await loadModels(id)
    message.success('已拉取并更新模型列表')
  } catch (e) {
    if (isModelsEndpointUnsupported(e)) {
      message.warning(
        '该上游可能不支持自动拉取模型列表（如 DeepSeek 的 Anthropic 兼容端点仅提供 /messages）。请在下方手动添加模型名。',
        { duration: 10000 },
      )
      if (!expandedRowKeys.value.includes(id)) {
        expandedRowKeys.value = [...expandedRowKeys.value, id]
        await loadModels(id)
      }
    } else {
      message.error('拉取模型失败：' + errMsg(e), { duration: 8000 })
    }
  } finally {
    fetchingId.value = null
  }
}

async function loadModels(id: number) { modelsByUpstream.value[id] = await listModels(id) }
async function onExpand(keys: number[]) {
  expandedRowKeys.value = keys
  for (const id of keys) {
    if (!modelsByUpstream.value[id]) await loadModels(id)
  }
}
async function addM(id: number) {
  const name = (newModelByUpstream.value[id] || '').trim()
  if (!name) return
  const opts = modelOptsFor(id)
  await addModel(id, name, opts.context_length, opts.max_output_length)
  newModelByUpstream.value[id] = ''
  await loadModels(id)
  await load()
}
function openModelEdit(upstreamId: number, m: UpstreamModel) {
  modelFormUpstreamId.value = upstreamId
  modelForm.value = { ...m }
  showModelForm.value = true
}
async function saveModel() {
  const m = modelForm.value
  if (!m) return
  try {
    await updateModel(modelFormUpstreamId.value, m.id, m.context_length, m.max_output_length)
    showModelForm.value = false
    modelForm.value = null
    await loadModels(modelFormUpstreamId.value)
    message.success('已保存')
  } catch (e) {
    message.error('保存失败：' + errMsg(e))
  }
}
async function delM(id: number, mid: number) {
  await deleteModel(id, mid)
  await loadModels(id)
  await load()
}

function parsePayload(s: BalanceSnapshot): BalancePayload | null {
  if (!s?.payload) return null
  if (typeof s.payload === 'string') {
    try { return JSON.parse(s.payload) as BalancePayload } catch { return null }
  }
  return s.payload
}

const QUOTA_WINDOW_LABELS: Record<string, string> = { five_hour: '5h', weekly: '周', monthly: '月' }

/** 快照摘要：balance -> "¥110.00"；quota -> "5h 12% · 周 34% · 月 56%"；无数据返回 null */
function balanceSummary(s: BalanceSnapshot | undefined): string | null {
  if (!s) return null
  const p = parsePayload(s)
  if (!p) return null
  if (p.kind === 'balance') {
    const b = p.balances?.[0]
    if (!b) return null
    return formatMoney(b.total, b.currency)
  }
  const parts = (p.windows || []).map(w => `${QUOTA_WINDOW_LABELS[w.id] ?? w.id} ${w.used_percent}%`)
  return parts.length ? parts.join(' · ') : null
}

async function refreshB(id: number) {
  if (refreshingId.value !== null) return
  refreshingId.value = id
  try {
    const s = await refreshBalance(id)
    balancesByUpstream.value = { ...balancesByUpstream.value, [id]: s }
    message.success('已刷新余额/额度')
  } catch (e) {
    message.error('刷新余额/额度失败：' + errMsg(e))
  } finally {
    refreshingId.value = null
  }
}

async function openHistory(row: Upstream) {
  historyUpstream.value = row
  historyPage.value = 1
  showHistory.value = true
  await loadHistory()
}
async function loadHistory() {
  const id = historyUpstream.value?.id
  if (!id) return
  historyLoading.value = true
  try {
    const r = await listBalanceHistory(id, historyPage.value, historyPageSize)
    historyRows.value = r.data
    historyTotal.value = r.total
  } catch (e) {
    message.error('加载历史失败：' + errMsg(e))
  } finally {
    historyLoading.value = false
  }
}

const historyColumns: DataTableColumns<BalanceSnapshot> = [
  { title: '时间', key: 'created_at', width: 170, render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, formatTime(row.created_at)) },
  { title: '厂商', key: 'vendor', width: 110, render: (row) => h(NTag, { size: 'small', bordered: false }, { default: () => row.vendor }) },
  { title: '内容', key: 'payload', render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, balanceSummary(row) ?? '-') },
]

const columns: DataTableColumns<Upstream> = [
  { type: 'expand', expandable: () => true, renderExpand: (row) => {
    const id = row.id as number
    const models = modelsByUpstream.value[id] || []
    const opts = modelOptsFor(id)
    return h('div', { style: 'padding: 8px 0 16px 24px' }, [
      h('div', { class: 'toolbar', style: 'margin-bottom: 8px; display: flex; align-items: center; gap: 8px; flex-wrap: wrap' }, [
        h(NInput, {
          value: newModelByUpstream.value[id] || '',
          'onUpdate:value': (v: string) => { newModelByUpstream.value[id] = v },
          placeholder: '模型名，如 gpt-4o',
          style: 'width: 240px',
          onKeyup: (e: KeyboardEvent) => { if (e.key === 'Enter') addM(id) },
        }),
        h('div', { style: 'display: flex; align-items: center; gap: 4px' }, [
          h(NInputNumber, {
            value: opts.context_length,
            'onUpdate:value': (v: number | null) => { opts.context_length = v ?? DEFAULT_MODEL_LENGTH },
            min: 0,
            step: 1000,
            placeholder: '上下文长度',
            style: 'width: 150px',
          }),
          ...CTX_PRESETS.map(p => h(NButton, {
            size: 'tiny',
            quaternary: true,
            type: opts.context_length === p.value ? 'primary' : 'default',
            onClick: () => { opts.context_length = p.value },
          }, { default: () => p.label })),
        ]),
        h(NInputNumber, {
          value: opts.max_output_length,
          'onUpdate:value': (v: number | null) => { opts.max_output_length = v ?? DEFAULT_MODEL_LENGTH },
          min: 0,
          step: 1000,
          placeholder: '最大输出长度',
          style: 'width: 150px',
        }),
        h(NButton, { type: 'primary', size: 'small', onClick: () => addM(id) }, { default: () => '添加' }),
        h(NButton, {
          size: 'small',
          loading: fetchingId.value === id,
          disabled: fetchingId.value !== null,
          onClick: () => fetchM(id),
        }, { default: () => '拉取模型' }),
      ]),
      models.length === 0
        ? h(NText, { depth: 3, style: 'font-size: 13px' }, { default: () => '暂无模型，可手动添加或点击「拉取模型」从上游获取' })
        : h('div', { style: 'display: flex; flex-wrap: wrap; gap: 8px' },
            models.map(m => h(NTag, {
              type: m.manual ? 'default' : 'info',
              bordered: false,
              closable: true,
              style: 'cursor: pointer',
              onClick: (e: MouseEvent) => {
                if ((e.target as HTMLElement).closest('.n-tag__close')) return
                openModelEdit(id, m)
              },
              onClose: () => delM(id, m.id),
            }, { default: () => [
              m.model_name + (m.manual ? '（手动）' : ''),
              h('span', { style: 'opacity: 0.6; font-size: 11px; margin-left: 6px' }, `${fmtK(m.context_length)} / ${fmtK(m.max_output_length)}`),
            ] }))
          ),
    ])
  }},
  // 名称/地址之外的列宽固定；名称给定宽、地址给 minWidth 吸收剩余空间。
  // 配合 scroll-x，窗口过窄时表格横向滚动而不是把无宽度的列压成 0。
  { title: '名称', key: 'name', width: 130, ellipsis: { tooltip: true }, render: (row) => h('span', { style: 'font-weight: 600; color: var(--text)' }, row.name) },
  { title: '状态', key: 'enabled', width: 80, render: (row) => h(NSwitch, {
      value: row.enabled, size: 'small', 'onUpdate:value': (v: boolean) => toggleEnabled(row, v) }) },
  { title: '地址', key: 'base_url', minWidth: 180, ellipsis: { tooltip: true }, render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, row.base_url) },
  {
    title: '格式',
    key: 'format',
    width: 110,
    render: (row) => h(NTag, { type: row.format === 'openai' ? 'info' : 'warning', bordered: false, size: 'small' }, { default: () => row.format }),
  },
  {
    title: '模型数',
    key: 'model_count',
    width: 80,
    render: (row) => h('span', { class: 'mono' }, formatInt(row.model_count ?? 0)),
  },
  {
    title: '余额/额度',
    key: 'balance',
    width: 180,
    render: (row) => {
      const text = balanceSummary(balancesByUpstream.value[row.id as number])
      if (!text) return h('span', { style: 'color: var(--text-4)' }, '-')
      return h('span', { class: 'mono', style: 'cursor: pointer', title: '点击查看历史', onClick: () => openHistory(row) }, text)
    },
  },
  {
    title: '日 token 上限',
    key: 'daily_token_limit',
    width: 120,
    render: (row) => row.daily_token_limit > 0
      ? h('span', { class: 'mono' }, formatInt(row.daily_token_limit))
      : h('span', { style: 'color: var(--text-4)' }, '不限'),
  },
  {
    title: '月 token 上限',
    key: 'monthly_token_limit',
    width: 120,
    render: (row) => row.monthly_token_limit > 0
      ? h('span', { class: 'mono' }, formatInt(row.monthly_token_limit))
      : h('span', { style: 'color: var(--text-4)' }, '不限'),
  },
  { title: '操作', key: 'actions', width: 360, render: (row) => h(NSpace, { size: 8, wrap: false }, {
    default: () => [
      h(NButton, { size: 'small', onClick: () => edit(row) }, { default: () => '编辑' }),
      h(NButton, {
        size: 'small',
        loading: fetchingId.value === row.id,
        disabled: fetchingId.value !== null,
        onClick: () => fetchM(row.id as number),
      }, { default: () => '拉取模型' }),
      h(NButton, {
        size: 'small',
        quaternary: true,
        loading: refreshingId.value === row.id,
        disabled: refreshingId.value !== null,
        onClick: () => refreshB(row.id as number),
      }, { default: () => '刷新余额' }),
      h(NButton, { size: 'small', quaternary: true, onClick: () => openHistory(row) }, { default: () => '历史' }),
      h(NPopconfirm, { onPositiveClick: () => del(row.id as number) }, {
        trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, { default: () => '删除' }),
        default: () => '确定删除？',
      }),
    ],
  })},
]

// 打开页面时后台静默刷新所有受支持 upstream 的余额/额度，完成后更新对应行；
// 失败不打扰用户（厂商接口超时/不支持时保持显示已有快照）
async function autoRefreshBalances() {
  try {
    const snaps = await refreshAllBalances()
    if (!snaps.length) return
    const map = { ...balancesByUpstream.value }
    for (const s of snaps) map[s.upstream_id] = s
    balancesByUpstream.value = map
  } catch { /* 静默降级 */ }
}

onMounted(() => {
  load()
  autoRefreshBalances()
})
</script>

<template>
  <div>
    <header class="page-header">
      <div>
        <h1>上游管理</h1>
        <p>配置上游 LLM 服务，支持 OpenAI / Anthropic 格式。点击行前箭头展开查看模型</p>
      </div>
      <div class="page-header-side">
        <n-button quaternary circle @click="load">
          <template #icon><AppIcon name="refresh" :size="16" /></template>
        </n-button>
      </div>
    </header>

    <n-card title="上游列表" class="panel">
      <template #header-extra>
        <n-space :size="8" :wrap="false">
          <n-button size="small" quaternary @click="chooseImportFile">
            <template #icon><AppIcon name="upload" :size="14" /></template>
            导入配置
          </n-button>
          <n-button size="small" quaternary @click="doExport">
            <template #icon><AppIcon name="download" :size="14" /></template>
            导出配置
          </n-button>
          <n-button type="primary" size="small" @click="add">
            <template #icon><AppIcon name="plus" :size="14" /></template>
            添加上游
          </n-button>
        </n-space>
      </template>
      <n-data-table
        :bordered="false"
        :columns="columns"
        :data="upstreams"
        :scroll-x="1400"
        :row-key="(row: Upstream) => row.id"
        :expanded-row-keys="expandedRowKeys"
        @update:expanded-row-keys="onExpand"
      />
    </n-card>

    <n-modal :show="showForm" @update:show="(show: boolean) => { if (!show) showForm = false }">
      <n-card :title="editing ? '编辑上游' : '添加上游'" :bordered="false" style="width:500px">
        <n-form label-placement="top">
          <n-form-item label="名称"><n-input v-model:value="form.name" /></n-form-item>
          <n-form-item label="Base URL"><n-input v-model:value="form.base_url" /></n-form-item>
          <n-form-item label="API Key">
            <n-input
              v-model:value="form.api_key"
              type="password"
              :placeholder="editing ? '未修改将保持原 key' : '请输入 API Key'"
            />
          </n-form-item>
          <n-form-item label="格式">
            <n-radio-group v-model:value="form.format">
              <n-radio value="openai">OpenAI</n-radio>
              <n-radio value="anthropic">Anthropic</n-radio>
              <n-radio value="responses">Responses</n-radio>
            </n-radio-group>
          </n-form-item>
          <n-form-item label="启用"><n-switch v-model:value="form.enabled" /></n-form-item>
          <n-form-item label="单日 token 上限">
            <n-input-number
              v-model:value="form.daily_token_limit"
              :min="0"
              :step="1000"
              placeholder="0 表示不限"
              style="width: 100%"
            />
          </n-form-item>
          <n-form-item label="单月 token 上限">
            <n-input-number
              v-model:value="form.monthly_token_limit"
              :min="0"
              :step="10000"
              placeholder="0 表示不限"
              style="width: 100%"
            />
          </n-form-item>
          <n-button type="primary" block @click="save">{{ editing ? '保存' : '添加' }}</n-button>
        </n-form>
      </n-card>
    </n-modal>
    <n-modal :show="showModelForm" @update:show="(show: boolean) => { if (!show) showModelForm = false }">
      <n-card :title="`配置模型：${modelForm?.model_name ?? ''}`" :bordered="false" style="width:440px">
        <n-form label-placement="top" v-if="modelForm">
          <n-form-item label="上下文长度（tokens）">
            <div style="width: 100%">
              <n-input-number v-model:value="modelForm.context_length" :min="0" :step="1000" style="width: 100%" />
              <n-space :size="6" style="margin-top: 6px">
                <n-button
                  v-for="p in CTX_PRESETS"
                  :key="p.value"
                  size="tiny"
                  :type="modelForm.context_length === p.value ? 'primary' : 'default'"
                  quaternary
                  @click="modelForm.context_length = p.value"
                >{{ p.label }}</n-button>
              </n-space>
            </div>
          </n-form-item>
          <n-form-item label="最大输出长度（tokens）">
            <n-input-number v-model:value="modelForm.max_output_length" :min="0" :step="1000" style="width: 100%" />
          </n-form-item>
          <n-button type="primary" block @click="saveModel">保存</n-button>
        </n-form>
      </n-card>
    </n-modal>
    <input ref="importInput" type="file" accept=".json,application/json" style="display: none" @change="onImportFile" />
    <n-modal :show="pendingImport !== null" @update:show="(show: boolean) => { if (!show) pendingImport = null }">
      <n-card title="导入配置" :bordered="false" style="width:440px">
        <p style="margin: 0 0 4px; color: var(--text-2); font-size: 13.5px; line-height: 1.7">
          {{ pendingImport ? describeConfigFile(pendingImport) : '' }}
        </p>
        <n-space justify="end" style="margin-top: 12px">
          <n-button size="small" @click="pendingImport = null">取消</n-button>
          <n-button type="primary" size="small" :loading="importing" @click="doImport">开始导入</n-button>
        </n-space>
      </n-card>
    </n-modal>
    <n-modal :show="showHistory" @update:show="(show: boolean) => { if (!show) showHistory = false }">
      <n-card :title="`余额/额度历史：${historyUpstream?.name ?? ''}`" :bordered="false" style="width:640px">
        <n-data-table
          :bordered="false"
          size="small"
          :columns="historyColumns"
          :data="historyRows"
          :loading="historyLoading"
          :row-key="(row: BalanceSnapshot) => row.id"
        />
        <div style="display: flex; justify-content: flex-end; margin-top: 12px">
          <n-pagination
            v-model:page="historyPage"
            :item-count="historyTotal"
            :page-size="historyPageSize"
            @update:page="loadHistory"
          />
        </div>
      </n-card>
    </n-modal>
  </div>
</template>
