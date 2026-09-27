/**
 * useUpstreamList 的行为契约（两套 Upstreams 页面共用的列表状态）。
 *
 * 这里守的是三条容易在重构里丢掉、且线上真的会出问题的规则：
 *   1. 切档位只重查列表——余额快照要做全表 GROUP BY，不该跟着每次点击走；
 *   2. 请求序号守卫——快速切档时「先发后到」的旧响应不能覆盖新档位的列表；
 *   3. 余额接口失败要降级成空快照，不能连累（也不该报错打扰）上游列表。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import { useUpstreamList } from './useUpstreamList'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { messagesOf, resetMessages } from '@/test/naiveMessage'
import type { Upstream } from '@/api/upstreams'
import type { BalanceSnapshot } from '@/api/balances'

vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

function upstream(over: Partial<Upstream> = {}): Upstream {
  return { id: 1, name: 'a', base_url: 'https://a', api_key: 'k', format: 'openai', enabled: true, daily_token_limit: 0, monthly_token_limit: 0, max_concurrent: 100, ...over }
}

function snapshot(upstreamId: number): BalanceSnapshot {
  return { id: upstreamId, upstream_id: upstreamId, upstream_name: `u${upstreamId}`, vendor: 'deepseek', payload: { kind: 'balance', is_available: true, balances: [] }, created_at: '2026-01-01T00:00:00+08:00' }
}

/** 挂起的响应：手动释放，用来构造「先发的请求后返回」 */
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => { resolve = r })
  return { promise, resolve }
}

/** 等 axios → api 模块 → composable 之间的若干层 promise 全部落地（宏任务边界最省心） */
function settle(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0))
}

let api: ApiMock

beforeEach(() => {
  resetMessages()
  api = createApiMock().install()
})

function mount() {
  return useUpstreamList()
}

