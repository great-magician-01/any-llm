<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { NButton, NSpace, NTag, NPopconfirm, NInput, NInputNumber, NSwitch, NText, NDatePicker, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { createUpstream, updateUpstream, deleteUpstream, fetchModels as fetchUpsModels, listModels, addModel, updateModel, deleteModel, testUpstream, testUpstreamConfig, DEFAULT_MODEL_CONTEXT_LENGTH, DEFAULT_MODEL_MAX_OUTPUT_LENGTH, type Upstream, type UpstreamModel } from '@/api/upstreams'
import { listBalanceHistory, refreshBalance, refreshAllBalances, type BalanceSnapshot } from '@/api/balances'
import { exportConfig, importConfig, type ConfigFile } from '@/api/config'
import { configFileName, parseConfigFile, describeConfigFile, describeImportResult, downloadJSON } from '@/utils/configTransfer'
import { balanceView, balanceSummary, balanceTooltip, formatFetchedAt } from '@/utils/balance'
import { expiryLabel, expiryToISO, isoToExpiry } from '@/utils/upstreamStatus'
import { connectivityView, type ConnectivityView } from '@/utils/connectivity'
import { presetSelectOptions, findPreset } from '@/utils/upstreamPresets'
import { formatInt, formatTime } from '@/utils/format'
import { useUpstreamList } from '@/composables/useUpstreamList'
import { useContainerWidth, fitColumns, fitScrollX, UPSTREAM_FIT_COLUMNS } from '@/composables/useTableFit'
import AppIcon from '@/components/AppIcon.vue'

const message = useMessage()
// 列表 + 启用状态过滤 + 余额快照：两套皮肤共用（含请求序号守卫与切档只重查
// 列表），见 composables/useUpstreamList.ts
const { upstreams, statusFilter, balancesByUpstream, load, setStatusFilter, toggleEnabled } = useUpstreamList()
// 表格随容器宽度连续自适应：列宽按比例收窄、操作列按钮自动换行，
// scroll-x 永不超过容器宽，任何屏宽都不出横向滚动条（见 useTableFit）
const tableBox = ref<HTMLElement | null>(null)
const { width: tableWidth } = useContainerWidth(tableBox)
const showForm = ref(false)
const form = ref<Upstream & { fetch_models?: boolean }>({ name: '', base_url: '', api_key: '', format: 'openai', extra_endpoints: [], remark: '', enabled: true, daily_token_limit: 0, monthly_token_limit: 0, max_concurrent: 100, expires_at: null, fetch_models: true })
// 日期选择器的 v-model 是 epoch ms（n-date-picker 默认行为），保存时再转成
// ISO 字符串；null = 不填 = 永久有效。
const expiryPicker = ref<number | null>(null)
const editing = ref<Upstream | null>(null)
// 内置上游预设快捷选择：只在「添加」时展示（编辑已有上游不套模板）。选中
// 带出 base_url、format 与附加端点，名称/Key/限额仍由用户填，带出后字段也
// 保持可改；清空选择（自定义）不动已填内容。
const presetKey = ref<string | null>(null)
const presetOptions = presetSelectOptions()
const activePreset = computed(() => findPreset(presetKey.value))
function onPresetSelect(key: string | null) {
  presetKey.value = key
  const p = findPreset(key)
  if (!p) return
  form.value.base_url = p.baseUrl
  form.value.format = p.format
  form.value.extra_endpoints = (p.extra ?? []).map(e => ({ format: e.format, base_url: e.baseUrl }))
}
// 附加格式端点：其余协议格式的原生入口，入站请求格式命中时网关原生直通
// （不再转译）。同一 format 只允许出现一次且不得与主格式重复（后端同样
// 400 拒绝）——有重复时表单内提示并阻止保存，由用户删除重复项。
const FORMAT_LABELS: Record<string, string> = { openai: 'OpenAI', anthropic: 'Anthropic', responses: 'Responses' }
const extraEndpointError = computed(() => {
  const eps = form.value.extra_endpoints ?? []
  const seen = new Set<string>()
  for (const ep of eps) {
    if (ep.format === form.value.format) return `附加端点与主格式重复（${FORMAT_LABELS[ep.format] ?? ep.format}），请删除重复项`
    if (seen.has(ep.format)) return `附加端点格式重复（${FORMAT_LABELS[ep.format] ?? ep.format}），请删除重复项`
    seen.add(ep.format)
  }
  return ''
})
// 三种协议中主格式占一种，附加端点最多再加其余两种
const canAddEndpoint = computed(() => (form.value.extra_endpoints ?? []).length < 2)
function addExtraEndpoint() {
  if (!canAddEndpoint.value) return
  const used = new Set([form.value.format, ...(form.value.extra_endpoints ?? []).map(e => e.format)])
  const free = ['openai', 'anthropic', 'responses'].find(f => !used.has(f)) ?? 'openai'
  form.value.extra_endpoints = [...(form.value.extra_endpoints ?? []), { format: free, base_url: '' }]
}
function removeExtraEndpoint(i: number) {
  form.value.extra_endpoints = (form.value.extra_endpoints ?? []).filter((_, j) => j !== i)
}
const expandedRowKeys = ref<number[]>([])
const modelsByUpstream = ref<Record<number, UpstreamModel[]>>({})
const newModelByUpstream = ref<Record<number, string>>({})
const newModelOpts = ref<Record<number, { context_length: number; max_output_length: number; multimodal: boolean }>>({})
const showModelForm = ref(false)
const modelFormUpstreamId = ref(0)
const modelForm = ref<UpstreamModel | null>(null)
const fetchingId = ref<number | null>(null)
const refreshingId = ref<number | null>(null)
const testingId = ref<number | null>(null)
// 表单内连通性测试：结果按 成功/警告/失败 三档就地展示在按钮下方
const formTesting = ref(false)
const formTestResult = ref<ConnectivityView | null>(null)
// 附加端点的逐个测试结果（主端点结果仍在 formTestResult）
const formExtraTestResults = ref<{ format: string; view: ConnectivityView }[]>([])
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
    newModelOpts.value[id] = { context_length: DEFAULT_MODEL_CONTEXT_LENGTH, max_output_length: DEFAULT_MODEL_MAX_OUTPUT_LENGTH, multimodal: false }
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
    // 文件里带禁用上游而当前只看「启用中」时它们不进列表——提醒一句，
    // 免得以为没导进去。
    if (payload.upstreams.some((u) => u.enabled === false) && statusFilter.value === 'enabled') {
      message.warning('导入包含已禁用的上游；当前只看「启用中」，切到「已禁用」或「全部」可见')
    }
    await load()
  } catch (e) {
    message.error('导入失败：' + errMsg(e))
  } finally {
    importing.value = false
  }
}
async function save() {
  try {
    // 有效期由独立的选择器 ref 持有，提交前转成 ISO 覆写——这样清空选择器时
    // 会显式发出 null（后端据此清除有效期），而不是把字段整个省掉。
    form.value.expires_at = expiryToISO(expiryPicker.value)
    // 备注同样在提交前归一：去掉首尾空白，没填就是空串（后端不 trim，与 keys 一致）
    form.value.remark = (form.value.remark ?? '').trim()
    // 附加端点归一与重复检查：撞车主格式/列表内重复时阻止保存并提示删除
    // 重复项（后端同样 400，这里是更早、更可操作的提示）。
    if (extraEndpointError.value) {
      message.error(extraEndpointError.value)
      return
    }
    form.value.extra_endpoints = (form.value.extra_endpoints ?? []).map(e => ({ format: e.format, base_url: e.base_url.trim() }))
    if (form.value.extra_endpoints.some(e => !e.base_url)) {
      message.warning('附加端点的 Base URL 不能为空，请填写或删除该行')
      return
    }
    const created = !editing.value?.id
    const createdEnabled = form.value.enabled
    if (!created) {
      await updateUpstream(editing.value!.id!, form.value)
    } else {
      await createUpstream(form.value)
    }
    showForm.value = false
    editing.value = null
    resetForm()
    await load()
    if (!created) {
      message.success('已保存')
    } else if (!createdEnabled && statusFilter.value === 'enabled') {
      // 新建为禁用而当前只看「启用中」时新行不可见——没有提示就像没建成，
      // 重复提交会撞同名 400。
      message.warning('已添加为禁用状态；当前只看「启用中」，切到「已禁用」或「全部」可见')
    } else {
      message.success('已添加')
    }
  } catch (e) {
    message.error('保存失败：' + errMsg(e))
  }
}
function resetForm() { form.value = { name: '', base_url: '', api_key: '', format: 'openai', extra_endpoints: [], remark: '', enabled: true, daily_token_limit: 0, monthly_token_limit: 0, max_concurrent: 100, expires_at: null, fetch_models: true }; expiryPicker.value = null; presetKey.value = null; formExtraTestResults.value = [] }
// When editing, keep the masked key returned by the list endpoint as the
// field value. The backend detects the masked placeholder and skips
// overwriting the stored secret; if the user types a new key, it gets saved.
// extra_endpoints 深拷贝进表单：直接引用列表行的数组会让编辑中的改动（含
// 未保存就取消）直接写进列表数据。
function edit(u: Upstream) { editing.value = u; form.value = { ...u, extra_endpoints: (u.extra_endpoints ?? []).map(e => ({ ...e })) }; expiryPicker.value = isoToExpiry(u.expires_at); presetKey.value = null; formTestResult.value = null; formExtraTestResults.value = []; showForm.value = true }
function add() { editing.value = null; resetForm(); formTestResult.value = null; showForm.value = true }
async function del(id: number) { await deleteUpstream(id); await load() }
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
  try {
    await addModel(id, name, opts.context_length, opts.max_output_length, opts.multimodal)
  } catch (e) {
    // 后端对已存在的同名模型回 409：必须把错误摆出来，否则按钮看似没反应
    message.error('添加模型失败：' + errMsg(e))
    return
  }
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
    await updateModel(modelFormUpstreamId.value, m.id, m.context_length, m.max_output_length, m.multimodal)
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

