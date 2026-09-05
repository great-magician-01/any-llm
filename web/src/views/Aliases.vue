<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { useMessage, NPopconfirm, NButton, NTag, NSpace, NModal, NCard, NForm, NFormItem, NInput, NSelect, NTooltip } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { listAliases, createAlias, updateAlias, deleteAlias, type ModelAlias } from '../api/aliases'
import { listUpstreams, listModels, type Upstream, type UpstreamModel } from '../api/upstreams'
import AppIcon from '../components/AppIcon.vue'

const message = useMessage()
const aliases = ref<ModelAlias[]>([])
const upstreams = ref<Upstream[]>([])

// 编辑弹窗
const showModal = ref(false)
const editing = ref<ModelAlias | null>(null)
interface BindingRow { upstream_id: number | null; model_name: string }
const form = ref<{ name: string; bindings: BindingRow[] }>({ name: '', bindings: [] })

// 模型下拉选项按上游缓存，选中上游时按需加载
const modelOptions = ref<Record<number, UpstreamModel[]>>({})

const upstreamOptions = computed(() =>
  upstreams.value.map((u) => ({ label: `${u.name}（${u.format}）`, value: u.id as number })),
)

function modelOptionsFor(upstreamId: number | null) {
  if (!upstreamId) return []
  return (modelOptions.value[upstreamId] || []).map((m) => ({ label: m.model_name, value: m.model_name }))
}

async function ensureModelOptions(upstreamId: number | null) {
  if (!upstreamId || modelOptions.value[upstreamId]) return
  try {
    const ms = await listModels(upstreamId)
    modelOptions.value = { ...modelOptions.value, [upstreamId]: ms }
  } catch {
    // 模型列表拉取失败不阻塞：输入框支持手输任意模型名
  }
}

const columns = computed<DataTableColumns<ModelAlias>>(() => [
  {
    title: '对外模型名',
    key: 'name',
    render: (row) => h('code', { class: 'mono alias-chip' }, row.name),
  },
  {
    title: '绑定链（按故障转移顺序）',
    key: 'bindings',
    render(row) {
      if (!row.bindings?.length) return h('span', { style: 'color: var(--text-4)' }, '—')
      const nodes: any[] = []
      row.bindings.forEach((b, i) => {
        if (i > 0) nodes.push(h(AppIcon, { name: 'arrow', size: 12, style: 'color: var(--text-4); flex: none' }))
        const dead = !b.upstream_name
        // 上游被禁用（而非删除）：名字仍在，但网关解析时会跳过该绑定
        const off = !!b.upstream_name && b.upstream_enabled === false
        nodes.push(
          h(
            NTooltip,
            { trigger: 'hover' },
            {
              trigger: () =>
                h(
                  NTag,
                  { size: 'small', bordered: false, type: dead ? 'error' : off ? 'warning' : i === 0 ? 'success' : 'default' },
                  { default: () => `${b.upstream_name || '上游#' + b.upstream_id} / ${b.model_name}` },
                ),
              default: () => (dead ? '该绑定指向的上游已被删除，请求时会跳过' : off ? '该绑定指向的上游已被禁用，请求时会跳过' : i === 0 ? '首选绑定' : `第 ${i + 1} 顺位（前面全部失败时兜底）`),
            },
          ),
        )
      })
      return h('div', { style: 'display: flex; align-items: center; gap: 6px; flex-wrap: wrap' }, nodes)
    },
  },
  {
    title: '创建时间',
    key: 'created_at',
    width: 170,
    render: (row) => h('span', { style: 'color: var(--text-3); font-size: 12px' }, (row.created_at || '').replace('T', ' ').slice(0, 19)),
  },
  {
    title: '操作',
    key: 'actions',
    width: 150,
    render(row) {
      return h(NSpace, { size: 4 }, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => openEdit(row) }, { default: () => '编辑' }),
          h(
            NPopconfirm,
            { onPositiveClick: () => { del(row.id) } },
            {
              trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, { default: () => '删除' }),
              default: () => '确定删除此别名？删除后使用该别名的请求将失败。',
            },
          ),
        ],
      })
    },
  },
])

async function load() {
  const [as, us] = await Promise.all([listAliases(), listUpstreams()])
  aliases.value = as
  upstreams.value = us
}

function openCreate() {
  editing.value = null
  form.value = { name: '', bindings: [{ upstream_id: null, model_name: '' }] }
  showModal.value = true
}

function openEdit(row: ModelAlias) {
  editing.value = row
  form.value = {
    name: row.name,
    bindings: (row.bindings || []).map((b) => ({ upstream_id: b.upstream_id, model_name: b.model_name })),
  }
  if (form.value.bindings.length === 0) form.value.bindings.push({ upstream_id: null, model_name: '' })
  for (const b of form.value.bindings) ensureModelOptions(b.upstream_id)
  showModal.value = true
}

function addBinding() {
  form.value.bindings.push({ upstream_id: null, model_name: '' })
}

