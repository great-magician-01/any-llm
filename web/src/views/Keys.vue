<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { useMessage, NPopconfirm, NButton, NInputNumber, NTag, NSpace, NModal, NCard, NForm, NFormItem, NInput, NSwitch, NAlert, NProgress, NTooltip } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { listKeys, createKey, updateKey, deleteKey, getKeyUsage, type ExtKey, type UsageTotals } from '../api/keys'
import { listUpstreams, listModels } from '../api/upstreams'
import { formatInt } from '../utils/format'
import { buildOmpYaml } from '../utils/ompConfig'
import AppIcon from '../components/AppIcon.vue'

const message = useMessage()
const keys = ref<ExtKey[]>([])
const usageByKey = ref<Record<number, UsageTotals>>({})

// create modal: 'form' = filling form, 'done' = showing generated key
const showCreateModal = ref(false)
const createModalState = ref<'form' | 'done'>('form')
const createForm = ref({ label: '', daily_token_limit: 0, monthly_token_limit: 0 })
const newlyCreatedKey = ref('')

// edit modal
const showEditModal = ref(false)
const editing = ref<ExtKey | null>(null)
const editForm = ref({ label: '', enabled: true, daily_token_limit: 0, monthly_token_limit: 0 })

const origin = computed(() => window.location.origin)
const endpoints = computed(() => [
  { label: 'OpenAI - Chat Completions', method: 'POST', url: `${origin.value}/v1/chat/completions` },
  { label: 'OpenAI - Models', method: 'GET', url: `${origin.value}/v1/models` },
  { label: 'Anthropic - Messages', method: 'POST', url: `${origin.value}/v1/messages` },
])

const columns = computed<DataTableColumns<ExtKey>>(() => [
  { title: '备注', key: 'label', render: (row) => h('span', { style: 'font-weight: 600; color: var(--text)' }, row.label) },
  {
    title: 'Key',
    key: 'key',
    render: (row) => h('div', { style: 'display: flex; align-items: center; gap: 6px' }, [
      h('code', { class: 'mono key-chip' }, row.key),
      h(
        NTooltip,
        { trigger: 'hover' },
        {
          trigger: () =>
            h(NButton, { size: 'tiny', quaternary: true, onClick: (e: MouseEvent) => copyKey(row.key, e) }, {
              icon: () => h(AppIcon, { name: 'copy', size: 13 }),
            }),
          default: () => '复制 Key',
        },
      ),
    ]),
  },
  {
    title: '状态',
    key: 'enabled',
    width: 80,
    render: (row) => h(NTag, { type: row.enabled ? 'success' : 'default', bordered: false }, { default: () => row.enabled ? '启用' : '禁用' }),
  },
  {
    title: '日 token 上限',
    key: 'daily_token_limit',
    width: 130,
    render: (row) => row.daily_token_limit > 0
      ? h('span', { class: 'mono' }, formatInt(row.daily_token_limit))
      : h('span', { style: 'color: var(--text-4)' }, '不限'),
  },
  {
    title: '月 token 上限',
    key: 'monthly_token_limit',
    width: 130,
    render: (row) => row.monthly_token_limit > 0
      ? h('span', { class: 'mono' }, formatInt(row.monthly_token_limit))
      : h('span', { style: 'color: var(--text-4)' }, '不限'),
  },
  {
    title: '今日 / 本月用量',
    key: 'usage',
    width: 200,
    render: (row) => {
      const u = usageByKey.value[row.id]
      if (!u) return h('span', { style: 'color: var(--text-4)' }, '—')
      const line = (label: string, used: number, limit: number) =>
        h('div', { class: 'quota-line' }, [
          h('span', { class: 'quota-text mono' }, `${label} ${formatInt(used)}${limit > 0 ? ' / ' + formatInt(limit) : ''}`),
          limit > 0
            ? h(NProgress, {
                type: 'line',
                percentage: Math.min(100, Math.round((used / limit) * 100)),
                status: used >= limit ? 'error' : used / limit >= 0.8 ? 'warning' : 'success',
                height: 5,
                showIndicator: false,
                borderRadius: '3px',
              })
            : null,
        ])
      return h('div', { class: 'quota-cell' }, [
        line('日', u.daily_tokens, row.daily_token_limit),
        line('月', u.monthly_tokens, row.monthly_token_limit),
      ])
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 260,
    render(row) {
      return h(NSpace, { size: 4 }, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => openEdit(row) }, { default: () => '编辑' }),
          h(
            NTooltip,
            { trigger: 'hover' },
            {
              trigger: () =>
                h(NButton, { size: 'small', quaternary: true, onClick: (e: MouseEvent) => copyOpencodeConfig(row.key, e) }, { default: () => 'opencode' }),
              default: () => '复制 opencode 配置 JSON（含此密钥）',
            },
          ),
          h(
            NTooltip,
            { trigger: 'hover' },
            {
              trigger: () =>
                h(NButton, { size: 'small', quaternary: true, onClick: (e: MouseEvent) => copyOmpConfig(row.key, e) }, { default: () => 'OMP' }),
              default: () => '复制 Oh My Pi 配置 YAML（含此密钥）',
            },
          ),
          h(
            NPopconfirm,
            { onPositiveClick: () => { del(row.id) } },
            {
              trigger: () =>
                h(NButton, { size: 'small', type: 'error', quaternary: true }, { default: () => '删除' }),
              default: () => '确定删除此 key？删除后该 key 不可用。',
            },
          ),
        ],
      })
    },
  },
])

