<script setup lang="ts">
import { computed } from 'vue'
import { NDrawer, NDrawerContent, NButton } from 'naive-ui'
import AppIcon from './AppIcon.vue'
import { useClipboard } from '../composables/useClipboard'

// 客户端接入文档抽屉：Keys 两套皮肤共用一份（内容随当前部署地址 origin 生成）。
defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()

const { copyText } = useClipboard()

const origin = computed(() => window.location.origin)

// 示例代码块。模型名占位用「upstream名称/模型名」，与 /v1/models 返回的 id 一致。
const snips = computed(() => ({
  curlOpenAI: `curl ${origin.value}/v1/chat/completions \\
  -H "Authorization: Bearer all-sk-你的密钥" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "upstream名称/模型名",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'`,
  curlAnthropic: `curl ${origin.value}/v1/messages \\
  -H "x-api-key: all-sk-你的密钥" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "upstream名称/模型名",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "你好"}]
  }'`,
  curlModels: `curl ${origin.value}/v1/models \\
  -H "Authorization: Bearer all-sk-你的密钥"`,
  openaiSdk: `from openai import OpenAI

client = OpenAI(
    base_url="${origin.value}/v1",  # OpenAI 兼容客户端填 /v1 后缀
    api_key="all-sk-你的密钥",
)

resp = client.chat.completions.create(
    model="upstream名称/模型名",  # 或模型别名
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.choices[0].message.content)`,
  anthropicSdk: `import anthropic

client = anthropic.Anthropic(
    base_url="${origin.value}",  # Anthropic SDK 填根地址，自拼 /v1/messages
    api_key="all-sk-你的密钥",
)

msg = client.messages.create(
    model="upstream名称/模型名",  # 或模型别名
    max_tokens=1024,
    messages=[{"role": "user", "content": "你好"}],
)
print(msg.content[0].text)`,
  claudeCode: `export ANTHROPIC_BASE_URL=${origin.value}
export ANTHROPIC_AUTH_TOKEN=all-sk-你的密钥
export ANTHROPIC_MODEL=upstream名称/模型名  # 可选，或模型别名`,
}))
</script>

