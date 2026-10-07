/**
 * 经典皮肤「概览」页的组件测试。
 *
 * 守的规则：
 *   - 「复制 Base URL」必须走共享的 useClipboard（HTTP + 非 localhost 的局域网部署下
 *     navigator.clipboard 不存在，只能退回 execCommand 才能真正复制成功——直接调
 *     navigator.clipboard.writeText 会永远弹「复制失败」）；
 *   - 复制成功/失败的提示都要带上 Base URL 原文：失败时用户得知道该手动复制什么；
 *   - 两套 Dashboard 必须共用同一份实现（源码级 parity，防止只改一边）。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Dashboard from './Dashboard.vue'
// ?raw 取源码做双主题 parity 断言（vite/client 已声明 *?raw 模块）
import CLASSIC_SRC from './Dashboard.vue?raw'
import GLASS_SRC from '../../glass/views/GlassDashboard.vue?raw'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { clickButton, flush, mountPage } from '@/test/ui'
import { messagesOf, resetMessages } from '@/test/naiveMessage'

vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
const execDescriptor = Object.getOwnPropertyDescriptor(document, 'execCommand')

/** 概览首屏的请求：三份 usage/summary + daily + 上游 + 密钥 + 余额快照 */
function baseRoutes(api: ApiMock) {
  api.on('get', '/usage/summary', { data: { data: [] } })
  api.on('get', '/usage/daily', { data: { data: [] } })
  api.on('get', '/upstreams', { data: { data: [] } })
  api.on('get', '/keys', { data: { data: [] } })
  api.on('get', '/balances', { data: { data: [] } })
}

let api: ApiMock
let wrapper: ReturnType<typeof mountPage> | null = null

beforeEach(() => {
  resetMessages()
  api = createApiMock().install()
  // 模拟「HTTP + 非 localhost」的部署（内网/局域网访问就是这样）：剪贴板 API 不可用
  Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true, writable: true })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  api.restore()
  if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor)
  else delete (navigator as any).clipboard
  if (execDescriptor) Object.defineProperty(document, 'execCommand', execDescriptor)
  else delete (document as any).execCommand
})

/**
 * 让 execCommand('copy') 照浏览器行为同步触发一次 copy 事件，
 * 返回 setData 供断言「剪贴板里真的写进去了」。
 */
function stubExecCommand(fireCopyEvent: boolean) {
  const setData = vi.fn()
  Object.defineProperty(document, 'execCommand', {
    value: () => {
      if (fireCopyEvent) {
        const event = new Event('copy') as Event & { clipboardData: unknown }
        event.clipboardData = { setData }
        document.dispatchEvent(event)
      }
      return true
    },
    configurable: true,
    writable: true,
  })
  return setData
}

async function open() {
  baseRoutes(api)
  wrapper = mountPage(Dashboard)
  await flush()
}

describe('Dashboard 页（经典皮肤）', () => {
  // 规则：剪贴板 API 不可用（局域网 HTTP）时点「复制 Base URL」必须退回 execCommand
  // 复制成功，不能直接弹复制失败
  it('剪贴板 API 不可用时仍能复制 Base URL（退回 execCommand）', async () => {
    const setData = stubExecCommand(true)
    await open()

    await clickButton('复制 Base URL')

    const expected = window.location.origin + '/v1'
    expect(setData).toHaveBeenCalledWith('text/plain', expected)
    expect(messagesOf('warning')).toHaveLength(0)
    expect(messagesOf('success')).toContain('已复制：' + expected)
    // 回退用的临时 textarea 用完必须摘掉
    expect(document.querySelector('textarea')).toBeNull()
  })

  // 规则：回退也失败时提示手动复制，并且必须带上 Base URL 原文
  it('回退也失败时提示手动复制并带上 Base URL', async () => {
    stubExecCommand(false)
    await open()

    await clickButton('复制 Base URL')

    const expected = window.location.origin + '/v1'
    expect(messagesOf('error')).toContain('复制失败，请手动复制：' + expected)
    expect(document.querySelector('textarea')).toBeNull()
  })

  // 规则：概览的「上游用量」和「余额/额度」卡片只展示启用中的上游——
  // 已禁用代表下线观察，不应再占概览版面（过期的仍显示，续期即恢复服务）
  it('上游用量与余额/额度卡片隐藏已禁用的上游', async () => {
    api.on('get', '/usage/summary', { data: { data: [] } })
    api.on('get', '/usage/daily', { data: { data: [] } })
    api.on('get', '/keys', { data: { data: [] } })
    api.on('get', '/upstreams', {
      data: {
        data: [
          { id: 1, name: 'up-enabled', enabled: true, daily_token_limit: 0, monthly_token_limit: 0 },
          { id: 2, name: 'up-disabled', enabled: false, daily_token_limit: 0, monthly_token_limit: 0 },
        ],
      },
    })
    api.on('get', '/usage/upstream/1', { data: { daily_tokens: 100, monthly_tokens: 200 } })
    api.on('get', '/usage/upstream/2', { data: { daily_tokens: 5, monthly_tokens: 6 } })
    const snapshot = (id: number, upstreamId: number, name: string) => ({
      id,
      upstream_id: upstreamId,
      upstream_name: name,
      vendor: 'deepseek',
      payload: {
        kind: 'balance',
        is_available: true,
        balances: [{ currency: 'CNY', total: '169.29', granted: '0', topped_up: '169.29' }],
      },
      created_at: new Date().toISOString(),
    })
    api.on('get', '/balances', { data: { data: [snapshot(11, 1, 'up-enabled'), snapshot(12, 2, 'up-disabled')] } })

    wrapper = mountPage(Dashboard)
    await flush()

    const text = wrapper.text()
    expect(text).toContain('up-enabled')
    expect(text).not.toContain('up-disabled')
  })

  // 规则：两套 Dashboard 必须共用同一份复制实现；任何一边自己调
  // navigator.clipboard 都会在这里露馅（这次修的就是经典/毛玻璃各写了一份）
  it('两套 Dashboard 都从共享的 useClipboard 复制，不私藏剪贴板实现', () => {
    for (const [name, src] of [['classic', CLASSIC_SRC], ['glass', GLASS_SRC]] as const) {
      expect(src, `${name} 必须引入共享的 useClipboard`).toContain("from '@/composables/useClipboard'")
      expect(src, `${name} 必须调用 useClipboard()`).toContain('useClipboard()')
      expect(src, `${name} 不允许直接调用剪贴板 API 写文本`).not.toContain('navigator.clipboard.writeText')
      expect(src, `${name} 不允许私藏 execCopy 回退实现`).not.toContain('function execCopy')
    }
  })
})
