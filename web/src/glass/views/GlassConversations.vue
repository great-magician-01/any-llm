<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { useMessage, NTag, NButton } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { fetchConversations, fetchConversation, type ConversationListItem, type ConversationDetail } from '../../api/conversations'
import { formatCompact, formatInt, formatTime } from '../../utils/format'
import { parseIR, type IRRequest, type IRResponse } from '../../utils/ir'
import AppIcon from '../../components/AppIcon.vue'
import IrContent from '../../components/IrContent.vue'

const message = useMessage()
const rows = ref<ConversationListItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
// SQLite 下后端返回 disabled: true（对话归档仅 PostgreSQL 记录）
const disabled = ref(false)

async function load() {
  try {
    const r = await fetchConversations(page.value, pageSize)
    rows.value = r.data
    total.value = r.total
    disabled.value = !!r.disabled
  } catch (e: any) {
    message.error('加载失败：' + (e?.response?.data?.error || e?.message || String(e)))
  }
}

const columns: DataTableColumns<ConversationListItem> = [
  { title: '时间', key: 'created_at', width: 165, render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, formatTime(row.created_at)) },
  { title: '模型', key: 'model', render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, row.model) },
  { title: '上游', key: 'upstream_name' },
  {
    title: 'Harness',
    key: 'harness',
    width: 110,
    render: (row) =>
      row.harness
        ? h(NTag, { size: 'small', bordered: false, type: 'info' }, { default: () => row.harness })
        : h('span', { style: 'color: var(--text-4)' }, '—'),
  },
  { title: '格式', key: 'format', width: 170, render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, `${row.in_format} → ${row.up_format}`) },
  { title: 'Tokens', key: 'total_tokens', width: 90, render: (row) => h('span', { class: 'mono', style: 'font-weight: 600' }, formatCompact(row.total_tokens)) },
  {
    title: '流式',
    key: 'stream',
    width: 80,
    render: (row) => h(NTag, { size: 'tiny', bordered: false, quaternary: true, type: row.stream ? 'info' : 'default' }, { default: () => (row.stream ? '流式' : '非流式') }),
  },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render: (row) => h(NTag, { size: 'small', bordered: false, type: row.status === 'ok' ? 'success' : 'error' }, { default: () => (row.status === 'ok' ? '成功' : '失败') }),
  },
  {
    title: '操作',
    key: 'actions',
    width: 70,
    render: (row) => h(NButton, { text: true, type: 'primary', size: 'small', onClick: () => openDetail(row) }, { default: () => '查看' }),
  },
]

/* ---- 详情抽屉 ---- */
const showDrawer = ref(false)
const detail = ref<ConversationDetail | null>(null)
const showRaw = ref(false)

const reqIR = computed(() => (detail.value ? parseIR<IRRequest>(detail.value.request_ir) : null))
const respIR = computed(() => (detail.value ? parseIR<IRResponse>(detail.value.response_ir) : null))
const systemBlocks = computed(() => reqIR.value?.System ?? [])
const messages = computed(() => reqIR.value?.Messages ?? [])
const replyBlocks = computed(() => respIR.value?.Content ?? [])

async function openDetail(row: ConversationListItem) {
  showDrawer.value = true
  detail.value = null
  showRaw.value = false
  try {
    detail.value = await fetchConversation(row.id)
  } catch (e: any) {
    showDrawer.value = false
    message.error('加载详情失败：' + (e?.response?.data?.error || e?.message || String(e)))
  }
}

/** 美化 IR 原始 JSON 文本，解析失败时原样展示 */
function prettyRaw(json: string): string {
  try {
    return JSON.stringify(JSON.parse(json), null, 2)
  } catch {
    return json
  }
}

onMounted(load)
</script>