async function load() {
  keys.value = await listKeys()
  // load usage in parallel
  const results = await Promise.all(keys.value.map(k => getKeyUsage(k.id).catch(() => null)))
  usageByKey.value = {}
  keys.value.forEach((k, i) => {
    if (results[i]) usageByKey.value[k.id] = results[i] as UsageTotals
  })
}

function openCreate() {
  createForm.value = { label: '', daily_token_limit: 0, monthly_token_limit: 0 }
  newlyCreatedKey.value = ''
  createModalState.value = 'form'
  showCreateModal.value = true
}

async function saveCreate() {
  const label = createForm.value.label.trim()
  if (!label) {
    message.warning('请填写备注')
    return
  }
  try {
    const k = await createKey(label, createForm.value.daily_token_limit, createForm.value.monthly_token_limit)
    newlyCreatedKey.value = k.key
    createModalState.value = 'done'
    await load()
  } catch (e: any) {
    message.error('创建失败：' + (e?.response?.data?.error || e?.message || String(e)))
  }
}

function resetCreateForm() {
  createForm.value = { label: '', daily_token_limit: 0, monthly_token_limit: 0 }
  newlyCreatedKey.value = ''
  createModalState.value = 'form'
}

function openEdit(row: ExtKey) {
  editing.value = row
  editForm.value = {
    label: row.label,
    enabled: row.enabled,
    daily_token_limit: row.daily_token_limit,
    monthly_token_limit: row.monthly_token_limit,
  }
  showEditModal.value = true
}

async function saveEdit() {
  if (!editing.value) return
  try {
    await updateKey(editing.value.id, {
      label: editForm.value.label,
      enabled: editForm.value.enabled,
      daily_token_limit: editForm.value.daily_token_limit,
      monthly_token_limit: editForm.value.monthly_token_limit,
    })
    showEditModal.value = false
    editing.value = null
    await load()
    message.success('已保存')
  } catch (e: any) {
    message.error('保存失败：' + (e?.response?.data?.error || e?.message || String(e)))
  }
}

async function del(id: number) {
  try {
    await deleteKey(id)
    await load()
    message.success('已删除')
  } catch {
    message.error('删除失败')
  }
}

// Aggregate every upstream's models (shared by the opencode / OMP exporters).
// The model id is `upstream-name/model-name`, matching the gateway's /v1/models.
async function collectUpstreamModels() {
  const ups = await listUpstreams()
  const out: Array<{ upstream: string; model_name: string; context_length: number; max_output_length: number }> = []
  await Promise.all(ups.map(async (u) => {
    if (u.id == null) return
    const ms = await listModels(u.id).catch(() => [])
    for (const m of ms) {
      out.push({ upstream: u.name, model_name: m.model_name, context_length: m.context_length, max_output_length: m.max_output_length })
    }
  }))
  return out
}

// opencode custom provider config: aggregate every upstream's models into
// the models map so the copied JSON works out of the box.
async function buildOpencodeConfig(apiKey: string): Promise<string> {
  const models: Record<string, { name: string; limit: { context: number; output: number } }> = {}
  for (const m of await collectUpstreamModels()) {
    const id = `${m.upstream}/${m.model_name}`
    models[id] = { name: id, limit: { context: m.context_length, output: m.max_output_length } }
  }
  const cfg: Record<string, unknown> = {
    $schema: 'https://opencode.ai/config.json',
    provider: {
      'any-llm': {
        npm: '@ai-sdk/openai-compatible',
        name: 'any-llm',
        options: {
          baseURL: `${origin.value}/v1`,
          apiKey,
        },
        models,
      },
    },
  }
  return JSON.stringify(cfg, null, 2)
}

