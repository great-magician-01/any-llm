<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NDrawer, NDrawerContent, NButton, NSelect, NAlert, NTag, useMessage } from 'naive-ui'
import AppIcon from '@/components/AppIcon.vue'
import ClaudeCodeExportDialog from '@/components/ClaudeCodeExportDialog.vue'
import { useClipboard } from '@/composables/useClipboard'
import { collectExportModels, filterAllowedModels, type ExportModel } from '@/utils/exportModels'
import { buildOpencodeJson } from '@/utils/opencodeConfig'
import { buildOmpYaml } from '@/utils/ompConfig'
import { buildPiJson } from '@/utils/piConfig'
import { buildDshYaml } from '@/utils/dshConfig'
import { errText } from '@/utils/errText'
import type { ExtKey } from '@/api/keys'

// Agent 接入配置文档抽屉：Keys 两套皮肤共用一份（UsageDocDrawer 的姊妹组件）。
// 把目前已接入的客户端 agent（opencode / Oh My Pi / Pi / dsh / Claude Code）
// 的配置方法集中成文档：每个 agent 给出配置文件位置与粘贴步骤，并按上方选中
// 的密钥生成配置预览与一键复制（受限密钥只生成白名单内的模型）；Claude Code
// 的完整槽位映射导出直接复用 ClaudeCodeExportDialog，不再造第二份。
const props = defineProps<{ show: boolean; keys: ExtKey[] }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()

const { copyText } = useClipboard()
const message = useMessage()

const origin = computed(() => window.location.origin)
const baseV1 = computed(() => `${origin.value}/v1`)

// —— 选中密钥：keys 变化（加载/删除）后始终保持一个有效选择，默认第一把 ——
const selectedKeyId = ref<number | null>(null)
watch(
  [() => props.show, () => props.keys],
  () => {
    if (selectedKeyId.value == null || !props.keys.some((k) => k.id === selectedKeyId.value)) {
      selectedKeyId.value = props.keys[0]?.id ?? null
    }
  },
  { immediate: true },
)
const selectedKey = computed(() => props.keys.find((k) => k.id === selectedKeyId.value) ?? null)
const keyOptions = computed(() => props.keys.map((k) => ({ label: k.label, value: k.id })))

// —— 模型清单：口径与 /v1/models 一致（exportModels 收集）。每次打开抽屉都
// 刷新一次（管理端改了上游/别名要能看到）；失败时保留旧清单，复制路径兜底重试 ——
const modelList = ref<ExportModel[] | null>(null)
watch(
  () => props.show,
  async (s) => {
    if (!s) return
    await ensureModels()
  },
  { immediate: true },
)
async function ensureModels(): Promise<boolean> {
  if (modelList.value !== null) return true
  try {
    modelList.value = await collectExportModels()
    return true
  } catch (e) {
    message.error('加载模型清单失败：' + errText(e))
    return false
  }
}
const modelsReady = computed(() => modelList.value !== null)

// 该密钥实际可导出的模型（受限密钥只保留白名单内的，与每行导出按钮同口径）
function keyModels(): ExportModel[] {
  return filterAllowedModels(modelList.value ?? [], selectedKey.value?.allowed_models)
}

const PREVIEW_PENDING = '# 模型清单加载中，加载完成后自动生成…'

// 四套 JSON/YAML 导出器共用一个入口：复制与预览走同一份生成逻辑
type CopyAgent = 'opencode' | 'omp' | 'pi' | 'dsh'
function buildFor(agent: CopyAgent, key: ExtKey, list: ExportModel[]): string {
  const baseUrl = baseV1.value
  if (agent === 'opencode') return buildOpencodeJson({ baseUrl, apiKey: key.key, models: list })
  const trimmed = list.map((m) => ({ id: m.id, contextWindow: m.contextLength, maxTokens: m.maxOutputLength }))
  if (agent === 'omp') return buildOmpYaml({ baseUrl, apiKey: key.key, models: trimmed })
  if (agent === 'pi') return buildPiJson({ baseUrl, apiKey: key.key, models: trimmed })
  return buildDshYaml({ baseUrl, apiKey: key.key, models: trimmed })
}

const previews = computed<Record<CopyAgent, string> | null>(() => {
  const key = selectedKey.value
  if (!key || !modelsReady.value) return null
  return {
    opencode: buildFor('opencode', key, keyModels()),
    omp: buildFor('omp', key, keyModels()),
    pi: buildFor('pi', key, keyModels()),
    dsh: buildFor('dsh', key, keyModels()),
  }
})

function previewOf(agent: CopyAgent): string {
  return previews.value?.[agent] ?? PREVIEW_PENDING
}

