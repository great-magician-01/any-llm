/**
 * axios 实例（api/client.ts）的契约。
 *
 * 守的规则：
 *   - 所有管理端请求都带 /api/admin 前缀与 cookie（withCredentials），
 *     JSON 请求头默认带上——漏一处就是整站 401 或后端解析不到 body；
 *   - 401 时清掉本地登录标记并跳登录页（玻璃套件跳玻璃登录页）；
 *   - 并发多个 401 只跳一次（否则整屏请求会各自改一次 hash）；
 *   - 拦截器必须原样把错误抛回去：页面靠 err.response.data.error 给用户看服务端原文。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import client from './client'
import { createApiMock, type ApiMock } from '@/test/apiMock'

let api: ApiMock

beforeEach(() => {
  localStorage.clear()
  window.location.hash = '#/upstreams'
  api = createApiMock().install()
})

afterEach(() => {
  api.restore()
  vi.restoreAllMocks()
})

describe('api client 基础契约', () => {
  // 规则：所有管理端请求都必须走 /api/admin 前缀并带 cookie，否则整站 401
  it('baseURL / withCredentials / JSON 头固定', () => {
    expect(client.defaults.baseURL).toBe('/api/admin')
    expect(client.defaults.withCredentials).toBe(true)
    expect(client.defaults.headers['Content-Type']).toBe('application/json')
  })

  // 规则：api 模块只写业务路径，前缀由实例统一拼（改前缀只需改一处）
  it('业务路径最终拼成 /api/admin/<path>', () => {
    expect(client.getUri({ url: '/upstreams' })).toBe('/api/admin/upstreams')
    expect(client.getUri({ url: '/keys', params: { status: 'all' } })).toBe('/api/admin/keys?status=all')
  })

  // 规则：请求经实例发出时保留业务路径（前缀由 baseURL 负责，不重复拼）
  it('请求按 instance 前缀发出（不是裸路径）', async () => {
    api.on('get', '/upstreams', { data: { data: [] } })
    await client.get('/upstreams')
    expect(api.calls[0].url).toBe('/upstreams')
    expect(api.calls[0].method).toBe('get')
  })
})

describe('api client 401 并发去重', () => {
  // 规则：并发多个请求同时 401 时只允许跳转一次（否则 hash 被反复改写、登录页被刷屏）
  it('并发 5 个 401 只清一次登录标记、只跳一次登录页', async () => {
    localStorage.setItem('authed', '1')
    const removeSpy = vi.spyOn(localStorage, 'removeItem')
    for (let i = 0; i < 5; i++) api.on('get', `/thing${i}`, { status: 401, data: { error: 'unauthorized' } })

    const results = await Promise.allSettled([0, 1, 2, 3, 4].map((i) => client.get(`/thing${i}`)))

    // 401 不能被吞掉：5 个调用各自都要 reject（页面据此走错误分支）
    expect(results.map((r) => r.status)).toEqual(['rejected', 'rejected', 'rejected', 'rejected', 'rejected'])
    expect(removeSpy.mock.calls.filter((c) => c[0] === 'authed')).toHaveLength(1)
    expect(window.location.hash).toBe('#/login')
  })

  // 规则：玻璃套件同样只跳一次，且必须落在玻璃登录页（不能把人踢回经典主题）
  it('玻璃套件下并发 401 同样只跳一次，且跳玻璃登录页', async () => {
    window.location.hash = '#/glass/keys'
    localStorage.setItem('authed', '1')
    expect(window.location.hash).toBe('#/glass/keys') // 前置条件：确实站在玻璃页上
    const removeSpy = vi.spyOn(localStorage, 'removeItem')
    for (let i = 0; i < 4; i++) api.on('get', `/thing${i}`, { status: 401, data: {} })

    await Promise.allSettled([0, 1, 2, 3].map((i) => client.get(`/thing${i}`)))

    expect(removeSpy.mock.calls.filter((c) => c[0] === 'authed')).toHaveLength(1)
    expect(window.location.hash).toBe('#/glass/login')
  })

  // 规则：已经在登录页时不再重复清理与跳转（原地跳会刷掉用户正在输入的登录表单）
  it('已经在登录页时不再清标记（避免原地反复跳）', async () => {
    window.location.hash = '#/login'
    localStorage.setItem('authed', '1')
    const removeSpy = vi.spyOn(localStorage, 'removeItem')
    api.on('get', '/upstreams', { status: 401, data: {} })

    await expect(client.get('/upstreams')).rejects.toBeTruthy()

    expect(removeSpy.mock.calls.filter((c) => c[0] === 'authed')).toHaveLength(0)
  })
})

describe('api client 非 401 错误', () => {
  // 规则：只有 401 才代表会话失效；权限/服务端错误必须原样抛回，不能把人踢去登录页
  it('403/500 不跳转、不清标记，且错误对象原样抛出', async () => {
    window.location.hash = '#/upstreams'
    localStorage.setItem('authed', '1')
    const removeSpy = vi.spyOn(localStorage, 'removeItem')
    api.on('get', '/upstreams', { status: 403, data: { error: 'permission_error' } })
    api.on('post', '/keys', { status: 500, data: { error: 'db down' } })

    const forbidden = await client.get('/upstreams').catch((e) => e)
    const failed = await client.post('/keys', {}).catch((e) => e)

    // 页面靠这两个字段给用户看「服务端原文」
    expect(forbidden.response.status).toBe(403)
    expect(forbidden.response.data.error).toBe('permission_error')
    expect(failed.response.status).toBe(500)
    expect(failed.response.data.error).toBe('db down')
    expect(window.location.hash).toBe('#/upstreams')
    expect(localStorage.getItem('authed')).toBe('1')
    expect(removeSpy.mock.calls.filter((c) => c[0] === 'authed')).toHaveLength(0)
  })

  // 规则：404 等业务错误不能触发登出逻辑（否则一个笔误就让人重新登录）
  it('控制台 404（未登记路由）也不会被当成登录过期', async () => {
    localStorage.setItem('authed', '1')
    const err = await client.get('/nope').catch((e) => e)

    expect(err.response.status).toBe(404)
    expect(localStorage.getItem('authed')).toBe('1')
    expect(window.location.hash).toBe('#/upstreams')
  })
})