async function testRow(u: Upstream) {
  if (testingId.value !== null) return
  testingId.value = u.id as number
  try {
    const v = connectivityView(await testUpstream(u.id as number))
    const text = `「${u.name}」${v.text}`
    if (v.type === 'success') message.success(text, { duration: 6000 })
    else if (v.type === 'warning') message.warning(text, { duration: 8000 })
    else message.error(text, { duration: 8000 })
  } catch (e) {
    message.error('测试失败：' + errMsg(e))
  } finally {
    testingId.value = null
  }
}

// 表单内测试：编辑态把表单当前值（可能已改过地址/key）作为覆盖发给 by-id 端点
// ——key 还是掩码或留空时后端沿用库存真 key；新增态直接测表单里的配置。
// 附加端点逐个各测一次（同样走覆盖/掩码 key 约定），结果按格式分行展示。
async function testForm() {
  if (formTesting.value) return
  if (!form.value.base_url.trim()) {
    message.warning('请先填写 Base URL')
    return
  }
  formTesting.value = true
  formTestResult.value = null
  formExtraTestResults.value = []
  try {
    const r = editing.value?.id
      ? await testUpstream(editing.value.id, { base_url: form.value.base_url, api_key: form.value.api_key, format: form.value.format })
      : await testUpstreamConfig({ base_url: form.value.base_url, api_key: form.value.api_key, format: form.value.format })
    formTestResult.value = connectivityView(r)
  } catch (e) {
    formTestResult.value = { type: 'error', text: '测试失败：' + errMsg(e) }
  }
  for (const ep of form.value.extra_endpoints ?? []) {
    if (!ep.base_url.trim()) continue
    try {
      const r = editing.value?.id
        ? await testUpstream(editing.value.id, { base_url: ep.base_url, api_key: form.value.api_key, format: ep.format })
        : await testUpstreamConfig({ base_url: ep.base_url, api_key: form.value.api_key, format: ep.format })
      formExtraTestResults.value = [...formExtraTestResults.value, { format: ep.format, view: connectivityView(r) }]
    } catch (e) {
      formExtraTestResults.value = [...formExtraTestResults.value, { format: ep.format, view: { type: 'error', text: '测试失败：' + errMsg(e) } }]
    }
  }
  formTesting.value = false
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

// 列宽集中在一处 computed：容器变窄时按比例压缩（操作列压缩空间最大，按钮
// 随之换行），列宽规格两套皮肤共用（UPSTREAM_FIT_COLUMNS）；scroll-x 取
// min(列宽总和, 容器宽)，任何屏宽都不会出横向滚动条。glass 此前刻意不设
// scroll-x，但那样容器过窄时定宽列会把表格撑出卡片——统一改用自适应后，
// scroll-x 只是 fixed 布局的开关，永远不大于容器宽，不会再有滚动条。
const colW = computed(() => fitColumns(tableWidth.value, UPSTREAM_FIT_COLUMNS))
const scrollX = computed(() => fitScrollX(colW.value, tableWidth.value))

// columns 改为 computed：容器宽度变化只重建列配置，不逐像素重算
const columns = computed<DataTableColumns<Upstream>>(() => [
  { type: 'expand', width: colW.value.expand, expandable: () => true, renderExpand: (row) => {
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
            'onUpdate:value': (v: number | null) => { opts.context_length = v ?? DEFAULT_MODEL_CONTEXT_LENGTH },
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
          'onUpdate:value': (v: number | null) => { opts.max_output_length = v ?? DEFAULT_MODEL_MAX_OUTPUT_LENGTH },
          min: 0,
          step: 1000,
          placeholder: '最大输出长度',
          style: 'width: 150px',
        }),
        h('div', { style: 'display: flex; align-items: center; gap: 6px' }, [
          h(NSwitch, {
            value: opts.multimodal,
            size: 'small',
            'onUpdate:value': (v: boolean) => { opts.multimodal = v },
          }),
          h('span', { style: 'font-size: 13px; color: var(--text-2)' }, '多模态'),
        ]),
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
              m.multimodal ? h('span', { style: 'opacity: 0.75; font-size: 11px; margin-left: 6px' }, '多模态') : null,
              h('span', { style: 'opacity: 0.6; font-size: 11px; margin-left: 6px' }, `${fmtK(m.context_length)} / ${fmtK(m.max_output_length)}`),
            ] }))
          ),
    ])
  }},
  // 所有列宽都来自 colW（随容器宽度自适应）；文本列配 ellipsis + tooltip，
  // 收窄时截断而不是把表格撑出横向滚动条
  { title: '名称', key: 'name', width: colW.value.name, ellipsis: { tooltip: true }, render: (row) => h('span', { style: 'font-weight: 600; color: var(--text)' }, row.name) },
  // 备注紧跟名称：窄屏首屏就能看到，空值显示「—」
  {
    title: '备注',
    key: 'remark',
    width: colW.value.remark,
    ellipsis: { tooltip: true },
    render: (row) => (row.remark
      ? h('span', { style: 'color: var(--text-2)' }, row.remark)
      : h('span', { style: 'color: var(--text-4)' }, '—')),
  },
  { title: '状态', key: 'enabled', width: colW.value.status, render: (row) => h(NSwitch, {
      value: row.enabled, size: 'small', 'onUpdate:value': (v: boolean) => toggleEnabled(row, v) }) },
  {
    title: '有效期至',
    key: 'expires_at',
    width: colW.value.expiry,
    render: (row) => {
      const { text, tone } = expiryLabel(row)
      if (tone === 'error') return h(NTag, { type: 'error', bordered: false, size: 'small' }, { default: () => text })
      if (tone === 'muted') return h('span', { style: 'color: var(--text-4)' }, text)
      return h('span', { style: 'font-size: 12.5px' }, text)
    },
  },
  { title: '地址', key: 'base_url', width: colW.value.baseUrl, ellipsis: { tooltip: true }, render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, row.base_url) },
  {
    title: '格式',
    key: 'format',
    width: colW.value.format,
    // 主格式 + 附加端点格式各一个 tag；附加的半透明显示，主从一眼可辨
    render: (row) => h('div', { style: 'display: flex; gap: 4px; flex-wrap: wrap' }, [
      h(NTag, { type: row.format === 'openai' ? 'info' : 'warning', bordered: false, size: 'small' }, { default: () => row.format }),
      ...(row.extra_endpoints ?? []).map(e => h(NTag, {
        type: e.format === 'openai' ? 'info' : 'warning', bordered: false, size: 'small', style: 'opacity: 0.6',
      }, { default: () => e.format })),
    ]),
  },
  {
    title: '模型数',
    key: 'model_count',
    width: colW.value.modelCount,
    render: (row) => h('span', { class: 'mono' }, formatInt(row.model_count ?? 0)),
  },
  {
    title: '余额/额度',
    key: 'balance',
    width: colW.value.balance,
    render: (row) => {
      const s = balancesByUpstream.value[row.id as number]
      const v = s ? balanceView(s) : null
      if (!s || !v) return h('span', { style: 'color: var(--text-4)' }, '-')
      const dim = 'color: var(--text-4); font-size: 12px'
      const lines = v.kind === 'balance'
        ? [h('div', { class: 'mono' }, v.text)]
        : v.windows.map(w => h('div', { class: 'mono' }, w.missing
          ? [h('span', { style: 'color: var(--text-4)' }, `${w.label} —`)]
          : [h('span', null, `${w.label} ${w.percent}%`),
            ...(w.reset ? [h('span', { style: dim }, `（${w.reset} 重置）`)] : [])]))
      lines.push(h('div', { style: dim }, `更新于 ${formatFetchedAt(s.created_at)}`))
      return h('div', { style: 'cursor: pointer; line-height: 1.5', title: balanceTooltip(s), onClick: () => openHistory(row) }, lines)
    },
  },
  {
    title: '日 token 上限',
    key: 'daily_token_limit',
    width: colW.value.dailyLimit,
    render: (row) => row.daily_token_limit > 0
      ? h('span', { class: 'mono' }, formatInt(row.daily_token_limit))
      : h('span', { style: 'color: var(--text-4)' }, '不限'),
  },
  {
    title: '月 token 上限',
    key: 'monthly_token_limit',
    width: colW.value.monthlyLimit,
    render: (row) => row.monthly_token_limit > 0
      ? h('span', { class: 'mono' }, formatInt(row.monthly_token_limit))
      : h('span', { style: 'color: var(--text-4)' }, '不限'),
  },
  {
    title: '并发上限',
    key: 'max_concurrent',
    width: colW.value.maxConcurrent,
    render: (row) => row.max_concurrent > 0
      ? h('span', { class: 'mono' }, formatInt(row.max_concurrent))
      : h('span', { style: 'color: var(--text-4)' }, '不限'),
  },
  // 操作列宽来自 colW（压缩空间最大的一列），NSpace 允许换行——容器越窄
  // 按钮换行越多，表格本身永不出现横向滚动条
  { title: '操作', key: 'actions', width: colW.value.actions, render: (row) => h(NSpace, { size: 8, wrap: true }, {
    default: () => [
      h(NButton, { size: 'small', onClick: () => edit(row) }, { default: () => '编辑' }),
      h(NButton, {
        size: 'small',
        loading: testingId.value === row.id,
        disabled: testingId.value !== null,
        onClick: () => testRow(row),
      }, { default: () => '测试' }),
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
])

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
        <p>配置上游 LLM 服务；一个上游可挂多种协议格式端点，入站格式命中时原生直通。点击行前箭头展开查看模型</p>
      </div>
      <div class="page-header-side">
        <n-button quaternary circle @click="load">
          <template #icon><AppIcon name="refresh" :size="16" /></template>
        </n-button>
      </div>
    </header>

    <n-card title="上游列表" class="panel">
      <template #header-extra>
        <n-space :size="8" align="center">
          <n-radio-group :value="statusFilter" size="small" @update:value="setStatusFilter">
            <n-radio-button value="enabled">启用中</n-radio-button>
            <n-radio-button value="disabled">已禁用</n-radio-button>
            <n-radio-button value="all">全部</n-radio-button>
          </n-radio-group>
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
      <!-- tableBox 量出表格可用宽度，驱动各列自适应（见 useTableFit） -->
      <div ref="tableBox">
        <n-data-table
          :bordered="false"
          :columns="columns"
          :data="upstreams"
          :scroll-x="scrollX"
          :row-key="(row: Upstream) => row.id"
          :expanded-row-keys="expandedRowKeys"
          @update:expanded-row-keys="onExpand"
        />
      </div>
    </n-card>

    <n-modal :show="showForm" @update:show="(show: boolean) => { if (!show) showForm = false }">
      <n-card :title="editing ? '编辑上游' : '添加上游'" :bordered="false" style="width:560px">
        <n-form label-placement="top">
          <n-form-item v-if="!editing" label="快捷配置">
            <div style="width: 100%">
              <n-select
                :value="presetKey"
                :options="presetOptions"
                clearable
                filterable
                placeholder="自定义（手动填写下方字段）"
                @update:value="onPresetSelect"
              />
              <div v-if="activePreset" style="margin-top: 6px; font-size: 12px; line-height: 1.6; color: var(--text-3)">
                <span class="mono">{{ activePreset.baseUrl }}</span><span v-if="activePreset.hint">，{{ activePreset.hint }}</span>
              </div>
            </div>
          </n-form-item>
          <n-form-item label="名称"><n-input v-model:value="form.name" /></n-form-item>
          <n-form-item label="备注"><n-input v-model:value="form.remark" placeholder="备注（可选）" /></n-form-item>
          <n-form-item label="Base URL"><n-input v-model:value="form.base_url" /></n-form-item>
          <n-form-item label="API Key">
            <n-input
              v-model:value="form.api_key"
              type="password"
              :placeholder="editing ? '未修改将保持原 key' : '请输入 API Key'"
            />
          </n-form-item>
          <n-form-item label="格式（主）">
            <n-radio-group v-model:value="form.format">
              <n-radio value="openai">OpenAI</n-radio>
              <n-radio value="anthropic">Anthropic</n-radio>
              <n-radio value="responses">Responses</n-radio>
            </n-radio-group>
          </n-form-item>
          <n-form-item label="附加格式端点（可选）">
            <div style="width: 100%">
              <div v-for="(ep, i) in form.extra_endpoints ?? []" :key="i" style="display: flex; gap: 8px; margin-bottom: 8px; align-items: center">
                <n-select
                  v-model:value="ep.format"
                  :options="[
                    { label: 'OpenAI', value: 'openai' },
                    { label: 'Anthropic', value: 'anthropic' },
                    { label: 'Responses', value: 'responses' },
                  ]"
                  style="width: 140px"
                />
                <n-input v-model:value="ep.base_url" placeholder="该格式端点的 Base URL" style="flex: 1" />
                <n-button size="small" quaternary @click="removeExtraEndpoint(i)">移除</n-button>
              </div>
              <n-button size="small" dashed block :disabled="!canAddEndpoint" @click="addExtraEndpoint">添加端点</n-button>
              <div style="margin-top: 6px; font-size: 12px; line-height: 1.6; color: var(--text-3)">
                该上游以其它协议格式提供服务的入口（如 DeepSeek 的 Anthropic 端点）。入站请求格式命中时原生直通，不再转译；未命中走主格式。
              </div>
              <n-alert v-if="extraEndpointError" type="warning" :bordered="false" style="margin-top: 8px">
                {{ extraEndpointError }}
              </n-alert>
            </div>
          </n-form-item>
          <n-form-item label="连通性">
            <div style="width: 100%">
              <n-button size="small" :loading="formTesting" @click="testForm">测试连通性</n-button>
              <n-alert v-if="formTestResult" :type="formTestResult.type" :bordered="false" style="margin-top: 8px">
                {{ formTestResult.text }}
              </n-alert>
              <n-alert
                v-for="r in formExtraTestResults"
                :key="r.format"
                :type="r.view.type"
                :bordered="false"
                style="margin-top: 8px"
              >
                [{{ FORMAT_LABELS[r.format] ?? r.format }}] {{ r.view.text }}
              </n-alert>
            </div>
          </n-form-item>
          <n-form-item label="启用"><n-switch v-model:value="form.enabled" /></n-form-item>
          <n-form-item label="有效期至">
            <n-date-picker
              v-model:value="expiryPicker"
              type="datetime"
              clearable
              placeholder="不填表示永久有效"
              style="width: 100%"
            />
          </n-form-item>
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
          <n-form-item label="并发上限">
            <n-input-number
              v-model:value="form.max_concurrent"
              :min="0"
              :step="10"
              placeholder="0 表示不限，默认 100"
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
          <n-form-item label="多模态">
            <n-space align="center" :size="8">
              <n-switch v-model:value="modelForm.multimodal" />
              <span style="font-size: 12.5px; color: var(--text-3)">支持图片等非文本输入</span>
            </n-space>
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
