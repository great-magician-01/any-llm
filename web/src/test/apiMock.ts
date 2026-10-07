/**
 * 组件/composable 测试用的 axios 假后端。
 *
 * 目标：断言落在「真实发出的 HTTP 请求」上（method / url / params / body），
 * 而不是「某个 api 模块函数被调用过」。做法是接管共享 axios 实例的 adapter
 * ——请求仍然走 axios 的完整链路（参数序列化、拦截器、错误对象），只是不发网。
 *
 * 未登记的路由一律回 404（带 error 体），免得测试里漏配的请求静默变成
 * undefined 而让断言失真。
 */
import type { AxiosAdapter, AxiosRequestConfig, AxiosResponse } from 'axios'
import client from '@/api/client'

export interface ApiCall {
  method: string
  url: string
  params: Record<string, any>
  data: any
}

export interface ApiReply {
  status?: number
  /** 响应体原样返回（envelope 由调用方自己写，如 { data: [...] }） */
  data?: any
}

/** 支持「同一路由多次调用返回不同结果」：登记多条按顺序消费，最后一条常驻。
 *  函数形式可以直接抛错（模拟无 response 的网络错误）或返回 Promise 挂起响应
 *  （用来构造竞态：先发的请求后返回）。 */
type Reply = ApiReply | ((call: ApiCall) => ApiReply | Promise<ApiReply>)

export interface ApiMock {
  calls: ApiCall[]
  /** 登记一条路由的响应；同一路由可登记多次以模拟先后不同的响应 */
  on(method: string, url: string, reply: Reply): ApiMock
  install(): ApiMock
  restore(): void
  callsTo(method: string, url: string): ApiCall[]
}

const key = (method: string, url: string) => `${method.toUpperCase()} ${url}`

/** axios 在进 adapter 前已把 JSON body 序列化成字符串 */
function parseBody(data: unknown): any {
  if (typeof data !== 'string' || data === '') return data
  try {
    return JSON.parse(data)
  } catch {
    return data
  }
}

export function createApiMock(): ApiMock {
  const calls: ApiCall[] = []
  const routes = new Map<string, Reply[]>()
  /** 每条路由最后消费掉的那条响应：登记只有一条时它就是默认响应 */
  const lastReply = new Map<string, Reply>()
  const previousAdapter = client.defaults.adapter

  const adapter: AxiosAdapter = async (config: AxiosRequestConfig) => {
    const method = (config.method ?? 'get').toLowerCase()
    const url = config.url ?? ''
    const k = key(method, url)
    const call: ApiCall = {
      method,
      url,
      params: (config.params ?? {}) as Record<string, any>,
      data: parseBody(config.data),
    }
    calls.push(call)

    const queue = routes.get(k)
    // 顺序消费：登记多条时按登记顺序一条一条给（用来模拟先后不同的响应）；
    // 队列空了就复用最后那条（单条登记 = 该路由的固定响应）
    let reply: Reply | undefined
    if (queue && queue.length > 0) {
      reply = queue.shift()!
      lastReply.set(k, reply)
    } else {
      reply = lastReply.get(k)
    }
    if (reply === undefined) {
      const err: any = new Error(`no api mock route for ${k}`)
      err.response = { status: 404, data: { error: 'no mock route' }, config }
      throw err
    }
    // 函数形式允许异步：请求会挂在 adapter 里，直到 Promise 决议（竞态测试用）
    const resolved = await (typeof reply === 'function' ? reply(call) : reply)
    const status = resolved.status ?? 200
    if (status < 200 || status >= 300) {
      const err: any = new Error(`Request failed with status code ${status}`)
      err.response = { status, data: resolved.data, config }
      throw err
    }
    // 手搓的响应对象：headers/config 的类型细节对被测代码无意义，按 AxiosResponse 交出去
    return { data: resolved.data ?? {}, status, statusText: 'OK', headers: {}, config } as AxiosResponse
  }

  const api: ApiMock = {
    calls,
    on(method, url, reply) {
      const k = key(method, url)
      const list = routes.get(k) ?? []
      list.push(reply)
      routes.set(k, list)
      return api
    },
    install() {
      client.defaults.adapter = adapter
      return api
    },
    restore() {
      client.defaults.adapter = previousAdapter
      calls.length = 0
      routes.clear()
      lastReply.clear()
    },
    callsTo(method, url) {
      const k = key(method, url)
      return calls.filter((c) => key(c.method, c.url) === k)
    },
  }

  return api
}
