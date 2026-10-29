/**
 * 「账单」页的组件测试。
 *
 * 守的规则：
 *   - 打开页面同时查汇总（/bills/summary）与明细（/bills），筛选参数按用户选的
 *     范围/上游下发；
 *   - 记一笔：金额/上游/日期校验，POST /bills 的 body 是十进制金额串；
 *   - 删除：确认框出现后确认才发 DELETE；
 *   - 自动账单（kind=auto）没有编辑入口，手动账单有。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Bills from './Bills.vue'
import BarChart from '@/components/BarChart.vue'
import StatCard from '@/components/StatCard.vue'
import { NDataTable, NSelect } from 'naive-ui'
// ?raw 取源码做结构断言（vite/client 已声明 *?raw 模块）
import SRC from './Bills.vue?raw'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { bodyHas, clickButton, fillInput, flush, inputByLabel, mountPage } from '@/test/ui'
import { messagesOf, resetMessages } from '@/test/naiveMessage'

vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

const UPSTREAM = { id: 1, name: 'deepseek', base_url: 'https://api.deepseek.com', format: 'openai', enabled: true }

const AUTO_BILL = {
  id: 11,
  upstream_id: 1,
  upstream_name: 'deepseek',
  kind: 'auto',
  amount_micros: 1_650_000,
  currency: 'CNY',
  billed_at: '2026-10-20T08:30:00+08:00',
  note: '',
  detail: '{"prev_total":"110.00","new_total":"108.35","prev_snapshot_id":1,"new_snapshot_id":2}',
  created_at: '2026-10-20T08:30:00+08:00',
}

const MANUAL_BILL = {
  id: 12,
  upstream_id: 1,
  upstream_name: 'deepseek',
  kind: 'manual',
  amount_micros: 30_000_000,
  currency: 'CNY',
  billed_at: '2026-10-15T00:00:00+08:00',
  note: '订阅费',
  created_at: '2026-10-21T09:00:00+08:00',
}

let api: ApiMock
let wrapper: ReturnType<typeof mountPage> | null = null

beforeEach(() => {
  resetMessages()
  api = createApiMock().install()
  api.on('get', '/upstreams', { data: { data: [UPSTREAM] } })
  api.on('get', '/bills/summary', { data: { buckets: [{ bucket: '2026-10-20', amounts: [{ currency: 'CNY', amount_micros: 1_650_000 }], count: 1 }], upstreams: [{ upstream_id: 1, upstream_name: 'deepseek', currency: 'CNY', amount_micros: 31_650_000, count: 2 }], kinds: [{ kind: 'auto', currency: 'CNY', count: 1, amount_micros: 1_650_000 }, { kind: 'manual', currency: 'CNY', count: 1, amount_micros: 30_000_000 }] } })
  api.on('get', '/bills', { data: { data: [AUTO_BILL, MANUAL_BILL], total: 2 } })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  api.restore()
})

async function open() {
  wrapper = mountPage(Bills)
  await flush()
}

function callsTo(method: string, url: string) {
  return api.callsTo(method, url)
}

describe('账单页', () => {
  it('打开页面同时查汇总与明细，并渲染账单内容', async () => {
    await open()

    const summaryCalls = callsTo('get', '/bills/summary')
    expect(summaryCalls).toHaveLength(1)
    // 默认本月范围 + 按日桶
    expect(summaryCalls[0].params.bucket).toBe('day')
    expect(summaryCalls[0].params.from).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    expect(callsTo('get', '/bills')[0].params.page).toBe(1)

    const text = document.body.textContent ?? ''
    expect(text).toContain('deepseek')
    expect(text).toContain('¥1.65')
    expect(text).toContain('¥30.00')
    // 手动账单可编辑；自动账单没有编辑入口
    expect(bodyHas('订阅费')).toBe(true)
    expect((document.body.textContent ?? '').match(/编辑/g)?.length).toBe(1)
  })

  it('自动账单类型列接了 NTooltip + billDetailText（悬浮展示推导依据）', async () => {
    // NTooltip 内容悬浮后才挂载，happy-dom 里模拟 hover 不可靠；结构保证用
    // 源码断言（billDetailText 的解析逻辑已在 utils/bill.test.ts 单测）
    expect(SRC).toContain('NTooltip')
    expect(SRC).toContain('billDetailText(row.detail)')
  })

  it('记一笔：默认带当前过滤上游，POST 的是十进制金额串', async () => {
    await open()
    api.on('post', '/bills', { data: { data: MANUAL_BILL } })
    await clickButton('记一笔')

    // 表单默认选中过滤/第一个上游、日期为今天，只需填金额
    await fillInput(inputByLabel('金额'), '12.50')
    await clickButton('保存')
    await flush()

    const posts = callsTo('post', '/bills')
    expect(posts).toHaveLength(1)
    expect(posts[0].data.upstream_id).toBe(1)
    expect(posts[0].data.amount).toBe('12.50')
    expect(posts[0].data.currency).toBe('CNY')
    expect(posts[0].data.date).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    expect(messagesOf('success')).toContain('已记一笔')
    // 保存后重查汇总与明细
    expect(callsTo('get', '/bills/summary').length).toBeGreaterThanOrEqual(2)
    expect(callsTo('get', '/bills').length).toBeGreaterThanOrEqual(2)
  })

  it('手动记账提交表单选择的非默认币种', async () => {
    await open()
    api.on('post', '/bills', { data: { data: { ...MANUAL_BILL, currency: 'USD' } } })
    await clickButton('记一笔')
    await fillInput(inputByLabel('金额'), '50')
    await fillInput(inputByLabel('币种'), 'usd')
    await clickButton('保存')
    await flush()

    const posts = callsTo('post', '/bills')
    expect(posts).toHaveLength(1)
    expect(posts[0].data.currency).toBe('USD')
  })

  it('趋势按币种切换且不跨币种求和，涉及上游按 id 去重', async () => {
    api.on('get', '/bills/summary', { data: {
      buckets: [{ bucket: '2026-10-20', amounts: [
        { currency: 'CNY', amount_micros: 100_000_000 },
        { currency: 'USD', amount_micros: 50_000_000 },
      ], count: 2 }],
      upstreams: [
        { upstream_id: 1, upstream_name: 'deepseek', currency: 'CNY', amount_micros: 100_000_000, count: 1 },
        { upstream_id: 1, upstream_name: 'deepseek', currency: 'USD', amount_micros: 50_000_000, count: 1 },
      ],
      kinds: [{ kind: 'manual', currency: 'CNY', count: 1, amount_micros: 100_000_000 }, { kind: 'manual', currency: 'USD', count: 1, amount_micros: 50_000_000 }],
    } })
    await open()
    // beforeEach 的默认汇总先消费一次，再刷新取得本用例的多币种响应。
    ;(document.body.querySelector('.page-header-side button') as HTMLElement).click()
    await flush()

    const chart = wrapper!.findComponent(BarChart)
    let series = chart.props('series') as { values: number[] }[]
    expect(series[0].values).toEqual([100])
    expect((chart.props('formatValue') as (n: number) => string)(100)).toBe('¥100.00')
    expect(wrapper!.findAllComponents(StatCard)[3].props('value')).toBe('1')

    const currencySelect = wrapper!.findAllComponents(NSelect).find((c) => c.attributes('aria-label') === '趋势币种')
    expect(currencySelect).toBeDefined()
    currencySelect!.vm.$emit('update:value', 'USD')
    await flush()
    series = chart.props('series') as { values: number[] }[]
    expect(series[0].values).toEqual([50])
    expect((chart.props('formatValue') as (n: number) => string)(50)).toBe('$50.00')
  })

  it('记一笔：金额非法时阻止提交', async () => {
    await open()
    await clickButton('记一笔')
    await fillInput(inputByLabel('金额'), 'abc')
    await clickButton('保存')

    expect(callsTo('post', '/bills')).toHaveLength(0)
    expect(messagesOf('warning').join('')).toContain('金额格式')
  })

  it('删除：确认后 DELETE 该账单并刷新（自动账单也可删，算错时的人工出口）', async () => {
    await open()
    api.on('delete', '/bills/11', { data: { ok: true } })

    await clickButton('删除')
    expect(bodyHas('删除这条账单？')).toBe(true)
    await clickButton('确认')
    await flush()

    expect(callsTo('delete', '/bills/11')).toHaveLength(1)
    expect(messagesOf('success')).toContain('已删除')
    expect(callsTo('get', '/bills').length).toBeGreaterThanOrEqual(2)
  })

  it('删除末页唯一一条后回退到上一页', async () => {
    let remaining = 21
    const firstPage = Array.from({ length: 20 }, (_, i) => ({ ...MANUAL_BILL, id: 100 + i }))
    api.on('get', '/bills', (call) => ({ data: {
      data: call.params.page === 2 && remaining === 21 ? [{ ...MANUAL_BILL, id: 12 }] : call.params.page === 1 ? firstPage : [],
      total: remaining,
    } }))
    await open()
    const table = wrapper!.findAllComponents(NDataTable).find((t) => t.props('pagination'))
    expect(table).toBeDefined()
    ;(table!.props('pagination') as { onChange: (page: number) => void }).onChange(2)
    await flush()
    expect(callsTo('get', '/bills').at(-1)?.params.page).toBe(2)

    api.on('delete', '/bills/12', { data: { ok: true } })
    remaining = 20
    await clickButton('删除')
    await clickButton('确认')
    await flush()

    expect(callsTo('get', '/bills').at(-1)?.params.page).toBe(1)
  })

  it('切换趋势档位只重查汇总（按月桶参数下发）', async () => {
    await open()
    const before = callsTo('get', '/bills/summary').length

    // n-radio-button 渲染成 label+input，不是 <button>，按 label 文本点击
    const monthRadio = Array.from(document.body.querySelectorAll('.n-radio-button')).find(
      (el) => (el.textContent ?? '').trim() === '按月',
    ) as HTMLElement | undefined
    expect(monthRadio).toBeDefined()
    monthRadio!.click()
    await flush()

    const after = callsTo('get', '/bills/summary')
    expect(after.length).toBe(before + 1)
    expect(after[after.length - 1].params.bucket).toBe('month')
    // 明细不跟着档位走
    expect(callsTo('get', '/bills').length).toBe(1)
  })
})
