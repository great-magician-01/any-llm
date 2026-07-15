<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { fetchSummary, fetchRecords, type UsageSummary, type UsageRecord } from '../api/usage'

const groupBy = ref('model')
const summaries = ref<UsageSummary[]>([])
const records = ref<UsageRecord[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20

async function load() {
  summaries.value = await fetchSummary(groupBy.value)
  const r = await fetchRecords(page.value, pageSize)
  records.value = r.data
  total.value = r.total
}

onMounted(load)
</script>

<template>
  <n-space vertical size="large">
    <n-space>
      <n-text>分组：</n-text>
      <n-radio-group v-model:value="groupBy" @update:value="load">
        <n-radio-button value="model">按模型</n-radio-button>
        <n-radio-button value="upstream">按上游</n-radio-button>
        <n-radio-button value="key">按 Key</n-radio-button>
      </n-radio-group>
    </n-space>
    <n-data-table :columns="[
      { title: groupBy === 'key' ? 'Key ID' : (groupBy === 'upstream' ? '上游' : '模型'), key: 'group_key' },
      { title: '请求数', key: 'request_count' },
      { title: '总 Token', key: 'total_tokens' },
      { title: '输入 Token', key: 'prompt_tokens' },
      { title: '输出 Token', key: 'completion_tokens' },
      { title: '成功', key: 'ok_count' },
      { title: '失败', key: 'error_count' },
    ]" :data="summaries" style="margin-bottom:24px" />

    <n-text>请求明细 (共 {{ total }} 条)</n-text>
    <n-data-table :columns="[
      { title: '时间', key: 'created_at', width: 160 },
      { title: '上游', key: 'upstream_name' },
      { title: '模型', key: 'model' },
      { title: '入格式', key: 'in_format', width: 100 },
      { title: '出格式', key: 'up_format', width: 100 },
      { title: 'Token', key: 'total_tokens', width: 80 },
      { title: '状态', key: 'status', width: 80 },
    ]" :data="records" :pagination="{ page: page, pageSize, itemCount: total, onChange: (p: number) => { page = p; load() } }" />
  </n-space>
</template>
