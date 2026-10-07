/**
 * 用量统计页「用量汇总」的默认时间范围：默认本月 1 号～今天，而不是全部用量
 * （全量口径会把历史累计摊在首屏，看不出当月消耗）。
 *
 * 断言落在真实发出的请求参数上（走真实 axios，假后端只换 adapter），并且两套
 * 主题各跑一遍——经典的 /usage 与毛玻璃的 /glass/usage 是逐行重复的实现，
 * 「只改了一边」要在这里变成红灯。
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { NDatePicker } from 'naive-ui'
import type { Component } from 'vue'
import ClassicUsage from './classic/views/Usage.vue'
import GlassUsage from './glass/views/GlassUsage.vue'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { flush, mountPage } from '@/test/ui'

let api: ApiMock
let wrapper: ReturnType<typeof mountPage> | null = null

beforeEach(() => {
  api = createApiMock().install()
  api.on('get', '/usage/summary', { data: { data: [] } })
  api.on('get', '/usage/daily', { data: { data: [] } })
  api.on('get', '/usage/records', { data: { data: [], total: 0 } })
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

const VIEWS: Array<[string, Component]> = [
  ['经典', ClassicUsage],
  ['毛玻璃', GlassUsage],
]

describe('用量统计：用量汇总默认只看本月', () => {
  it.each(VIEWS)('%s视图首屏请求带本月范围，而不是不带范围的全量', async (_name, component) => {
    await open(component)

    const params = summaryParams()
    expect(params.group_by).toBe('model')
    expect(params.from, '默认必须带起始时间（不带 = 全部用量）').toBeTruthy()
    expectCurrentMonth(params)
  })

  it.each(VIEWS)('%s视图的 Token 构成（缓存/推理）跟随同一个范围', async (_name, component) => {
    await open(component)

    const params = summaryParams()
    // 汇总接口不含缓存/推理 token，这两个数从日聚合叠加得到：范围必须和汇总一致，
    // 否则「Token 构成」圆环与上方汇总卡对不上
    const extras = api.callsTo('get', '/usage/daily').find((c) => c.params.days === 90)
    expect(extras, '没有找到 Token 构成的取数请求').toBeTruthy()
    expect(extras!.params.from).toBe(params.from)
    expect(extras!.params.to).toBe(params.to)
  })

  it.each(VIEWS)('%s视图清空日期范围后回到全部用量', async (_name, component) => {
    await open(component)
    expectCurrentMonth(summaryParams())

    // 清空 = 全量口径（服务端不传 from/to），这条路径不能被默认值堵死
    wrapper!.findComponent(NDatePicker).vm.$emit('update:value', null)
    await flush()

    const params = summaryParams()
    expect(params.from).toBeUndefined()
    expect(params.to).toBeUndefined()
  })

  it.each(VIEWS)('%s视图的趋势图仍走自己的 7/14/30 天窗口', async (_name, component) => {
    await open(component)

    // 趋势图的档位是独立控件，默认近 14 天：不能被汇总的月份范围顶掉，
    // 否则档位按钮显示 14 天、图上却是整月
    const trend = api.callsTo('get', '/usage/daily').find((c) => c.params.days === 14)
    expect(trend, '没有找到趋势图取数请求').toBeTruthy()
    expect(trend!.params.from).toBeUndefined()
    expect(trend!.params.to).toBeUndefined()
  })

  it('两套主题的默认汇总请求完全一致', async () => {
    await open(ClassicUsage)
    const classic = summaryParams()
    wrapper?.unmount()

    await open(GlassUsage)
    const glass = summaryParams()

    expect(glass).toEqual(classic)
  })
})
