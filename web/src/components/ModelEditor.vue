<script setup lang="ts">
import { ref, watch } from 'vue'
import { listModels, addModel, deleteModel, type UpstreamModel } from '../api/upstreams'

const props = defineProps<{ upstreamId: number; show: boolean }>()
const emit = defineEmits(['close'])
const models = ref<UpstreamModel[]>([])
const newName = ref('')

async function load() {
  models.value = await listModels(props.upstreamId)
}
async function add() {
  if (newName.value.trim()) {
    await addModel(props.upstreamId, newName.value.trim())
    newName.value = ''
    await load()
  }
}
async function del(id: number) {
  await deleteModel(id)
  await load()
}

watch(() => props.show, (s) => { if (s) load() })
</script>

<template>
  <n-modal :show="show" @update:show="(show: boolean) => { if (!show) emit('close') }">
    <n-card title="模型管理" style="width:500px">
      <n-space vertical>
        <n-space>
          <n-input v-model:value="newName" placeholder="模型名" style="width:200px" />
          <n-button @click="add">添加</n-button>
        </n-space>
        <n-list>
          <n-list-item v-for="m in models" :key="m.id">
            <n-space justify="space-between" style="width:100%">
              <span>{{ m.model_name }}{{ m.manual ? ' (手动)' : '' }}</span>
              <n-button size="small" @click="del(m.id)">删除</n-button>
            </n-space>
          </n-list-item>
        </n-list>
      </n-space>
    </n-card>
  </n-modal>
</template>