describe('useUpstreamList', () => {
  it('默认档位是「启用中」，load 同时拉列表与余额快照', async () => {
    api.on('get', '/upstreams', { data: { data: [upstream()] } })
    api.on('get', '/balances', { data: { data: [snapshot(1)] } })

    const list = mount()
    await list.load()

    expect(list.statusFilter.value).toBe('enabled')
    expect(api.calls[0].params).toEqual({ status: 'enabled' })
    expect(list.upstreams.value.map((u) => u.name)).toEqual(['a'])
    // 余额按 upstream_id 建索引
    expect(Object.keys(list.balancesByUpstream.value)).toEqual(['1'])
  })

  // 规则：切档位只重查列表；余额快照与启用档位无关，不该跟着档位点击反复全表聚合
  it('切换档位只重查列表，不重新拉余额快照', async () => {
    api.on('get', '/upstreams', { data: { data: [upstream()] } })
    api.on('get', '/balances', { data: { data: [] } })
    const list = mount()
    await list.load()
    const balanceCalls = api.callsTo('get', '/balances').length

    api.on('get', '/upstreams', { data: { data: [upstream({ id: 2, name: 'b', enabled: false })] } })
    list.setStatusFilter('disabled')
    await settle()

    expect(api.callsTo('get', '/upstreams').map((c) => c.params.status)).toEqual(['enabled', 'disabled'])
    expect(api.callsTo('get', '/balances')).toHaveLength(balanceCalls)
    expect(list.upstreams.value.map((u) => u.name)).toEqual(['b'])
  })

  // 规则：请求序号守卫——先发后到的旧响应必须被丢弃，否则表格显示与单选按钮各说各话
  it('先发的慢请求返回时不覆盖后发请求的结果', async () => {
    const slow = deferred<{ data: { data: Upstream[] } }>()
    api.on('get', '/upstreams', () => slow.promise) // 第 1 次：一直挂着
    api.on('get', '/upstreams', { data: { data: [upstream({ id: 9, name: 'fast' })] } }) // 第 2 次起
    api.on('get', '/balances', { data: { data: [] } })

    const list = mount()
    const first = list.loadList() // 档位 enabled，挂在 slow 上

    list.setStatusFilter('disabled') // 第 2 次：立刻返回
    await settle()
    expect(list.upstreams.value.map((u) => u.name)).toEqual(['fast'])

    // 迟到的旧响应（属于上一次查询）这时才放行
    slow.resolve({ data: { data: [upstream({ id: 1, name: 'stale' })] } })
    await first

    expect(list.upstreams.value.map((u) => u.name)).toEqual(['fast'])
  })

  // 规则：只有仍然是最新的那次请求失败才报错；被丢弃的旧请求失败不该弹提示
  it('被丢弃的旧请求失败时不打扰用户', async () => {
    const slow = deferred<never>()
    api.on('get', '/upstreams', () => slow.promise)
    api.on('get', '/upstreams', { data: { data: [] } })
    api.on('get', '/balances', { data: { data: [] } })

    const list = mount()
    const stale = list.loadList()

    list.setStatusFilter('all')
    await settle()

    // 放行的旧响应直接失败：旧请求的错误不该弹给用户
    slow.resolve(Promise.reject(new Error('boom')) as never)
    await stale

    expect(messagesOf('error')).toEqual([])
  })

  // 规则：最新一次请求失败要报错，且错误文案优先取服务端给的 error 字段
  it('最新一次列表查询失败时展示服务端错误原文', async () => {
    api.on('get', '/upstreams', { status: 500, data: { error: '数据库不可用' } })
    api.on('get', '/balances', { data: { data: [] } })

    const list = mount()
    await list.loadList()

    expect(messagesOf('error')).toEqual(['加载上游列表失败：数据库不可用'])
  })

  // 规则：错误体是纯文本 / 无 error 字段 / 完全拿不到 response 时都要有可读文案
  it('错误消息提取的降级路径：纯文本体、非 error 结构、无 response', async () => {
    api.on('get', '/balances', { data: { data: [] } })

    api.on('get', '/upstreams', { status: 502, data: 'upstream 404: no models' })
    await mount().loadList()
    expect(messagesOf('error').at(-1)).toBe('加载上游列表失败：upstream 404: no models')

    api.on('get', '/upstreams', { status: 400, data: { detail: 'x' } })
    await mount().loadList()
    expect(messagesOf('error').at(-1)).toBe('加载上游列表失败：{"detail":"x"}')

    api.on('get', '/upstreams', () => { throw new Error('Network Error') })
    await mount().loadList()
    expect(messagesOf('error').at(-1)).toBe('加载上游列表失败：Network Error')
  })

  // 规则：余额接口挂了（例如后端还没升级）必须降级为空快照，且不阻塞上游列表、不报错
  it('余额快照拉取失败时降级为空表且不报错', async () => {
    api.on('get', '/upstreams', { data: { data: [upstream()] } })
    api.on('get', '/balances', { status: 500, data: { error: 'not implemented' } })

    const list = mount()
    await list.load()

    expect(list.upstreams.value).toHaveLength(1)
    expect(list.balancesByUpstream.value).toEqual({})
    expect(messagesOf('error')).toEqual([])
  })

  // 规则：切换启用状态 = PUT enabled + 按当前档位整列重查（该行是否还属于本档位由后端决定）
  it('toggleEnabled 成功后更新行状态并重查列表', async () => {
    api.on('get', '/upstreams', { data: { data: [upstream()] } })
    api.on('get', '/balances', { data: { data: [] } })
    api.on('put', '/upstreams/1', { data: upstream({ enabled: false }) })

    const list = mount()
    await list.load()
    const row = list.upstreams.value[0]
    await list.toggleEnabled(row, false)

    const puts = api.callsTo('put', '/upstreams/1')
    expect(puts).toHaveLength(1)
    expect(puts[0].data).toEqual({ enabled: false })
    expect(row.enabled).toBe(false)
    // 重查仍带当前档位
    expect(api.callsTo('get', '/upstreams')).toHaveLength(2)
  })

  // 规则：切换失败时不能显示成已生效（保持原状态），并给出服务端错误原文
  it('toggleEnabled 失败时保持原状态并报错', async () => {
    api.on('get', '/upstreams', { data: { data: [upstream()] } })
    api.on('get', '/balances', { data: { data: [] } })
    api.on('put', '/upstreams/1', { status: 409, data: { error: '上游正在被使用' } })

    const list = mount()
    await list.load()
    const row = list.upstreams.value[0]
    await list.toggleEnabled(row, false)

    expect(row.enabled).toBe(true)
    expect(messagesOf('error')).toEqual(['禁用失败：上游正在被使用'])
    // 失败不重查（避免用一次没生效的状态刷新列表）
    expect(api.callsTo('get', '/upstreams')).toHaveLength(1)
  })

  // 规则：失败文案要区分方向（启用失败 / 禁用失败），否则用户不知道点的是哪个
  it('启用方向的失败文案是「启用失败」', async () => {
    api.on('get', '/upstreams', { data: { data: [upstream({ enabled: false })] } })
    api.on('get', '/balances', { data: { data: [] } })
    api.on('put', '/upstreams/1', { status: 500, data: { error: 'boom' } })

    const list = mount()
    await list.load()
    await list.toggleEnabled(list.upstreams.value[0], true)

    expect(messagesOf('error')).toEqual(['启用失败：boom'])
  })

  // 规则：两个页面各自持有一份状态（不是模块级单例），否则一个页面的刷新会影响另一个
  it('返回的 ref 可被页面直接绑定，且实例之间不共享状态', async () => {
    api.on('get', '/upstreams', { data: { data: [] } })
    api.on('get', '/balances', { data: { data: [snapshot(7)] } })

    const list = mount()
    expect(list.upstreams.value).toEqual([])
    await list.loadBalances()
    expect(list.balancesByUpstream.value[7].upstream_id).toBe(7)
    // 不是模块级单例：两个页面各自持有一份状态
    const other = mount()
    expect(other.upstreams).not.toBe(list.upstreams)
    expect(ref([]).value).toEqual([])
  })
})
