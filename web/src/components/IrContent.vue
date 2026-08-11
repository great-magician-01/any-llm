<script lang="ts">
import MarkdownIt from 'markdown-it'

/** 模块级单例；默认选项（html: false）——归档内容按文本转义渲染，杜绝 XSS */
const md = new MarkdownIt()
</script>

<script setup lang="ts">
import { computed } from 'vue'
import { formatInt } from '../utils/format'
import type { IRContentBlock } from '../utils/ir'

interface BlockView {
  kind: 'text' | 'markdown' | 'thinking' | 'tool_use' | 'tool_result' | 'image' | 'unknown'
  text: string
  long: boolean
  toolName: string
  isError: boolean
  imageURL: string
  mediaType: string
}

const props = defineProps<{ blocks: IRContentBlock[]; role: 'user' | 'assistant' }>()

/** 工具结果超长折叠阈值（字符数） */
const COLLAPSE_LEN = 2000

/** 美化 JSON 值：对象直接缩进序列化；字符串先尝试按 JSON 解析再缩进，失败则原样返回 */
function pretty(v: unknown): string {
  if (v == null) return ''
  if (typeof v === 'string') {
    try {
      return JSON.stringify(JSON.parse(v), null, 2)
    } catch {
      return v
    }
  }
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}

function base(partial: Partial<BlockView> & Pick<BlockView, 'kind'>): BlockView {
  return { text: '', long: false, toolName: '', isError: false, imageURL: '', mediaType: '', ...partial }
}

function toView(b: IRContentBlock): BlockView {
  switch (b.Type) {
    case 'text':
      // 助手文本按 Markdown 渲染，用户文本按纯文本展示
      return base({ kind: props.role === 'assistant' ? 'markdown' : 'text', text: b.Text || '' })
    case 'thinking':
      return base({ kind: 'thinking', text: b.Thinking || '' })
    case 'redacted_thinking':
      return base({ kind: 'thinking', text: '' })
    case 'tool_use':
      return base({ kind: 'tool_use', text: pretty(b.ToolUse?.Input), toolName: b.ToolUse?.Name || 'tool' })
    case 'tool_result': {
      const text = pretty(b.ToolResult?.Content)
      return base({ kind: 'tool_result', text, long: text.length > COLLAPSE_LEN, isError: !!b.ToolResult?.IsError })
    }
    case 'image':
      return base({ kind: 'image', imageURL: b.Image?.URL || '', mediaType: b.Image?.MediaType || '' })
    default:
      return base({ kind: 'unknown', text: b.Text || '' })
  }
}

const views = computed(() => props.blocks.map(toView))

function renderMd(text: string): string {
  return md.render(text)
}
</script>

<template>
  <div class="ir-content">
    <div v-if="!views.length" class="dim">（无内容）</div>
    <template v-for="(v, i) in views" :key="i">
      <!-- 用户纯文本 -->
      <div v-if="v.kind === 'text'" class="plain-text">{{ v.text }}</div>

      <!-- 助手 Markdown -->
      <div v-else-if="v.kind === 'markdown'" class="md" v-html="renderMd(v.text)"></div>

      <!-- 思考过程（折叠） -->
      <div v-else-if="v.kind === 'thinking'" class="sub-block">
        <n-collapse>
          <n-collapse-item name="t">
            <template #header>
              <span class="dim">{{ v.text ? `思考过程（${formatInt(v.text.length)} 字符）` : '思考过程（已加密隐藏）' }}</span>
            </template>
            <pre v-if="v.text" class="mono pre-wrap sub-body dim">{{ v.text }}</pre>
            <div v-else class="dim">该思考内容已被加密隐藏</div>
          </n-collapse-item>
        </n-collapse>
      </div>

      <!-- 工具调用 -->
      <div v-else-if="v.kind === 'tool_use'" class="sub-block">
        <div class="tool-head">
          <n-tag size="small" :bordered="false" type="info">{{ v.toolName }}</n-tag>
        </div>
        <pre class="mono pre-wrap sub-body">{{ v.text }}</pre>
      </div>

      <!-- 工具结果（超长折叠） -->
      <div v-else-if="v.kind === 'tool_result'" class="sub-block">
        <div class="tool-head">
          <n-tag size="small" :bordered="false" :type="v.isError ? 'error' : 'default'">
            {{ v.isError ? '工具结果（错误）' : '工具结果' }}
          </n-tag>
        </div>
        <n-collapse v-if="v.long">
          <n-collapse-item name="r">
            <template #header><span class="dim">结果内容（{{ formatInt(v.text.length) }} 字符，点击展开）</span></template>
            <pre class="mono pre-wrap sub-body">{{ v.text }}</pre>
          </n-collapse-item>
        </n-collapse>
        <pre v-else class="mono pre-wrap sub-body">{{ v.text }}</pre>
      </div>

      <!-- 图片 -->
      <div v-else-if="v.kind === 'image'" class="sub-block">
        <n-image v-if="v.imageURL" :src="v.imageURL" :width="320" object-fit="contain" />
        <span v-else class="dim">[base64 图片{{ v.mediaType ? `（${v.mediaType}）` : '' }}]</span>
      </div>

      <!-- 未知类型兜底 -->
      <pre v-else class="mono pre-wrap sub-body">{{ v.text }}</pre>
    </template>
  </div>
</template>

<style scoped>
.ir-content {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
  font-size: 13.5px;
}
.dim {
  color: var(--text-3);
  font-size: 12.5px;
}
.plain-text {
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.65;
}
.pre-wrap {
  white-space: pre-wrap;
  word-break: break-word;
}
.sub-block {
  min-width: 0;
}
.sub-body {
  margin: 6px 0 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: rgba(2, 6, 18, 0.5);
  border: 1px solid var(--border-soft);
  font-size: 12.5px;
  line-height: 1.6;
  max-height: 480px;
  overflow: auto;
}
.tool-head {
  margin-bottom: 2px;
}
/* markdown 渲染结果的基础排版（v-html 内容需 :deep 选择器） */
.md {
  line-height: 1.65;
  word-break: break-word;
}
.md :deep(p) {
  margin: 0 0 8px;
}
.md :deep(p:last-child) {
  margin-bottom: 0;
}
.md :deep(h1),
.md :deep(h2),
.md :deep(h3),
.md :deep(h4) {
  margin: 12px 0 6px;
  color: var(--text);
  font-size: 15px;
}
.md :deep(ul),
.md :deep(ol) {
  margin: 4px 0 8px;
  padding-left: 20px;
}
.md :deep(pre) {
  margin: 8px 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: rgba(2, 6, 18, 0.5);
  border: 1px solid var(--border-soft);
  overflow-x: auto;
}
.md :deep(code) {
  font-family: ui-monospace, 'SF Mono', Consolas, 'Courier New', monospace;
  font-size: 12.5px;
}
.md :deep(:not(pre) > code) {
  padding: 1px 5px;
  border-radius: 4px;
  background: rgba(148, 163, 184, 0.14);
}
.md :deep(a) {
  color: var(--brand-hover);
}
.md :deep(blockquote) {
  margin: 8px 0;
  padding: 2px 12px;
  border-left: 3px solid var(--border);
  color: var(--text-3);
}
.md :deep(table) {
  border-collapse: collapse;
  margin: 8px 0;
}
.md :deep(th),
.md :deep(td) {
  border: 1px solid var(--border-soft);
  padding: 4px 10px;
  font-size: 12.5px;
}
</style>
