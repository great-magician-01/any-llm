/**
 * AgentConfigDrawer（Keys 页「Agent 配置」文档抽屉）的组件测试。
 *
 * 断言只落在两件事上：
 *   1. 用户看到什么：五个已接入 agent 的文档节（配置文件位置 + 粘贴步骤）、
 *      受限密钥的模型裁剪标注、无密钥时的引导；
 *   2. 交互行为：复制按钮把「含选中密钥的合法配置」写进剪贴板；
 *      Claude Code 完整导出弹窗能从抽屉里打开。
 *
 * 模型清单走真实 axios + 假后端 adapter（与 Upstreams.test.ts 同一套路）。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { mount } from '@vue/test-utils'
import naive, { NConfigProvider, dateZhCN, zhCN } from 'naive-ui'
import AgentConfigDrawer from './AgentConfigDrawer.vue'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { bodyHas, clickButton, flush } from '@/test/ui'
import { resetMessages } from '@/test/naiveMessage'
import type { ExtKey } from '@/api/keys'
import type { Upstream, UpstreamModel } from '@/api/upstreams'

// useMessage 换成本地记录器，其余 naive-ui 导出保持原样，组件照常真实渲染
vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

const TEST_KEY = 'all-sk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'

function upstream(over: Partial<Upstream> = {}): Upstream {
  return {
    id: 1,
    name: 'deepseek',
    base_url: 'https://api.deepseek.com',
    api_key: 'sk-***masked***',
    format: 'openai',
    enabled: true,
    daily_token_limit: 0,
    monthly_token_limit: 0,
    max_concurrent: 100,
    expires_at: null,
    model_count: 2,
    ...over,
  }
}

function model(name: string, over: Partial<UpstreamModel> = {}): UpstreamModel {
  return {
    id: 1,
    upstream_id: 1,
    model_name: name,
    manual: true,
    context_length: 65536,
    max_output_length: 8192,
    multimodal: false,
    ...over,
  }
}

function extKey(over: Partial<ExtKey> = {}): ExtKey {
  return {
    id: 1,
    key: TEST_KEY,
    label: '默认密钥',
    remark: '',
    enabled: true,
    daily_token_limit: 0,
    monthly_token_limit: 0,
    ...over,
  }
}

let api: ApiMock
let wrapper: ReturnType<typeof mount> | null = null
const writeText = vi.fn(async (_text: string) => {})

beforeEach(() => {
  resetMessages()
  writeText.mockClear()
  api = createApiMock().install()
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  api.restore()
})

/** collectExportModels 需要的三个清单路由（上游 + 别名 + 各上游模型） */
function baseRoutes(models: UpstreamModel[] = [model('deepseek-chat'), model('deepseek-reasoner')]) {
  api.on('get', '/upstreams', { data: { data: [upstream()] } })
  api.on('get', '/aliases', { data: { data: [] } })
  api.on('get', '/upstreams/1/models', { data: { data: models } })
}

function mountDrawer(keys: ExtKey[]) {
  const Host = defineComponent({
    setup() {
      const show = ref(true)
      return () =>
        h(NConfigProvider, { locale: zhCN, dateLocale: dateZhCN }, {
          default: () =>
            h(AgentConfigDrawer, {
              show: show.value,
              keys,
              'onUpdate:show': (v: boolean) => {
                show.value = v
              },
            }),
        })
    },
  })
  wrapper = mount(Host, { global: { plugins: [naive] }, attachTo: document.body })
  return wrapper
}

describe('AgentConfigDrawer', () => {
  it('渲染五个已接入 agent 的文档：配置文件位置与粘贴步骤齐全', async () => {
    baseRoutes()
    mountDrawer([extKey()])
    await flush()
    expect(bodyHas('opencode'))
    expect(bodyHas('~/.config/opencode/opencode.json'))
    expect(bodyHas('Oh My Pi（OMP）'))
    expect(bodyHas('~/.omp/agent/models.yml'))
    expect(bodyHas('Pi agent'))
    expect(bodyHas('~/.pi/agent/models.json'))
    expect(bodyHas('~/.dsh/settings.yaml'))
    expect(bodyHas('~/.dsh/.credentials.yaml'))
    expect(bodyHas('Claude Code'))
    // 预览按选中密钥生成：密钥明文出现在配置预览里
    expect(bodyHas(TEST_KEY))
    // OpenAI 兼容客户端的 base_url 带 /v1 后缀
    expect(bodyHas(`${window.location.origin}/v1`))
  })

  it('受限密钥只生成白名单内的模型，并标出限制数量', async () => {
    baseRoutes()
    mountDrawer([extKey({ allowed_models: ['deepseek/deepseek-chat'] })])
    await flush()
    expect(bodyHas('deepseek/deepseek-chat'))
    expect(bodyHas('deepseek/deepseek-reasoner')).toBe(false)
    expect(bodyHas('已限制 1 个模型'))
  })

  it('复制 opencode 配置：剪贴板拿到含选中密钥的合法 JSON', async () => {
    baseRoutes()
    mountDrawer([extKey()])
    await flush()
    await clickButton('复制 opencode 配置')
    expect(writeText).toHaveBeenCalledTimes(1)
    const cfg = JSON.parse(writeText.mock.lastCall![0])
    expect(cfg.provider['any-llm'].options.apiKey).toBe(TEST_KEY)
    expect(cfg.provider['any-llm'].options.baseURL).toBe(`${window.location.origin}/v1`)
    expect(Object.keys(cfg.provider['any-llm'].models).sort()).toEqual([
      'deepseek/deepseek-chat',
      'deepseek/deepseek-reasoner',
    ])
  })

  it('复制 dsh 配置：两段文本齐全，密钥只落在 .credentials.yaml 凭据行', async () => {
    baseRoutes()
    mountDrawer([extKey()])
    await flush()
    await clickButton('复制 dsh 配置')
    const text = writeText.mock.lastCall![0]
    expect(text).toContain('# ----- ~/.dsh/settings.yaml -----')
    expect(text).toContain('# ----- ~/.dsh/.credentials.yaml -----')
    expect(text).toContain(`ANY_LLM_API_KEY: ${TEST_KEY}`)
  })

  it('没有密钥时给出先创建密钥的引导，不渲染任何 agent 配置节', async () => {
    baseRoutes()
    mountDrawer([])
    await flush()
    expect(bodyHas('还没有密钥'))
    expect(bodyHas('复制 opencode 配置')).toBe(false)
    expect(bodyHas('复制 dsh 配置')).toBe(false)
  })

  it('从抽屉打开 Claude Code 完整导出弹窗（模型槽位映射）', async () => {
    baseRoutes()
    mountDrawer([extKey()])
    await flush()
    await clickButton('打开完整导出（模型槽位映射）')
    await flush()
    expect(bodyHas('Claude Code 配置（Linux）'))
    expect(bodyHas('主会话模型'))
    expect(bodyHas('复制配置'))
  })
})
