/**
 * 用量统计页「用量汇总」的默认时间范围：默认本月 1 号～今天，而不是全部用量
 * （全量口径会把历史累计摊在首屏，看不出当月消耗）。
 *
 * 断言落在真实发出的请求参数上（走真实 axios，假后端只换 adapter）。页面
 * classic/glass 两套 URL 树共用同一份视图，无需双跑。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { NDatePicker } from 'naive-ui'
import Usage from './Usage.vue'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { flush, mountPage } from '@/test/ui'

// useMessage 换成本地记录器（与 Upstreams 页测试同款）：Usage 页新增了加载
// 失败的 message.error，挂载时必须有 message 上下文。
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
  api.on('get', '/usage/models', { data: { data: [] } })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  api.restore()
})

async function open() {
  wrapper = mountPage(Usage)
  await flush()
}

/** 最后一次「按当前范围取数」的请求参数 */
function summaryParams(): Record<string, any> {
  const calls = api.callsTo('get', '/usage/summary')
  expect(calls.length).toBeGreaterThan(0)
  return calls[calls.length - 1].params
}

/** 断言范围就是「本月 1 号 00:00:00 ~ 今天 23:59:59」 */
function expectCurrentMonth(params: Record<string, any>) {
  const now = new Date()
  const from = new Date(params.from)
  const to = new Date(params.to)
  expect([from.getFullYear(), from.getMonth(), from.getDate()]).toEqual([now.getFullYear(), now.getMonth(), 1])
  expect([from.getHours(), from.getMinutes(), from.getSeconds()]).toEqual([0, 0, 0])
  expect([to.getFullYear(), to.getMonth(), to.getDate()]).toEqual([now.getFullYear(), now.getMonth(), now.getDate()])
  expect([to.getHours(), to.getMinutes(), to.getSeconds()]).toEqual([23, 59, 59])
}

describe('用量统计：用量汇总默认只看本月', () => {
  it('首屏请求带本月范围，而不是不带范围的全量', async () => {
    await open()

    const params = summaryParams()
    expect(params.group_by).toBe('model')
    expect(params.from, '默认必须带起始时间（不带 = 全部用量）').toBeTruthy()
    expectCurrentMonth(params)
  })

  it('Token 构成（缓存/推理）跟随同一个范围', async () => {
    await open()

    const params = summaryParams()
    // 汇总接口不含缓存/推理 token，这两个数从日聚合叠加得到：范围必须和汇总一致，
    // 否则「Token 构成」圆环与上方汇总卡对不上
    const extras = api.callsTo('get', '/usage/daily').find((c) => c.params.days === 90)
    expect(extras, '没有找到 Token 构成的取数请求').toBeTruthy()
    expect(extras!.params.from).toBe(params.from)
    expect(extras!.params.to).toBe(params.to)
  })

  it('清空日期范围后回到全部用量', async () => {
    await open()
    expectCurrentMonth(summaryParams())

    // 清空 = 全量口径（服务端不传 from/to），这条路径不能被默认值堵死
    wrapper!.findComponent(NDatePicker).vm.$emit('update:value', null)
    await flush()

    const params = summaryParams()
    expect(params.from).toBeUndefined()
    expect(params.to).toBeUndefined()
  })

  it('趋势图仍走自己的 7/14/30 天窗口', async () => {
    await open()

    // 趋势图的档位是独立控件，默认近 14 天：不能被汇总的月份范围顶掉，
    // 否则档位按钮显示 14 天、图上却是整月
    const trend = api.callsTo('get', '/usage/daily').find((c) => c.params.days === 14)
    expect(trend, '没有找到趋势图取数请求').toBeTruthy()
    expect(trend!.params.from).toBeUndefined()
    expect(trend!.params.to).toBeUndefined()
  })
})