async function buildOmpConfig(apiKey: string): Promise<string> {
  const rows = await collectUpstreamModels()
  return buildOmpYaml({
    baseUrl: origin.value,
    apiKey,
    models: rows.map((m) => ({ id: `${m.upstream}/${m.model_name}`, contextWindow: m.context_length, maxTokens: m.max_output_length })),
  })
}

async function copyOpencodeConfig(apiKey: string, evt?: MouseEvent) {
  try {
    const json = await buildOpencodeConfig(apiKey)
    await copyKey(json, evt)
  } catch (e: any) {
    message.error('生成配置失败：' + (e?.message || String(e)))
  }
}

async function copyOmpConfig(apiKey: string, evt?: MouseEvent) {
  try {
    const yaml = await buildOmpConfig(apiKey)
    await copyKey(yaml, evt)
  } catch (e: any) {
    message.error('生成配置失败：' + (e?.message || String(e)))
  }
}

async function copyKey(key: string, evt?: MouseEvent) {
  // prefer the modern async clipboard API (HTTPS / localhost only)
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(key)
      message.success('已复制到剪贴板')
      return
    } catch {
      // permission denied or non-secure context — fall through
    }
  }
  // legacy fallback for HTTP non-localhost:
  // the temp textarea must live INSIDE the modal (otherwise naive-ui's focus
  // trap steals focus and clears the selection before execCommand runs).
  // We also intercept the copy event to force the correct data in, in case
  // the selection is still lost.
  const anchor = (evt?.currentTarget as HTMLElement | undefined) || (document.activeElement as HTMLElement) || document.body
  let ok = false
  try {
    ok = execCopy(key, anchor)
  } catch {
    ok = false
  }
  if (ok) {
    message.success('已复制到剪贴板')
  } else {
    message.error('复制失败，请手动选择复制')
  }
}

function execCopy(text: string, anchor: HTMLElement): boolean {
  // mount the textarea inside the same modal/card as the clicked button
  // so the modal's focus trap does not steal focus from it.
  const container = anchor.parentElement || document.body
  const ta = document.createElement('textarea')
  ta.setAttribute('readonly', '')
  ta.value = text
  ta.style.position = 'absolute'
  ta.style.left = '-9999px'
  ta.style.top = '0'
  ta.style.width = '1px'
  ta.style.height = '1px'
  ta.style.opacity = '0'
  container.appendChild(ta)

  let ok = false
  // safety net: force the clipboard payload even if the selection is cleared
  const onCopy = (e: ClipboardEvent) => {
    try {
      e.preventDefault()
      e.clipboardData?.setData('text/plain', text)
      ok = true
    } catch {
      // ignore
    }
  }
  document.addEventListener('copy', onCopy)
  try {
    ta.focus()
    ta.select()
    ta.setSelectionRange(0, text.length)
    document.execCommand('copy')
  } finally {
    document.removeEventListener('copy', onCopy)
    try { container.removeChild(ta) } catch { /* already removed */ }
  }
  return ok
}

onMounted(load)
</script>

