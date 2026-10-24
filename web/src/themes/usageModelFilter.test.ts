/**
 * 用量统计-请求明细「按模型筛选」：
 *   - 下拉框选项来自 /usage/models（usage_records 里实际出现过的模型）；
 *   - 选中模型后明细请求带 model 参数，且回到第 1 页（筛选后结果集变小，
 *     旧页码可能越界）；
 *   - 清空后回到全量（不带 model 参数）。
 *
 * 断言落在真实发出的请求参数上（走真实 axios，假后端只换 adapter），并且两套
 * 主题各跑一遍——经典的 /usage 与毛玻璃的 /glass/usage 是逐行重复的实现，
 * 「只改了一边」要在这里变成红灯。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { NDataTable, NSelect } from 'naive-ui'
import type { Component } from 'vue'
import ClassicUsage from './classic/views/Usage.vue'
import GlassUsage from './glass/views/GlassUsage.vue'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { flush, mountPage } from '@/test/ui'

// useMessage 换成本地记录器（与 Upstreams 页测试同款）：Usage 页挂载时需要
// message 上下文。
vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

let api: ApiMock
let wrapper: ReturnType<typeof mountPage> | null = null

beforeEach(() => {
  api = createApiMock().install()
  api.on('get', '/usage/summary', { data: { data: [] } })
  api.on('get', '/usage/daily', { data: { data: [] } })
  api.on('get', '/usage/records', { data: { data: [], total: 0 } })
  api.on('get', '/usage/models', { data: { data: ['deepseek-chat', 'gpt-4o'] } })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  api.restore()
})

async function open(component: Component) {
  wrapper = mountPage(component)
  await flush()
}

/** 最后一次明细请求的参数 */
function recordsParams(): Record<string, any> {
  const calls = api.callsTo('get', '/usage/records')
  expect(calls.length).toBeGreaterThan(0)
  return calls[calls.length - 1].params
}

/** 请求明细卡片头部的模型筛选下拉框 */
function modelSelect() {
  const sel = wrapper!.findComponent(NSelect)
  expect(sel.exists(), '请求明细卡片头部必须有模型筛选下拉框').toBe(true)
  return sel
}

/** 请求明细的远程分页表格（页面里还有汇总表，它不带 pagination） */
function recordsTable() {
  const table = wrapper!.findAllComponents(NDataTable).find((t) => t.props('pagination'))
  expect(table, '请求明细的远程分页表格必须存在').toBeTruthy()
  return table!
}

const VIEWS: Array<[string, Component]> = [
  ['经典', ClassicUsage],
  ['毛玻璃', GlassUsage],
]

describe('用量统计-请求明细：按模型筛选', () => {
  it.each(VIEWS)('%s视图从 /usage/models 拉取筛选项', async (_name, component) => {
    await open(component)
    expect(api.callsTo('get', '/usage/models').length).toBe(1)
  })

  it.each(VIEWS)('%s视图选中模型后明细请求带 model 并回到第 1 页', async (_name, component) => {
    await open(component)

    // 先翻到第 2 页：随后选择模型必须重置页码，否则旧页码在筛选后的结果集里越界
    const pagination = recordsTable().props('pagination') as { onChange: (p: number) => void }
    expect(pagination.onChange, '明细表必须是远程分页').toBeTruthy()
    pagination.onChange(2)
    await flush()
    expect(recordsParams().page).toBe(2)

    modelSelect().vm.$emit('update:value', 'gpt-4o')
    await flush()
    const params = recordsParams()
    expect(params.model).toBe('gpt-4o')
    expect(params.page).toBe(1)
  })

  it.each(VIEWS)('%s视图清空筛选后回到全量明细', async (_name, component) => {
    await open(component)

    modelSelect().vm.$emit('update:value', 'deepseek-chat')
    await flush()
    expect(recordsParams().model).toBe('deepseek-chat')

    // 清空 = 全量口径（服务端不收 model 参数）
    modelSelect().vm.$emit('update:value', null)
    await flush()
    expect(recordsParams().model).toBeUndefined()
  })

  it('两套主题的筛选请求完全一致', async () => {
    await open(ClassicUsage)
    modelSelect().vm.$emit('update:value', 'deepseek-chat')
    await flush()
    const classic = recordsParams()
    wrapper?.unmount()

    await open(GlassUsage)
    modelSelect().vm.$emit('update:value', 'deepseek-chat')
    await flush()
    const glass = recordsParams()

    expect(glass).toEqual(classic)
  })
})