async function copyAgentConfig(agent: CopyAgent, evt?: MouseEvent) {
  const key = selectedKey.value
  if (!key) return
  if (!(await ensureModels())) return
  await copyText(buildFor(agent, key, keyModels()), evt)
}

// Claude Code：最小环境变量段（接入地址 + 密钥 + 网关模型发现），模型在会话内
// 用 /model 选择；完整槽位映射导出点按钮打开共享弹窗。
const ccQuick = computed(() => {
  const token = selectedKey.value?.key ?? 'sk-all-你的密钥'
  return [
    `export ANTHROPIC_BASE_URL=${origin.value}`,
    `export ANTHROPIC_AUTH_TOKEN=${token}`,
    'export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1  # /model 从网关拉取可选模型',
  ].join('\n')
})
const ccShow = ref(false)
function openCcExport() {
  if (!selectedKey.value) return
  ccShow.value = true
}
</script>

<template>
  <n-drawer :show="show" @update:show="(v: boolean) => emit('update:show', v)" :width="720" placement="right">
    <n-drawer-content title="Agent 接入配置" closable>
      <div class="doc">
        <section class="doc-section">
          <h3>选择密钥</h3>
          <p class="doc-note">
            下方各 agent 的配置都按这里选中的密钥生成：密钥明文直接写进配置文本，受限密钥只生成白名单内的模型。
            模型 id 统一为 <code class="mono chip">upstream名称/模型名</code> 或「模型别名」，上游协议由网关自动转换，客户端无需关心。
          </p>
          <n-select v-if="keys.length > 0" v-model:value="selectedKeyId" :options="keyOptions" placeholder="选择密钥" style="margin-top: 10px" />
          <n-alert v-else type="warning" style="margin-top: 10px">
            还没有密钥：请先在「新增密钥」创建一把，再来看各 agent 的配置方法。
          </n-alert>
          <div v-if="selectedKey" class="sel-key">
            <code class="mono key-chip">{{ selectedKey.key }}</code>
            <n-button size="tiny" quaternary @click="copyText(selectedKey.key, $event)">
              <template #icon><AppIcon name="copy" :size="12" /></template>
            </n-button>
            <n-tag v-if="selectedKey.allowed_models?.length" size="small" type="warning" :bordered="false">
              已限制 {{ selectedKey.allowed_models.length }} 个模型
            </n-tag>
          </div>
        </section>

        <section v-if="selectedKey" class="doc-section">
          <h3>opencode</h3>
          <ul>
            <li>配置文件：<code class="mono chip">~/.config/opencode/opencode.json</code>（全局配置；项目根的 <code class="mono">opencode.json</code> 亦可）</li>
            <li>把生成的 JSON 合并进已有配置的 <code class="mono">provider</code> 段（原 provider 保留不动），保存后重启 opencode</li>
            <li>会话里把模型切到 <code class="mono">any-llm/&lt;模型 id&gt;</code>（或模型别名）即走网关</li>
          </ul>
          <div class="code-block">
            <div class="code-head">
              <span>opencode.json · provider 段</span>
              <n-button size="tiny" quaternary @click="copyAgentConfig('opencode', $event)">
                <template #icon><AppIcon name="copy" :size="12" /></template>
                复制 opencode 配置
              </n-button>
            </div>
            <pre class="code-body"><code>{{ previewOf('opencode') }}</code></pre>
          </div>
        </section>

        <section v-if="selectedKey" class="doc-section">
          <h3>Oh My Pi（OMP）</h3>
          <ul>
            <li>配置文件：<code class="mono chip">~/.omp/agent/models.yml</code></li>
            <li>把生成的 YAML 合并进 <code class="mono">providers</code> 段（provider 名固定 any-llm），保存后重启 omp</li>
            <li>Web 搜索无需单独配置搜索模型，当前选中的模型即可；启用方式见生成文本的头注释</li>
          </ul>
          <div class="code-block">
            <div class="code-head">
              <span>models.yml · providers 段</span>
              <n-button size="tiny" quaternary @click="copyAgentConfig('omp', $event)">
                <template #icon><AppIcon name="copy" :size="12" /></template>
                复制 OMP 配置
              </n-button>
            </div>
            <pre class="code-body"><code>{{ previewOf('omp') }}</code></pre>
          </div>
        </section>

        <section v-if="selectedKey" class="doc-section">
          <h3>Pi agent</h3>
          <ul>
            <li>配置文件：<code class="mono chip">~/.pi/agent/models.json</code></li>
            <li>把生成的 JSON 合并进 <code class="mono">providers</code> 段；该文件会被整体解析，不能含注释</li>
            <li>保存后重启 pi</li>
          </ul>
          <div class="code-block">
            <div class="code-head">
              <span>models.json · providers 段</span>
              <n-button size="tiny" quaternary @click="copyAgentConfig('pi', $event)">
                <template #icon><AppIcon name="copy" :size="12" /></template>
                复制 Pi 配置
              </n-button>
            </div>
            <pre class="code-body"><code>{{ previewOf('pi') }}</code></pre>
          </div>
        </section>

        <section v-if="selectedKey" class="doc-section">
          <h3>dsh（DeepSeek Harness）</h3>
          <ul>
            <li>两个文件都要粘贴：<code class="mono chip">~/.dsh/settings.yaml</code>（providers 段）与 <code class="mono chip">~/.dsh/.credentials.yaml</code>（凭据行，扁平的「引用: 密钥」映射）</li>
            <li>生成的文本带段头注释，按注释分别合并进对应文件</li>
            <li>凭据引用名固定 <code class="mono">ANY_LLM_API_KEY</code>，密钥明文只落在 .credentials.yaml，不进路由配置</li>
          </ul>
          <div class="code-block">
            <div class="code-head">
              <span>settings.yaml + .credentials.yaml</span>
              <n-button size="tiny" quaternary @click="copyAgentConfig('dsh', $event)">
                <template #icon><AppIcon name="copy" :size="12" /></template>
                复制 dsh 配置
              </n-button>
            </div>
            <pre class="code-body"><code>{{ previewOf('dsh') }}</code></pre>
          </div>
        </section>

        <section v-if="selectedKey" class="doc-section">
          <h3>Claude Code</h3>
          <ul>
            <li>把环境变量段追加到 <code class="mono chip">~/.bashrc</code> / <code class="mono chip">~/.zshrc</code>，<code class="mono">source</code> 后正常启动 claude</li>
            <li>最小段（下方）只带接入地址与密钥，模型在会话内用 <code class="mono">/model</code> 选择（走网关模型发现）</li>
            <li>完整段可把 fable / opus / sonnet / haiku / 主会话 / 子代理各槽位映射到网关模型（变量集与 cc-switch 一致，支持 <code class="mono">[1M]</code> 上下文标记），点下方按钮生成</li>
          </ul>
          <div class="code-block">
            <div class="code-head">
              <span>shell 环境变量 · 最小段</span>
              <n-button size="tiny" quaternary @click="copyText(ccQuick, $event)">
                <template #icon><AppIcon name="copy" :size="12" /></template>
                复制最小环境变量段
              </n-button>
            </div>
            <pre class="code-body"><code>{{ ccQuick }}</code></pre>
          </div>
          <div class="cc-actions">
            <n-button size="small" type="primary" secondary @click="openCcExport">
              <template #icon><AppIcon name="key" :size="13" /></template>
              打开完整导出（模型槽位映射）
            </n-button>
          </div>
        </section>

        <section class="doc-section">
          <h3>其他客户端</h3>
          <p class="doc-note">
            未列出的客户端（OpenAI / Anthropic 兼容 SDK、curl 等）接入方式见页头的「使用文档」；
            密钥列表每行与新建密钥后的弹窗里也提供同款一键复制入口。
          </p>
        </section>
      </div>
    </n-drawer-content>
  </n-drawer>

  <ClaudeCodeExportDialog v-model:show="ccShow" :api-key="selectedKey?.key ?? ''" :allowed-models="selectedKey?.allowed_models ?? null" />
