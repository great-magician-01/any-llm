<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { useMessage, NPopconfirm, NButton } from 'naive-ui'
import { listKeys, createKey, deleteKey, type ExtKey } from '../api/keys'

const message = useMessage()
const keys = ref<ExtKey[]>([])
const label = ref('')
const newlyCreatedKey = ref('')
const showNewKeyModal = ref(false)

const origin = computed(() => window.location.origin)
const endpoints = computed(() => [
  { label: 'OpenAI - Chat Completions', method: 'POST', url: `${origin.value}/v1/chat/completions` },
  { label: 'OpenAI - Models', method: 'GET', url: `${origin.value}/v1/models` },
  { label: 'Anthropic - Messages', method: 'POST', url: `${origin.value}/v1/messages` },
])

const columns = computed(() => [
  { title: '备注', key: 'label' },
  { title: 'Key', key: 'key' },
  {
    title: '操作',
    key: 'actions',
    width: 100,
    render(row: ExtKey) {
      return h(
        NPopconfirm,
        {
          onPositiveClick: () => { del(row.id) },
        },
        {
          trigger: () =>
            h(NButton, { size: 'small', type: 'error', quaternary: true }, { default: () => '删除' }),
          default: () => '确定删除此 key？删除后该 key 不可用。',
        },
      )
    },
  },
])

async function load() { keys.value = await listKeys() }
async function add() {
  if (label.value.trim()) {
    const k = await createKey(label.value.trim())
    newlyCreatedKey.value = k.key
    showNewKeyModal.value = true
    label.value = ''
    await load()
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
      <div class="toolbar">
        <n-input
          v-model:value="label"
          placeholder="备注，如：我的应用"
          style="width: 240px"
          @keyup.enter="add"
        />
        <n-button type="primary" @click="add">生成 Key</n-button>
      </div>
      <n-data-table :bordered="false" :columns="columns" :data="keys" />
    </n-card>

    <n-modal :show="showNewKeyModal" @update:show="(s: boolean) => showNewKeyModal = s">
      <n-card title="密钥已生成" :bordered="false" style="width:560px" role="dialog" aria-modal="true">
        <n-alert type="warning" style="margin-bottom: 12px">
          请立即复制保存此密钥。出于安全考虑，关闭后将无法再次完整查看。
        </n-alert>
        <n-input-group>
          <n-input :value="newlyCreatedKey" readonly style="font-family: monospace" />
          <n-button type="primary" @click="copyKey(newlyCreatedKey)">复制</n-button>
        </n-input-group>
        <template #footer>
          <div style="text-align: right">
            <n-button @click="showNewKeyModal = false">我已保存</n-button>
          </div>
        </template>
      </n-card>
    </n-modal>
  </div>
</template>
