/**
 * 「API 密钥」页的组件测试。此前只有双主题 parity 源断言（keysExportParity），
 * 视图合并后 parity 失去对象，这里补上基础运行时测试：
 *   - 打开页面拉取密钥列表与各密钥用量，并把 label/密钥指纹渲染进表格；
 *   - 删除走确认框，确认后发 DELETE 并刷新列表；
 *   - 源码级防误删：客户端配置导出器（pi 等）与 Agent 配置抽屉必须保持接入。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Keys from './Keys.vue'
// ?raw 取源码做防误删断言（vite/client 已声明 *?raw 模块）
import SRC from './Keys.vue?raw'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { bodyHas, clickButton, flush, mountPage } from '@/test/ui'
import { messagesOf, resetMessages } from '@/test/naiveMessage'

// useMessage 换成本地记录器（与 Upstreams 页测试同款）
vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

const KEY = {
  id: 1,
  key: 'all-sk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  label: '生产环境',
  remark: '',
  enabled: true,
  daily_token_limit: 0,
  monthly_token_limit: 0,
}

let api: ApiMock
let wrapper: ReturnType<typeof mountPage> | null = null

beforeEach(() => {
  resetMessages()
  api = createApiMock().install()
  api.on('get', '/keys', { data: { data: [{ ...KEY }] } })
  api.on('get', '/usage/key/1', { data: { requests: 12, tokens: 3400 } })
  api.on('delete', '/keys/1', { data: {} })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  api.restore()
})

async function open() {
  wrapper = mountPage(Keys)
  await flush()
}

describe('Keys 页', () => {
  it('打开页面拉取密钥与用量并渲染 label', async () => {
    await open()

    expect(api.callsTo('get', '/keys')).toHaveLength(1)
    expect(api.callsTo('get', '/usage/key/1')).toHaveLength(1)
    expect(bodyHas('生产环境')).toBe(true)
    expect(messagesOf('error')).toHaveLength(0)
  })

  it('删除：确认框出现后确认，DELETE 该 id 并刷新列表', async () => {
    await open()

    await clickButton('删除')
    expect(bodyHas('确定删除此 key？')).toBe(true)
    await clickButton('确认')

    expect(api.callsTo('delete', '/keys/1')).toHaveLength(1)
    expect(api.callsTo('get', '/keys').length).toBeGreaterThanOrEqual(2)
    expect(messagesOf('error')).toHaveLength(0)
  })

  // 以下为原 keysExportParity 的单源版：新增/删除一个客户端导出入口时
  // （如 pi）这几条会露馅，防止重构时整段丢掉
  it('接入 pi 导出器（生成器 + 复制入口）', () => {
    expect(SRC, '必须引入 pi 生成器').toContain("import { buildPiJson } from '@/utils/piConfig'")
    expect(SRC, '必须调用 buildPiJson').toContain('buildPiJson({')
    expect(SRC, '必须在行内按钮与新建弹窗都提供 pi 入口').toContain('copyPiConfig(newlyCreatedKey')
  })

  it('接入 Agent 配置文档抽屉（文档 + 按选中密钥生成）', () => {
    expect(SRC, '必须引入 AgentConfigDrawer').toContain("import AgentConfigDrawer from '@/components/AgentConfigDrawer.vue'")
    expect(SRC, '必须渲染 AgentConfigDrawer 并传入密钥列表').toContain('<AgentConfigDrawer v-model:show="showAgent" :keys="keys" />')
    expect(SRC, '页头必须提供 Agent 配置入口').toContain('Agent 配置')
  })

  it('表单与剪贴板走共享实现', () => {
    expect(SRC, '必须使用共享的 useKeyForms').toContain("from '@/composables/useKeyForms'")
    expect(SRC, '必须使用共享的 useClipboard').toContain("from '@/composables/useClipboard'")
  })
})