</template>

<style scoped>
.doc {
  display: flex;
  flex-direction: column;
  gap: 22px;
  padding-bottom: 8px;
}
.doc-section h3 {
  margin: 0 0 10px;
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}
.doc-section ul {
  margin: 0;
  padding-left: 18px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  color: var(--text-2);
  line-height: 1.7;
}
.doc-note {
  margin: 0;
  font-size: 13px;
  color: var(--text-3);
  line-height: 1.7;
}
.chip {
  padding: 1px 6px;
  border-radius: 5px;
  background: rgba(148, 163, 184, 0.12);
  color: var(--text-2);
  font-size: 12px;
}
.sel-key {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 10px;
}
.key-chip {
  padding: 3px 8px;
  border-radius: 6px;
  background: rgba(148, 163, 184, 0.1);
  border: 1px solid var(--border-soft);
  font-size: 12px;
  color: var(--text-2);
}
.code-block {
  margin-top: 10px;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
  overflow: hidden;
}
.code-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 8px 4px 12px;
  font-size: 12px;
  color: var(--text-3);
  background: rgba(148, 163, 184, 0.08);
  border-bottom: 1px solid var(--border-soft);
}
.code-body {
  margin: 0;
  padding: 12px;
  max-height: 300px;
  overflow: auto;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--text-2);
  white-space: pre;
}
.cc-actions {
  margin-top: 10px;
}
</style>
