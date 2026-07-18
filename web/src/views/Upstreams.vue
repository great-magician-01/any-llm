<script setup lang="ts">
import { ref, onMounted, h } from 'vue'
import { NButton, NSpace, NTag, NPopconfirm, NInput, NText, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { listUpstreams, createUpstream, updateUpstream, deleteUpstream, fetchModels as fetchUpsModels, listModels, addModel, deleteModel, type Upstream, type UpstreamModel } from '../api/upstreams'

const message = useMessage()
const upstreams = ref<Upstream[]>([])
const showForm = ref(false)
const form = ref<Upstream & { fetch_models?: boolean }>({ name: '', base_url: '', api_key: '', format: 'openai', fetch_models: true })
const editing = ref<Upstream | null>(null)
const expandedRowKeys = ref<number[]>([])
const modelsByUpstream = ref<Record<number, UpstreamModel[]>>({})
const newModelByUpstream = ref<Record<number, string>>({})
const fetchingId = ref<number | null>(null)

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

async function load() { upstreams.value = await listUpstreams() }
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
function resetForm() { form.value = { name: '', base_url: '', api_key: '', format: 'openai', fetch_models: true } }
// When editing, keep the masked key returned by the list endpoint as the
// field value. The backend detects the masked placeholder and skips
// overwriting the stored secret; if the user types a new key, it gets saved.
function edit(u: Upstream) { editing.value = u; form.value = { ...u }; showForm.value = true }
function add() { editing.value = null; resetForm(); showForm.value = true }
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
  await addModel(id, name)
  newModelByUpstream.value[id] = ''
  await loadModels(id)
  await load()
}
async function delM(id: number, mid: number) {
  await deleteModel(mid)
  await loadModels(id)
  await load()
}

const columns: DataTableColumns<Upstream> = [
  { type: 'expand', expandable: () => true, renderExpand: (row) => {
    const id = row.id as number
    const models = modelsByUpstream.value[id] || []
    return h('div', { style: 'padding: 8px 0 16px 24px' }, [
      h('div', { class: 'toolbar', style: 'margin-bottom: 8px' }, [
        h(NInput, {
          value: newModelByUpstream.value[id] || '',
          'onUpdate:value': (v: string) => { newModelByUpstream.value[id] = v },
          placeholder: '模型名，如 gpt-4o',
          style: 'width: 240px',
          onKeyup: (e: KeyboardEvent) => { if (e.key === 'Enter') addM(id) },
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
              onClose: () => delM(id, m.id),
            }, { default: () => m.model_name + (m.manual ? '（手动）' : '') }))
          ),
    ])
  }},
  { title: '名称', key: 'name' },
  { title: '地址', key: 'base_url', ellipsis: { tooltip: true } },
  { title: '格式', key: 'format', width: 100 },
  { title: '模型数', key: 'model_count', width: 90 },
  { title: '操作', key: 'actions', width: 200, render: (row) => h(NSpace, { size: 8 }, {
    default: () => [
      h(NButton, { size: 'small', onClick: () => edit(row) }, { default: () => '编辑' }),
      h(NButton, {
        size: 'small',
        loading: fetchingId.value === row.id,
        disabled: fetchingId.value !== null,
        onClick: () => fetchM(row.id as number),
      }, { default: () => '拉取模型' }),
      h(NPopconfirm, { onPositiveClick: () => del(row.id as number) }, {
        trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, { default: () => '删除' }),
        default: () => '确定删除？',
      }),
    ],
  })},
]

onMounted(load)
</script>

<template>
  <div>
    <header class="page-header">
      <h1>上游管理</h1>
      <p>配置上游 LLM 服务，支持 OpenAI / Anthropic 格式。点击行前箭头展开查看模型</p>
    </header>

    <n-card title="上游列表" class="panel">
      <template #header-extra>
        <n-button type="primary" size="small" @click="add">添加上游</n-button>
      </template>
      <n-data-table
        :bordered="false"
        :columns="columns"
        :data="upstreams"
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
            </n-radio-group>
          </n-form-item>
          <n-button type="primary" block @click="save">{{ editing ? '保存' : '添加' }}</n-button>
        </n-form>
      </n-card>
    </n-modal>
  </div>
</template>
