/**
 * 对话记录页双主题测试：「按请求 / 按会话」两个 tab 的请求序列与渲染要素
 * 在经典与毛玻璃两套视图里必须一致。沿用 themeParity 的思路：断言落在真实
 * 发出的 HTTP 请求与用户可见文案上，允许样式差异。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ClassicConversations from './classic/views/Conversations.vue'
import GlassConversations from './glass/views/GlassConversations.vue'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { flush, mountPage, type AnyWrapper } from '@/test/ui'
import { messagesOf, resetMessages } from '@/test/naiveMessage'

vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

const CONV = {
  id: 1, ext_key_id: 1, upstream_id: 1, upstream_name: 'u', model: 'm',
  in_format: 'openai', up_format: 'openai', harness: 'claude-code',
  user_agent: 'ua', stream: true, status: 'ok',
  prompt_tokens: 10, completion_tokens: 5, total_tokens: 15,
  cache_read_tokens: 0, cache_creation_tokens: 0, reasoning_tokens: 0,
  created_at: '2026-10-07T10:00:00+08:00',
}

const SESSION = {
  id: 7, session_id: 'cc:abc-123', ext_key_id: 1, harness: 'claude-code',
  in_format: 'anthropic', model: 'm', turn_count: 3,
  prompt_tokens: 30, completion_tokens: 15, total_tokens: 45,
  cache_read_tokens: 0, cache_creation_tokens: 0, reasoning_tokens: 0,
  status: 'ok', msg_count: 6,
  created_at: '2026-10-07T10:00:00+08:00', last_active_at: '2026-10-07T11:00:00+08:00',
}

function clickTab(name: string) {
  const tab = Array.from(document.querySelectorAll('.n-tabs-tab'))
    .find((el) => (el.textContent ?? '').includes(name)) as HTMLElement | undefined
  expect(tab, `tab ${name} should exist`).toBeTruthy()
  tab!.click()
}

describe('对话记录页双主题', () => {
  let api: ApiMock
  let wrapper: AnyWrapper | undefined

  beforeEach(() => {
    resetMessages()
    api = createApiMock()
      .on('GET', '/conversations', { data: { data: [CONV], total: 1 } })
      .on('GET', '/conv-sessions', { data: { data: [SESSION], total: 1 } })
      .on('GET', '/conv-sessions/7', { data: {
        data: { ...SESSION, messages: '[{"Role":"user","Content":[{"Type":"text","Text":"hi"}]},{"Role":"assistant","Content":[{"Type":"text","Text":"hello"}]}]' },
      } })
      .install()
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    api.restore()
  })

  it.each([
    ['classic', ClassicConversations],
    ['glass', GlassConversations],
  ])('%s：默认按请求加载，切到按会话才发 conv-sessions 请求', async (_name, view) => {
    wrapper = mountPage(view as any)
    await flush()

    // 首屏只拉按请求列表，不拉会话
    expect(api.callsTo('GET', '/conversations')).toHaveLength(1)
    expect(api.callsTo('GET', '/conv-sessions')).toHaveLength(0)
    expect(document.body.textContent).toContain('按请求')
    expect(document.body.textContent).toContain('按会话')

    // 切 tab → 懒加载会话列表并渲染
    clickTab('按会话')
    await flush()
    expect(api.callsTo('GET', '/conv-sessions')).toHaveLength(1)
    expect(document.body.textContent).toContain('cc:abc-123')

    // 打开会话详情 → 拉 detail 并渲染消息
    const viewBtn = Array.from(document.querySelectorAll('button'))
      .find((el) => (el.textContent ?? '').trim() === '查看') as HTMLElement | undefined
    expect(viewBtn).toBeTruthy()
    viewBtn!.click()
    await flush()
    expect(api.callsTo('GET', '/conv-sessions/7')).toHaveLength(1)
    expect(document.body.textContent).toContain('hi')
    expect(document.body.textContent).toContain('hello')
    expect(messagesOf("error")).toHaveLength(0)
  })

  it.each([
    ['classic', ClassicConversations],
    ['glass', GlassConversations],
  ])('%s：SQLite 归档关闭时两个 tab 都显示提示', async (_name, view) => {
    api.on('GET', '/conversations', { data: { data: [], total: 0, disabled: true } })
    wrapper = mountPage(view as any)
    await flush()
    expect(document.body.textContent).toContain('PostgreSQL')
    expect(messagesOf("error")).toHaveLength(0)
  })
})
