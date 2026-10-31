/**
 * 用量统计-请求明细失败详情：
 *   - 失败标签的 tooltip 展示 HTTP 状态码（仅 >0）和原始报文；
 *   - 报文保留换行，长内容限制在浮层内滚动/换行；
 *   - 成功标签没有失败详情，旧记录/空详情给出明确占位。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { NDataTable, NTooltip } from 'naive-ui'
import Usage from './Usage.vue'
import type { UsageRecord } from '@/api/usage'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { flush, mountPage } from '@/test/ui'

vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

const ERROR_BODY = '{"error":{"message":"upstream overloaded"}}\nrequest id: req-123'
const FAILED_RECORD: UsageRecord = {
  id: 1,
  upstream_name: 'deepseek',
  model: 'deepseek-chat',
  in_format: 'openai',
  up_format: 'openai',
  prompt_tokens: 10,
  completion_tokens: 0,
  total_tokens: 10,
  cache_read_tokens: 0,
  cache_creation_tokens: 0,
  reasoning_tokens: 0,
  duration_ms: 25,
  stream: false,
  status: 'error',
  created_at: '2026-10-31T12:00:00+08:00',
  error_code: 502,
  error_body: ERROR_BODY,
}
const OK_RECORD: UsageRecord = {
  ...FAILED_RECORD,
  id: 2,
  status: 'ok',
  completion_tokens: 5,
  total_tokens: 15,
  error_code: 0,
  error_body: '',
}

let api: ApiMock
let wrapper: ReturnType<typeof mountPage> | null = null

beforeEach(() => {
  api = createApiMock().install()
  api.on('get', '/usage/summary', { data: { data: [] } })
  api.on('get', '/usage/daily', { data: { data: [] } })
  api.on('get', '/usage/models', { data: { data: [] } })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  api.restore()
})

async function open(records: unknown[] = [FAILED_RECORD, OK_RECORD]) {
  api.on('get', '/usage/records', { data: { data: records, total: records.length } })
  wrapper = mountPage(Usage)
  await flush()
}

/** 请求明细表格每行的最后一个单元格就是状态列。 */
function statusCells() {
  const table = wrapper!.findAllComponents(NDataTable).find((t) => t.props('pagination'))
  expect(table, '请求明细的远程分页表格必须存在').toBeTruthy()
  return table!.findAll('tbody tr').map((row) => {
    const cells = row.findAll('td')
    return cells[cells.length - 1]
  })
}

describe('用量统计-请求明细：失败详情', () => {
  it('失败标签 tooltip 含状态码和原始报文，成功标签不渲染失败详情', async () => {
    await open()
    const [failedStatus, okStatus] = statusCells()

    const tooltip = failedStatus.findComponent(NTooltip)
    expect(tooltip.exists(), '失败标签必须包 NTooltip').toBe(true)
    expect(okStatus.findComponent(NTooltip).exists(), '成功标签不能有失败详情 tooltip').toBe(false)
    expect(okStatus.find('.usage-error-details').exists()).toBe(false)

    // NTooltip 内容默认悬浮后才挂载；直接渲染其默认插槽，断言用户悬浮时看到的内容。
    const details = mount(defineComponent({
      render: () => tooltip.vm.$slots.default?.(),
    }))
    expect(details.text()).toContain('状态码：502')
    expect(details.find('.usage-error-body').text()).toBe(ERROR_BODY)
    expect(details.find('.usage-error-body').attributes('style')).toContain('white-space: pre-wrap')
    expect(details.find('.usage-error-body').attributes('style')).toContain('overflow-wrap: anywhere')
    expect(details.find('.usage-error-details').attributes('style')).toContain('overflow-y: auto')
    expect(details.find('.usage-error-details').attributes('style')).toContain('max-height:')
    details.unmount()
  })

  it('无 HTTP 状态码时只显示诊断报文，旧记录或空详情显示占位', async () => {
    const networkError = { ...FAILED_RECORD, error_code: 0, error_body: 'network unreachable' }
    const legacyError = { ...FAILED_RECORD, id: 3 }
    delete (legacyError as Partial<UsageRecord>).error_code
    delete (legacyError as Partial<UsageRecord>).error_body
    await open([networkError, legacyError])
    const [networkStatus, legacyStatus] = statusCells()

    const networkDetails = mount(defineComponent({
      render: () => networkStatus.findComponent(NTooltip).vm.$slots.default?.(),
    }))
    expect(networkDetails.text()).toContain('network unreachable')
    expect(networkDetails.text()).not.toContain('状态码')
    networkDetails.unmount()

    const legacyDetails = mount(defineComponent({
      render: () => legacyStatus.findComponent(NTooltip).vm.$slots.default?.(),
    }))
    expect(legacyDetails.text()).toBe('暂无失败详情')
    expect(legacyDetails.text()).not.toContain('状态码')
    legacyDetails.unmount()
  })
})
