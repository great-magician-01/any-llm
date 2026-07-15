<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { listUpstreams, createUpstream, updateUpstream, deleteUpstream, fetchModels as fetchUpsModels, type Upstream } from '../api/upstreams'
import ModelEditor from '../components/ModelEditor.vue'

const upstreams = ref<Upstream[]>([])
const showForm = ref(false)
const form = ref<Upstream & { fetch_models?: boolean }>({ name: '', base_url: '', api_key: '', format: 'openai', fetch_models: true })
const editing = ref<Upstream | null>(null)
const modelEditorId = ref(0)
const showModels = ref(false)

async function load() { upstreams.value = await listUpstreams() }
async function save() {
  if (editing.value?.id) {
    await updateUpstream(editing.value.id, form.value)
  } else {
    await createUpstream(form.value)
  }
  showForm.value = false
  editing.value = null
  resetForm()
  await load()
}
function resetForm() { form.value = { name: '', base_url: '', api_key: '', format: 'openai', fetch_models: true } }
function edit(u: Upstream) { editing.value = u; form.value = { ...u }; showForm.value = true }
function add() { editing.value = null; resetForm(); showForm.value = true }
async function del(id: number) { await deleteUpstream(id); await load() }
async function fetchM(id: number) { await fetchUpsModels(id); await load() }

onMounted(load)
</script>

<template>
  <n-space vertical size="large">
    <n-space>
      <n-button type="primary" @click="add">添加上游</n-button>
    </n-space>
    <n-data-table :columns="[
      { title: '名称', key: 'name' },
      { title: '地址', key: 'base_url', ellipsis: { tooltip: true } },
      { title: '格式', key: 'format', width: 100 },
      { title: '操作', key: 'actions', width: 280 }
    ]" :data="upstreams">
      <template #actions="{ row }">
        <n-space>
          <n-button size="small" @click="edit(row)">编辑</n-button>
          <n-button size="small" @click="fetchM(row.id)">拉取模型</n-button>
          <n-button size="small" @click="showModels = true; modelEditorId = row.id">模型</n-button>
          <n-popconfirm @positive-click="del(row.id)">
            <template #trigger><n-button size="small" type="error">删除</n-button></template>
            确定删除？
          </n-popconfirm>
        </n-space>
      </template>
    </n-data-table>

    <n-modal :show="showForm" @update:show="(show: boolean) => { if (!show) showForm = false }">
      <n-card :title="editing ? '编辑上游' : '添加上游'" style="width:500px">
        <n-form>
          <n-form-item label="名称"><n-input v-model:value="form.name" /></n-form-item>
          <n-form-item label="Base URL"><n-input v-model:value="form.base_url" /></n-form-item>
          <n-form-item label="API Key"><n-input v-model:value="form.api_key" type="password" /></n-form-item>
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

    <ModelEditor :upstream-id="modelEditorId" :show="showModels" @close="showModels = false" />
  </n-space>
</template>