<template>
  <div>
    <header class="page-header">
      <div>
        <h1>API 密钥</h1>
        <p>对外访问网关使用的 Key，请求时通过 Authorization: Bearer 携带</p>
      </div>
      <div class="page-header-side">
        <n-button quaternary circle @click="load">
          <template #icon><AppIcon name="refresh" :size="16" /></template>
        </n-button>
      </div>
    </header>

    <n-card title="访问端点" class="panel">
      <p style="margin: 0 0 12px; color: var(--text-3); font-size: 13px">
        可直接复制以下 URL 作为客户端 base_url，模型名格式：<code class="mono code-chip">upstream-name/model-name</code>
      </p>
      <n-space vertical :size="10">
        <n-input-group v-for="ep in endpoints" :key="ep.url">
          <n-tag :bordered="false" :type="ep.method === 'POST' ? 'success' : 'info'" class="mono" style="min-width: 64px; justify-content: center">{{ ep.method }}</n-tag>
          <n-input :value="ep.url" readonly style="font-family: monospace" />
          <n-button type="primary" @click="copyKey(ep.url, $event)">
            <template #icon><AppIcon name="copy" :size="14" /></template>
            复制
          </n-button>
        </n-input-group>
      </n-space>
    </n-card>

    <n-card title="密钥列表" class="panel">
      <template #header-extra>
        <n-button type="primary" size="small" @click="openCreate">
          <template #icon><AppIcon name="plus" :size="14" /></template>
          新增密钥
        </n-button>
      </template>
      <n-data-table :bordered="false" :columns="columns" :data="keys" />
    </n-card>

    <n-modal :show="showCreateModal" @update:show="(s: boolean) => { showCreateModal = s }">
      <n-card :title="createModalState === 'form' ? '新增密钥' : '密钥已生成'" :bordered="false" style="width:560px" role="dialog" aria-modal="true">
        <template v-if="createModalState === 'form'">
          <n-form label-placement="top">
            <n-form-item label="备注">
              <n-input v-model:value="createForm.label" placeholder="备注，如：我的应用" />
            </n-form-item>
            <n-form-item label="单日 token 上限（0 = 不限）">
              <n-input-number v-model:value="createForm.daily_token_limit" :min="0" :step="1000" style="width: 100%" />
            </n-form-item>
            <n-form-item label="单月 token 上限（0 = 不限）">
              <n-input-number v-model:value="createForm.monthly_token_limit" :min="0" :step="10000" style="width: 100%" />
            </n-form-item>
          </n-form>
        </template>
        <template v-else>
          <n-alert type="info" style="margin-bottom: 12px">
            密钥已生成，之后也可以随时在列表中查看和复制。
          </n-alert>
          <n-input-group>
            <n-input :value="newlyCreatedKey" readonly style="font-family: monospace" />
            <n-button type="primary" @click="copyKey(newlyCreatedKey, $event)">复制</n-button>
          </n-input-group>
          <n-button block style="margin-top: 12px" @click="copyOpencodeConfig(newlyCreatedKey, $event)">
            <template #icon><AppIcon name="copy" :size="14" /></template>
            复制 opencode 配置 JSON（含此密钥）
          </n-button>
          <n-button block style="margin-top: 8px" @click="copyOmpConfig(newlyCreatedKey, $event)">
            <template #icon><AppIcon name="copy" :size="14" /></template>
            复制 Oh My Pi 配置 YAML（含此密钥）
          </n-button>
        </template>
        <template #footer>
          <div style="text-align: right">
            <template v-if="createModalState === 'form'">
              <n-button @click="showCreateModal = false" style="margin-right: 8px">取消</n-button>
              <n-button type="primary" @click="saveCreate">保存</n-button>
            </template>
            <template v-else>
              <n-button @click="showCreateModal = false" style="margin-right: 8px">关闭</n-button>
              <n-button type="primary" @click="resetCreateForm">再新增</n-button>
            </template>
          </div>
        </template>
      </n-card>
    </n-modal>

    <n-modal :show="showEditModal" @update:show="(s: boolean) => { if (!s) showEditModal = false }">
      <n-card title="编辑密钥" :bordered="false" style="width:500px">
        <n-form label-placement="top">
          <n-form-item label="备注">
            <n-input v-model:value="editForm.label" />
          </n-form-item>
          <n-form-item label="启用">
            <n-switch v-model:value="editForm.enabled" />
          </n-form-item>
          <n-form-item label="单日 token 上限（0 = 不限）">
            <n-input-number v-model:value="editForm.daily_token_limit" :min="0" :step="1000" style="width: 100%" />
          </n-form-item>
          <n-form-item label="单月 token 上限（0 = 不限）">
            <n-input-number v-model:value="editForm.monthly_token_limit" :min="0" :step="10000" style="width: 100%" />
          </n-form-item>
          <n-button type="primary" block @click="saveEdit">保存</n-button>
        </n-form>
      </n-card>
    </n-modal>
  </div>
</template>

<style scoped>
.key-chip {
  padding: 3px 8px;
  border-radius: 6px;
  background: rgba(148, 163, 184, 0.1);
  border: 1px solid var(--border-soft);
  font-size: 12px;
  color: var(--text-2);
}
.code-chip {
  padding: 1px 6px;
  border-radius: 5px;
  background: rgba(148, 163, 184, 0.12);
  color: var(--text-2);
  font-size: 12px;
}
.quota-cell {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 2px 0;
}
.quota-line {
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.quota-text {
  font-size: 12px;
  color: var(--text-3);
}
</style>
