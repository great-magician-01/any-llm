<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { useMessage, NPopconfirm, NButton, NInputNumber, NTag, NSpace, NModal, NCard, NForm, NFormItem, NInput, NSwitch, NAlert } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { listKeys, createKey, updateKey, deleteKey, getKeyUsage, type ExtKey, type UsageTotals } from '../api/keys'

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
  { title: '备注', key: 'label' },
  { title: 'Key', key: 'key' },
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
    render: (row) => row.daily_token_limit > 0 ? String(row.daily_token_limit) : '不限',
  },
  {
    title: '月 token 上限',
    key: 'monthly_token_limit',
    width: 130,
    render: (row) => row.monthly_token_limit > 0 ? String(row.monthly_token_limit) : '不限',
  },
  {
    title: '今日 / 本月用量',
    key: 'usage',
    width: 180,
    render: (row) => {
      const u = usageByKey.value[row.id]
      if (!u) return h('span', { style: 'color: var(--text-3)' }, '—')
      const daily = u.daily_tokens
      const monthly = u.monthly_tokens
      const dailyS = row.daily_token_limit > 0 ? `${daily} / ${row.daily_token_limit}` : String(daily)
      const monthlyS = row.monthly_token_limit > 0 ? `${monthly} / ${row.monthly_token_limit}` : String(monthly)
      return h(NSpace, { vertical: true, size: 0 }, {
        default: () => [
          h('span', { style: 'font-size: 12px' }, '日: ' + dailyS),
          h('span', { style: 'font-size: 12px' }, '月: ' + monthlyS),
        ],
      })
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 140,
    render(row) {
      return h(NSpace, { size: 4 }, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => openEdit(row) }, { default: () => '编辑' }),
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

function copyKey(key: string) {
  try {
    const ok = execCopy(key)
    if (ok) {
      message.success('已复制到剪贴板')
    } else {
      message.error('复制失败，请手动选择复制')
    }
  } catch {
    message.error('复制失败，请手动选择复制')
  }
}

function execCopy(text: string): boolean {
  const ta = document.createElement('textarea')
  ta.setAttribute('readonly', '')
  ta.value = text
  ta.style.position = 'absolute'
  ta.style.left = '-9999px'
  ta.style.top = (window.pageYOffset || document.documentElement.scrollTop) + 'px'
  document.body.appendChild(ta)
  ta.focus()
  ta.select()
  ta.setSelectionRange(0, 99999)
  const ok = document.execCommand('copy')
  document.body.removeChild(ta)
  return ok
}

onMounted(load)
</script>

<template>
  <div>
    <header class="page-header">
      <h1>API 密钥</h1>
      <p>对外访问网关使用的 Key，请求时通过 Authorization: Bearer 携带</p>
    </header>

    <n-card title="访问端点" class="panel">
      <p style="margin: 0 0 12px; color: var(--text-3); font-size: 13px">
        可直接复制以下 URL 作为客户端 base_url，模型名格式：<code>upstream-name/model-name</code>
      </p>
      <n-space vertical :size="10">
        <n-input-group v-for="ep in endpoints" :key="ep.url">
          <n-tag :bordered="false" type="info" style="min-width: 64px; justify-content: center">{{ ep.method }}</n-tag>
          <n-input :value="ep.url" readonly style="font-family: monospace" />
          <n-button type="primary" @click="copyKey(ep.url)">复制</n-button>
        </n-input-group>
      </n-space>
    </n-card>

    <n-card title="密钥列表" class="panel">
      <template #header-extra>
        <n-button type="primary" size="small" @click="openCreate">新增密钥</n-button>
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
          <n-alert type="warning" style="margin-bottom: 12px">
            请立即复制保存此密钥。出于安全考虑，关闭后将无法再次完整查看。
          </n-alert>
          <n-input-group>
            <n-input :value="newlyCreatedKey" readonly style="font-family: monospace" />
            <n-button type="primary" @click="copyKey(newlyCreatedKey)">复制</n-button>
          </n-input-group>
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