function removeBinding(i: number) {
  form.value.bindings.splice(i, 1)
}

function moveBinding(i: number, dir: -1 | 1) {
  const j = i + dir
  if (j < 0 || j >= form.value.bindings.length) return
  const arr = form.value.bindings
  ;[arr[i], arr[j]] = [arr[j], arr[i]]
}

async function save() {
  const name = form.value.name.trim()
  if (!name) {
    message.warning('请填写对外模型名')
    return
  }
  const rows = form.value.bindings.filter((b) => b.upstream_id || b.model_name.trim())
  if (rows.length === 0) {
    message.warning('请至少添加一条绑定')
    return
  }
  for (let i = 0; i < rows.length; i++) {
    if (!rows[i].upstream_id) {
      message.warning(`第 ${i + 1} 条绑定未选择上游`)
      return
    }
    if (!rows[i].model_name.trim()) {
      message.warning(`第 ${i + 1} 条绑定未填写模型名`)
      return
    }
  }
  const bindings = rows.map((b) => ({ upstream_id: b.upstream_id as number, model_name: b.model_name.trim() }))
  try {
    if (editing.value) {
      await updateAlias(editing.value.id, { name, bindings })
      message.success('已保存')
    } else {
      await createAlias(name, bindings)
      message.success('已创建')
    }
    showModal.value = false
    editing.value = null
    await load()
  } catch (e: any) {
    message.error('保存失败：' + (e?.response?.data?.error || e?.message || String(e)))
  }
}

async function del(id: number) {
  try {
    await deleteAlias(id)
    await load()
    message.success('已删除')
  } catch {
    message.error('删除失败')
  }
}

onMounted(load)
</script>

<template>
  <div>
    <header class="page-header">
      <div>
        <h1>模型别名</h1>
        <p>
          固定对外模型名：客户端直接请求别名（无需 <code class="mono code-chip">upstream/model</code> 格式），
          网关按绑定顺序尝试，失败自动切换到下一个绑定
        </p>
      </div>
      <div class="page-header-side">
        <n-button quaternary circle @click="load">
          <template #icon><AppIcon name="refresh" :size="16" /></template>
        </n-button>
      </div>
    </header>

    <n-card title="别名列表" class="panel">
      <template #header-extra>
        <n-button type="primary" size="small" @click="openCreate">
          <template #icon><AppIcon name="plus" :size="14" /></template>
          新增别名
        </n-button>
      </template>
      <n-data-table :bordered="false" :columns="columns" :data="aliases" />
    </n-card>

    <n-modal :show="showModal" @update:show="(s: boolean) => { showModal = s }">
      <n-card :title="editing ? '编辑别名' : '新增别名'" :bordered="false" style="width:640px" role="dialog" aria-modal="true">
        <n-form label-placement="top">
          <n-form-item label="对外模型名">
            <n-input v-model:value="form.name" placeholder="客户端请求时使用的模型名，如：claude-sonnet 或 my-gpt" />
          </n-form-item>
          <n-form-item label="绑定链（按顺序尝试，全部失败才报错）">
            <div class="binding-list">
              <div v-for="(b, i) in form.bindings" :key="i" class="binding-row">
                <span class="binding-order mono">{{ i + 1 }}</span>
                <n-select
                  :value="b.upstream_id"
                  :options="upstreamOptions"
                  placeholder="选择上游"
                  style="width: 220px"
                  @update:value="(v: number | null) => { b.upstream_id = v; ensureModelOptions(v) }"
                />
                <n-select
                  v-model:value="b.model_name"
                  :options="modelOptionsFor(b.upstream_id)"
                  placeholder="模型名（可手输）"
                  filterable
                  tag
                  style="flex: 1"
                />
                <n-button quaternary circle size="small" :disabled="i === 0" @click="moveBinding(i, -1)">
                  <template #icon><span class="mono">↑</span></template>
                </n-button>
                <n-button quaternary circle size="small" :disabled="i === form.bindings.length - 1" @click="moveBinding(i, 1)">
                  <template #icon><span class="mono">↓</span></template>
                </n-button>
                <n-button quaternary circle size="small" type="error" :disabled="form.bindings.length <= 1" @click="removeBinding(i)">
                  <template #icon><span class="mono">×</span></template>
                </n-button>
              </div>
              <n-button dashed block size="small" @click="addBinding">
                <template #icon><AppIcon name="plus" :size="13" /></template>
                添加绑定（故障转移兜底）
              </n-button>
            </div>
          </n-form-item>
        </n-form>
        <template #footer>
          <div style="text-align: right">
            <n-button style="margin-right: 8px" @click="showModal = false">取消</n-button>
            <n-button type="primary" @click="save">保存</n-button>
          </div>
        </template>
      </n-card>
    </n-modal>
  </div>
</template>

<style scoped>
.alias-chip {
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
.binding-list {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.binding-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.binding-order {
  flex: none;
  width: 18px;
  text-align: center;
  color: var(--text-3);
  font-size: 12px;
}
</style>
