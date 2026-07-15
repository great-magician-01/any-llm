<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { listKeys, createKey, deleteKey, type ExtKey } from '../api/keys'

const keys = ref<ExtKey[]>([])
const label = ref('')
const showNew = ref(false)

async function load() { keys.value = await listKeys() }
async function add() {
  if (label.value.trim()) {
    await createKey(label.value.trim())
    label.value = ''
    showNew.value = true
    await load()
  }
}
async function del(id: number) { await deleteKey(id); await load() }

onMounted(load)
</script>

<template>
  <n-space vertical size="large">
    <n-space>
      <n-input v-model:value="label" placeholder="备注" style="width:200px" />
      <n-button type="primary" @click="add">生成 Key</n-button>
    </n-space>
    <n-data-table :columns="[
      { title: '备注', key: 'label' },
      { title: 'Key', key: 'key' },
      { title: '操作', key: 'actions', width: 100 }
    ]" :data="keys">
      <template #actions="{ row }">
        <n-popconfirm @positive-click="del(row.id)">
          <template #trigger><n-button size="small" type="error">删除</n-button></template>
          确定删除此 key？删除后该 key 将不可用。
        </n-popconfirm>
      </template>
    </n-data-table>
  </n-space>
</template>
