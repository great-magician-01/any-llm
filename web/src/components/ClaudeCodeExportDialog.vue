<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { NModal, NCard, NButton, NSelect, NCheckbox, NAlert, useMessage } from 'naive-ui'
import AppIcon from './AppIcon.vue'
import { useClipboard } from '../composables/useClipboard'
import { collectExportModels, filterAllowedModels } from '../utils/exportModels'
import { buildClaudeCodeSh, type ClaudeCodeMapping } from '../utils/claudeCodeConfig'
import { errText } from '../utils/errText'

// Claude Code（Linux）配置导出弹窗：两套 Keys 皮肤共用一份。
// 把各模型槽位映射到该密钥可用的网关模型后，生成追加到 ~/.bashrc 的
// export 段（变量集与 cc-switch 一致，见 utils/claudeCodeConfig.ts）。
const props = defineProps<{ show: boolean; apiKey: string; allowedModels?: string[] | null }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()

const { copyText } = useClipboard()
const message = useMessage()
const origin = window.location.origin

interface SlotState {
  model: string | null
  oneM: boolean
}

const slotDefs = [
  { key: 'main', label: '主会话模型', desc: 'ANTHROPIC_MODEL' },
  { key: 'fable', label: 'fable 别名', desc: '最强模型档' },
  { key: 'opus', label: 'opus 别名', desc: '复杂推理 / opusplan 计划阶段' },
  { key: 'sonnet', label: 'sonnet 别名', desc: '日常编码 / opusplan 执行阶段' },
  { key: 'haiku', label: 'haiku 别名', desc: '兼标题/摘要等后台小任务（不设置会 404）' },
  { key: 'subagent', label: '子代理模型', desc: 'CLAUDE_CODE_SUBAGENT_MODEL' },
] as const

type SlotKey = (typeof slotDefs)[number]['key']

// 默认：主会话与 haiku 指向清单第一个模型（haiku 不设会让后台任务请求
// 不存在的 haiku 模型而 404），其余槽位不设置。
function freshSlots(first: string | null): Record<SlotKey, SlotState> {
  return {
    main: { model: first, oneM: false },
    fable: { model: null, oneM: false },
    opus: { model: null, oneM: false },
    sonnet: { model: null, oneM: false },
    haiku: { model: first, oneM: false },
    subagent: { model: null, oneM: false },
  }
}

const slots = ref<Record<SlotKey, SlotState>>(freshSlots(null))
const gatewayDiscovery = ref(true)
const disableNonessential = ref(false)
const models = ref<string[]>([])
const loading = ref(false)

const modelOptions = computed(() => models.value.map((m) => ({ label: m, value: m })))

// 每次打开都重新拉模型清单并重置映射（口径与 /v1/models 一致，受限密钥
// 只列白名单内的模型）。
watch(
  () => props.show,
  async (v) => {
    if (!v) return
    loading.value = true
    try {
      const list = filterAllowedModels(await collectExportModels(), props.allowedModels)
      models.value = list.map((m) => m.id)
      slots.value = freshSlots(models.value[0] ?? null)
    } catch (e) {
      // 失败时模型清单保持为空，弹窗会按「没有可用模型」的既有分支提示
      message.error('加载模型清单失败：' + errText(e))
    } finally {
      loading.value = false
    }
  },
)

function onSlotModelChange(key: SlotKey, v: string | null) {
  slots.value[key].model = v
  if (!v) slots.value[key].oneM = false // 清空选择时顺带复位 [1M] 勾选
}

const preview = computed(() => {
  const mapping: ClaudeCodeMapping = {}
  for (const def of slotDefs) {
    const s = slots.value[def.key]
    if (s.model) mapping[def.key] = { model: s.model, oneM: s.oneM }
  }
  return buildClaudeCodeSh({
    baseUrl: origin,
    apiKey: props.apiKey,
    mapping,
    gatewayModelDiscovery: gatewayDiscovery.value,
    disableNonessentialTraffic: disableNonessential.value,
  })
})

async function doCopy(evt?: MouseEvent) {
  await copyText(preview.value, evt)
}
</script>

<template>
  <n-modal :show="show" @update:show="(v: boolean) => emit('update:show', v)">
    <n-card title="Claude Code 配置（Linux）" :bordered="false" style="width: 720px" role="dialog" aria-modal="true">
      <p class="dlg-note">
        把 Claude Code 的各模型槽位映射到该密钥可用的网关模型，生成追加到
        <code class="mono">~/.bashrc</code> / <code class="mono">~/.zshrc</code> 的环境变量段
        （变量集与 cc-switch 一致）。勾选 <code class="mono">[1M]</code> 表示该槽位按 1M 上下文窗口处理
        ——仅客户端侧生效，发给网关的仍是裸模型 id。
      </p>
      <n-alert v-if="!loading && models.length === 0" type="warning" style="margin-bottom: 12px">
        该密钥暂无可用模型：请先在「上游 / 模型别名」添加模型（或放宽该密钥的模型白名单），
        下方只能导出接入地址与密钥。
      </n-alert>
      <div class="slot-rows">
        <div v-for="def in slotDefs" :key="def.key" class="slot-row">
          <div class="slot-label">
            <span class="slot-name">{{ def.label }}</span>
            <span class="slot-desc">{{ def.desc }}</span>
          </div>
          <n-select
            :value="slots[def.key].model"
            :options="modelOptions"
            :loading="loading"
            clearable
            filterable
            placeholder="不设置"
            style="flex: 1"
            @update:value="(v: string | null) => onSlotModelChange(def.key, v)"
          />
          <n-checkbox v-model:checked="slots[def.key].oneM" :disabled="!slots[def.key].model">[1M]</n-checkbox>
        </div>
      </div>
      <div class="opt-rows">
        <n-checkbox v-model:checked="gatewayDiscovery">
          启用网关模型发现（<code class="mono">/model</code> 从网关 /v1/models 拉取可选模型）
        </n-checkbox>
        <n-checkbox v-model:checked="disableNonessential">
          关闭非必要流量（遥测 / 自动更新等；仅限网关出网的内网环境勾选）
        </n-checkbox>
      </div>
      <pre class="preview mono">{{ preview }}</pre>
      <template #footer>
        <div style="text-align: right">
          <n-button style="margin-right: 8px" @click="emit('update:show', false)">关闭</n-button>
          <n-button type="primary" @click="doCopy($event)">
            <template #icon><AppIcon name="copy" :size="14" /></template>
            复制配置
          </n-button>
        </div>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.dlg-note {
  margin: 0 0 14px;
  font-size: 13px;
  color: var(--text-3);
  line-height: 1.7;
}
.slot-rows {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.slot-row {
  display: flex;
  align-items: center;
  gap: 10px;
}
.slot-label {
  width: 190px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
}
.slot-name {
  font-size: 13px;
  color: var(--text);
}
.slot-desc {
  font-size: 12px;
  color: var(--text-4);
}
.opt-rows {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 14px;
}
.preview {
  margin: 14px 0 0;
  padding: 12px;
  max-height: 260px;
  overflow: auto;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
  background: rgba(148, 163, 184, 0.08);
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--text-2);
  white-space: pre;
}
</style>
