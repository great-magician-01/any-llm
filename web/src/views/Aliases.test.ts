/**
 * 「模型别名」页的运行时测试：多套配置的展示（只显示当前生效配置的绑定链）
 * 与列表页一键切换（POST /aliases/:id/activate，不重写绑定）。
 *
 * 切换控件是 naive-ui 的单选组（NRadioButton），其 DOM 是 <label> 而不是
 * <button>，因此这里按可见文案找 .n-radio-button 后点击。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Aliases from './Aliases.vue'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { bodyHas, clickButton, flush, mountPage } from '@/test/ui'
import { messagesOf, resetMessages } from '@/test/naiveMessage'

vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

const ALIAS = {
  id: 1,
  name: 'deepseek',
  active_profile: '峰期',
  bindings: [
    { id: 1, alias_id: 1, upstream_id: 1, upstream_name: 'up-a', upstream_enabled: true, model_name: 'peak-model', priority: 0, profile: '峰期' },
    { id: 2, alias_id: 1, upstream_id: 2, upstream_name: 'up-b', upstream_enabled: true, model_name: 'valley-model', priority: 1, profile: '谷期' },
  ],
  created_at: '2026-01-01T00:00:00+08:00',
}

const upstream = (id: number, name: string) => ({
  id,
  name,
  base_url: 'https://example.com',
  api_key: 'sk',
  format: 'openai',
  enabled: true,
  daily_token_limit: 0,
  monthly_token_limit: 0,
  max_concurrent: 100,
  expires_at: null,
})

let api: ApiMock
let wrapper: ReturnType<typeof mountPage> | null = null

beforeEach(() => {
  resetMessages()
  api = createApiMock().install()
  api.on('get', '/aliases', { data: { data: [structuredClone(ALIAS)] } })
  api.on('get', '/upstreams', { data: { data: [upstream(1, 'up-a'), upstream(2, 'up-b')] } })
  api.on('post', '/aliases/1/activate', { data: { ...structuredClone(ALIAS), active_profile: '谷期' } })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  api.restore()
})

async function open() {
  wrapper = mountPage(Aliases)
  await flush()
}

function clickRadio(label: string) {
  const el = Array.from(document.querySelectorAll<HTMLElement>('.n-radio-button')).find(
    (e) => (e.textContent ?? '').trim() === label,
  )
  if (!el) {
    throw new Error(`未找到配置切换项「${label}」`)
  }
  el.click()
}

describe('Aliases 页', () => {
  it('只展示当前生效配置的绑定链，并列出全部可切换配置', async () => {
    await open()

    expect(bodyHas('deepseek')).toBe(true)
    expect(bodyHas('当前：峰期')).toBe(true)
    expect(bodyHas('up-a / peak-model')).toBe(true)
    // 谷期配置的绑定此刻不生效，不应出现在绑定链里（但仍作为切换项存在）
    expect(bodyHas('up-b / valley-model')).toBe(false)
    expect(document.body.textContent).toContain('谷期')
    expect(messagesOf('error')).toHaveLength(0)
  })

  it('点击另一套配置 → 调 activate 接口并立即展示新配置', async () => {
    await open()

    clickRadio('谷期')
    await flush()

    const calls = api.callsTo('post', '/aliases/1/activate')
    expect(calls).toHaveLength(1)
    expect(calls[0].data).toEqual({ profile: '谷期' })
    expect(bodyHas('当前：谷期')).toBe(true)
    expect(bodyHas('up-b / valley-model')).toBe(true)
    expect(bodyHas('up-a / peak-model')).toBe(false)
    expect(messagesOf('error')).toHaveLength(0)
  })

  it('编辑弹窗按配置分组，保存时带回 active_profile 与每条绑定的 profile', async () => {
    api.on('get', '/upstreams/1/models', { data: { data: [] } })
    api.on('get', '/upstreams/2/models', { data: { data: [] } })
    api.on('put', '/aliases/1', { data: structuredClone(ALIAS) })
    await open()

    await clickButton('编辑')
    expect(bodyHas('当前生效配置')).toBe(true)
    expect(bodyHas('删除此配置')).toBe(true)

    await clickButton('保存')
    await flush()

    const calls = api.callsTo('put', '/aliases/1')
    expect(calls).toHaveLength(1)
    expect(calls[0].data).toEqual({
      name: 'deepseek',
      active_profile: '峰期',
      bindings: [
        { upstream_id: 1, model_name: 'peak-model', profile: '峰期' },
        { upstream_id: 2, model_name: 'valley-model', profile: '谷期' },
      ],
    })
    expect(messagesOf('error')).toHaveLength(0)
  })
})