<template>
  <div>
    <header class="page-header">
      <div>
        <h1>对话记录</h1>
        <p>查看归档的完整请求与响应内容（仅 PostgreSQL 记录）</p>
      </div>
      <div class="page-header-side">
        <n-button quaternary circle @click="load">
          <template #icon><AppIcon name="refresh" :size="16" /></template>
        </n-button>
      </div>
    </header>

    <n-card v-if="disabled" class="panel">
      <n-empty description="对话归档仅在使用 PostgreSQL（DB_TYPE=postgres）时记录" />
    </n-card>

    <n-card v-else title="对话明细" class="panel">
      <template #header-extra>
        <span class="toolbar-label">共 {{ formatInt(total) }} 条</span>
      </template>
      <n-data-table
        :bordered="false"
        :columns="columns"
        :data="rows"
        :pagination="{ page: page, pageSize, itemCount: total, onChange: (p: number) => { page = p; load() } }"
      />
    </n-card>

    <n-drawer v-model:show="showDrawer" :width="720" placement="right">
      <n-drawer-content :title="detail ? `对话详情 #${detail.id}` : '对话详情'" closable>
        <div v-if="!detail" class="loading-hint">加载中…</div>
        <template v-else>
          <n-descriptions :column="2" size="small" bordered>
            <n-descriptions-item label="模型">
              <span class="mono">{{ detail.model }}</span>
            </n-descriptions-item>
            <n-descriptions-item label="上游">{{ detail.upstream_name }}</n-descriptions-item>
            <n-descriptions-item label="Harness">{{ detail.harness || '—' }}</n-descriptions-item>
            <n-descriptions-item label="格式">
              <span class="mono">{{ detail.in_format }} → {{ detail.up_format }}</span>
            </n-descriptions-item>
            <n-descriptions-item label="User-Agent" :span="2">
              <span class="mono ua-text">{{ detail.user_agent || '—' }}</span>
            </n-descriptions-item>
            <n-descriptions-item label="时间">
              <span class="mono">{{ formatTime(detail.created_at) }}</span>
            </n-descriptions-item>
            <n-descriptions-item label="流式">{{ detail.stream ? '流式' : '非流式' }}</n-descriptions-item>
            <n-descriptions-item label="Tokens" :span="2">
              <span class="mono">
                输入 {{ formatInt(detail.prompt_tokens) }} · 输出 {{ formatInt(detail.completion_tokens) }} · 合计 {{ formatInt(detail.total_tokens) }}
              </span>
              <span v-if="detail.cache_read_tokens || detail.cache_creation_tokens || detail.reasoning_tokens" class="mono tokens-extra">
                （缓存读 {{ formatInt(detail.cache_read_tokens) }} · 缓存写 {{ formatInt(detail.cache_creation_tokens) }} · 推理 {{ formatInt(detail.reasoning_tokens) }}）
              </span>
            </n-descriptions-item>
            <n-descriptions-item label="状态">
              <n-tag size="small" :bordered="false" :type="detail.status === 'ok' ? 'success' : 'error'">
                {{ detail.status === 'ok' ? '成功' : '失败' }}
              </n-tag>
            </n-descriptions-item>
          </n-descriptions>

          <div class="raw-toggle">
            <span>原始 JSON</span>
            <n-switch v-model:value="showRaw" size="small" />
          </div>

          <template v-if="!showRaw">
            <n-collapse v-if="systemBlocks.length" class="system-collapse">
              <n-collapse-item name="system">
                <template #header><span class="sys-title">系统提示（{{ systemBlocks.length }} 段）</span></template>
                <pre v-for="(s, i) in systemBlocks" :key="i" class="mono pre-wrap sys-text">{{ s.Text }}</pre>
              </n-collapse-item>
            </n-collapse>

            <div v-if="reqIR" class="msg-list">
              <div v-for="(m, i) in messages" :key="i" class="msg" :class="m.Role">
                <div class="msg-role">{{ m.Role === 'user' ? '用户' : '助手' }}</div>
                <IrContent :blocks="m.Content ?? []" :role="m.Role" />
              </div>
              <div v-if="replyBlocks.length" class="msg assistant">
                <div class="msg-role reply">本次回复</div>
                <IrContent :blocks="replyBlocks" role="assistant" />
              </div>
            </div>
            <div v-else class="empty-hint">请求内容为空或 IR 解析失败</div>
          </template>

          <template v-else>
            <div class="raw-title mono">request_ir</div>
            <pre class="mono pre-wrap raw-block">{{ prettyRaw(detail.request_ir) }}</pre>
            <div class="raw-title mono">response_ir</div>
            <pre class="mono pre-wrap raw-block">{{ prettyRaw(detail.response_ir) }}</pre>
          </template>
        </template>
      </n-drawer-content>
    </n-drawer>
  </div>
</template>

<style scoped>
.loading-hint,
.empty-hint {
  padding: 40px 0;
  text-align: center;
  color: var(--text-4);
  font-size: 13px;
}
.ua-text {
  font-size: 12.5px;
  word-break: break-all;
}
.tokens-extra {
  color: var(--text-3);
  font-size: 12px;
}
.raw-toggle {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  margin: 16px 0 4px;
  font-size: 13px;
  color: var(--text-3);
}
.system-collapse {
  margin-top: 12px;
}
.sys-title {
  color: var(--text-3);
  font-size: 12.5px;
}
.sys-text {
  margin: 6px 0 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid var(--border-soft);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  color: var(--text-3);
  font-size: 12.5px;
  line-height: 1.6;
  max-height: 320px;
  overflow: auto;
}
.pre-wrap {
  white-space: pre-wrap;
  word-break: break-word;
}
.msg-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
  margin-top: 16px;
}
.msg {
  border-left: 2px solid rgba(255, 255, 255, 0.16);
  padding: 2px 0 2px 14px;
  min-width: 0;
}
.msg.assistant {
  border-left-color: var(--brand);
}
.msg-role {
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: var(--text-3);
  margin-bottom: 6px;
}
.msg.assistant .msg-role {
  color: var(--brand-hover);
}
.msg-role.reply {
  color: var(--brand-2);
}
.raw-title {
  margin: 14px 0 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-3);
}
.raw-block {
  margin: 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid var(--border-soft);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  font-size: 12.5px;
  line-height: 1.6;
  max-height: 480px;
  overflow: auto;
}
</style>