<template>
  <n-drawer :show="show" @update:show="(v: boolean) => emit('update:show', v)" :width="680" placement="right">
    <n-drawer-content title="使用文档 · 客户端接入" closable>
      <div class="doc">
        <section class="doc-section">
          <h3>接入信息</h3>
          <ul>
            <li>OpenAI 兼容客户端 base_url：<code class="mono chip">{{ origin }}/v1</code>（SDK 自动拼 <code class="mono">/chat/completions</code>）</li>
            <li>Anthropic SDK base_url：<code class="mono chip">{{ origin }}</code>（SDK 自动拼 <code class="mono">/v1/messages</code>）</li>
            <li>鉴权：请求头 <code class="mono">Authorization: Bearer &lt;key&gt;</code>；Anthropic 协议也可用 <code class="mono">x-api-key: &lt;key&gt;</code></li>
            <li>模型名：格式为 <code class="mono">upstream名称/模型名</code>，或「模型别名」页配置的固定别名；<code class="mono">GET /v1/models</code> 返回当前密钥实际可用的模型清单</li>
          </ul>
          <p class="doc-note">
            对外端点共四个：<code class="mono">POST /v1/chat/completions</code>（OpenAI 对话）、<code class="mono">POST /v1/messages</code>（Anthropic 对话）、<code class="mono">POST /v1/responses</code>（OpenAI Responses / Codex）、<code class="mono">GET /v1/models</code>。客户端协议任选其一，网关自动完成格式转换，模型名与鉴权方式不变。
          </p>
        </section>

        <section class="doc-section">
          <h3>curl 快速验证</h3>
          <div class="code-block">
            <div class="code-head">
              <span>OpenAI 对话（流式）</span>
              <n-button size="tiny" quaternary @click="copyText(snips.curlOpenAI, $event)">
                <template #icon><AppIcon name="copy" :size="12" /></template>
                复制
              </n-button>
            </div>
            <pre class="code-body"><code>{{ snips.curlOpenAI }}</code></pre>
          </div>
          <div class="code-block">
            <div class="code-head">
              <span>Anthropic 对话</span>
              <n-button size="tiny" quaternary @click="copyText(snips.curlAnthropic, $event)">
                <template #icon><AppIcon name="copy" :size="12" /></template>
                复制
              </n-button>
            </div>
            <pre class="code-body"><code>{{ snips.curlAnthropic }}</code></pre>
          </div>
          <div class="code-block">
            <div class="code-head">
              <span>列出当前密钥可用模型</span>
              <n-button size="tiny" quaternary @click="copyText(snips.curlModels, $event)">
                <template #icon><AppIcon name="copy" :size="12" /></template>
                复制
              </n-button>
            </div>
            <pre class="code-body"><code>{{ snips.curlModels }}</code></pre>
          </div>
        </section>

        <section class="doc-section">
          <h3>OpenAI SDK（Python）</h3>
          <div class="code-block">
            <div class="code-head">
              <span>chat.completions</span>
              <n-button size="tiny" quaternary @click="copyText(snips.openaiSdk, $event)">
                <template #icon><AppIcon name="copy" :size="12" /></template>
                复制
              </n-button>
            </div>
            <pre class="code-body"><code>{{ snips.openaiSdk }}</code></pre>
          </div>
        </section>

        <section class="doc-section">
          <h3>Anthropic SDK（Python）</h3>
          <div class="code-block">
            <div class="code-head">
              <span>messages.create</span>
              <n-button size="tiny" quaternary @click="copyText(snips.anthropicSdk, $event)">
                <template #icon><AppIcon name="copy" :size="12" /></template>
                复制
              </n-button>
            </div>
            <pre class="code-body"><code>{{ snips.anthropicSdk }}</code></pre>
          </div>
        </section>

        <section class="doc-section">
          <h3>Claude Code</h3>
          <p class="doc-note">设置以下环境变量后正常启动即可，模型用 <code class="mono">upstream名称/模型名</code> 或别名：</p>
          <div class="code-block">
            <div class="code-head">
              <span>环境变量</span>
              <n-button size="tiny" quaternary @click="copyText(snips.claudeCode, $event)">
                <template #icon><AppIcon name="copy" :size="12" /></template>
                复制
              </n-button>
            </div>
            <pre class="code-body"><code>{{ snips.claudeCode }}</code></pre>
          </div>
        </section>

        <section class="doc-section">
          <h3>opencode / Oh My Pi / dsh</h3>
          <p class="doc-note">
            密钥列表每行的 <code class="mono">opencode</code> / <code class="mono">OMP</code> / <code class="mono">dsh</code> 按钮会生成并复制含该密钥的完整配置（受限密钥只导出白名单内的模型），粘贴进对应客户端的配置文件即可；新建密钥后的弹窗里也提供同样的按钮。dsh 的文本包含 settings.yaml 与 .credentials.yaml 两段，按段头注释分别粘贴。
          </p>
        </section>

        <section class="doc-section">
          <h3>常见返回码</h3>
          <ul>
            <li><code class="mono">401</code>：密钥缺失、错误或已被禁用</li>
            <li><code class="mono">403</code>：请求的模型不在该密钥的「可用模型」白名单内</li>
            <li><code class="mono">404</code>：模型名拼写错误，或对应上游已禁用 / 已过期</li>
            <li><code class="mono">429</code>：超出密钥或上游的日 / 月 token 限额，或上游并发已满</li>
          </ul>
          <p class="doc-note">流式请求：OpenAI 协议加 <code class="mono">"stream": true</code>，Anthropic SDK 使用 <code class="mono">client.messages.stream(...)</code>。日 / 月限额按服务器本地时间的自然日 / 自然月累计。</p>
        </section>
      </div>
    </n-drawer-content>
  </n-drawer>
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
  margin: 10px 0 0;
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
  overflow-x: auto;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--text-2);
}
</style>
