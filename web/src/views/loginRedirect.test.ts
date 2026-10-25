/**
 * 登录页的落点契约（同一视图在 /login 与 /glass/login 两种挂载路径下都要守）：
 *   - 带 `?redirect=` 时登录成功要回到原目标（深链分享、会话过期回来都靠它）；
 *   - 没有 redirect 时回当前套件的默认落地页（classic → /dashboard，
 *     glass → /glass/dashboard，由 useLoginRedirect 的套件感知 fallback 决定）；
 *   - 开放重定向必须被挡住：`//evil.com`（协议相对 URL）、`https://evil.com`、
 *     `/\evil.com`（部分浏览器把 '\' 当 '/'）一律回落默认页，绝不跳外站；
 *   - 登录失败时不跳转，并显示「密码错误」。
 *
 * 这是路由守卫与登录页之间的回归网：守卫负责把原目标写进 `?redirect=`，
 * 登录页负责安全消费。
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import naive from 'naive-ui'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { bodyHas, clickButton, flush } from '@/test/ui'
import Login from './Login.vue'
import type { AnyWrapper } from '@/test/ui'

let api: ApiMock
let router: Router
let wrapper: AnyWrapper | null = null

/** 只挂登录页与它可能跳去的落点；'/' 与 '/glass' 按真实路由那样重定向到 dashboard */
function makeRouter(): Router {
  const blank = { template: '<div />' }
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', redirect: '/dashboard' },
      { path: '/glass', redirect: '/glass/dashboard' },
      { path: '/login', name: 'login', component: Login },
      { path: '/glass/login', name: 'glass-login', component: Login },
      { path: '/dashboard', name: 'dashboard', component: blank },
      { path: '/usage', name: 'usage', component: blank },
      { path: '/glass/dashboard', name: 'glass-dashboard', component: blank },
      { path: '/glass/usage', name: 'glass-usage', component: blank },
    ],
  })
}

async function mountLoginAt(initial: string): Promise<void> {
  router = makeRouter()
  await router.push(initial)
  await router.isReady()
  wrapper = mount(Login as any, { global: { plugins: [naive, router] }, attachTo: document.body })
  await flush()
}

/** 填密码并点「进入控制台」 */
async function submitLogin(password = 'admin'): Promise<void> {
  await wrapper!.find('input').setValue(password)
  await clickButton('进入控制台')
  await flush()
}

beforeEach(() => {
  api = createApiMock().install()
  localStorage.clear()
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  api.restore()
  document.body.innerHTML = ''
})

describe('classic 登录页（挂载在 /login）', () => {
  it('带 ?redirect= 时登录后回到原目标（含 query）', async () => {
    api.on('post', '/login', { data: { ok: true } })
    await mountLoginAt('/login?redirect=%2Fusage%3Frange%3D7d')

    await submitLogin('s3cret')

    expect(router.currentRoute.value.fullPath).toBe('/usage?range=7d')
    // 顺带钉住提交体：密码要真的发出去
    expect(api.callsTo('post', '/login')).toHaveLength(1)
    expect(api.callsTo('post', '/login')[0].data).toEqual({ password: 's3cret' })
  })

  it('没有 redirect 时回默认落地页', async () => {
    api.on('post', '/login', { data: { ok: true } })
    await mountLoginAt('/login')

    await submitLogin()

    expect(router.currentRoute.value.path).toBe('/dashboard')
  })

  it.each([
    ['//evil.com', '协议相对 URL'],
    ['https://evil.com', '绝对 URL'],
    ['/\\evil.com', '反斜杠变体'],
  ])('开放重定向被挡住：%s（%s）', async (redirect) => {
    api.on('post', '/login', { data: { ok: true } })
    await mountLoginAt(`/login?redirect=${encodeURIComponent(redirect)}`)

    await submitLogin()

    expect(router.currentRoute.value.path).toBe('/dashboard')
  })

  it('登录失败不跳转并提示密码错误', async () => {
    api.on('post', '/login', { status: 401, data: { error: 'wrong password' } })
    await mountLoginAt('/login?redirect=%2Fusage')

    await submitLogin('wrong')

    expect(router.currentRoute.value.name).toBe('login')
    expect(bodyHas('密码错误')).toBe(true)
  })
})

describe('glass 登录页（同一视图挂载在 /glass/login）', () => {
  it('带 ?redirect= 时登录后回到玻璃主题的原目标', async () => {
    api.on('post', '/login', { data: { ok: true } })
    await mountLoginAt('/glass/login?redirect=%2Fglass%2Fusage')

    await submitLogin()

    expect(router.currentRoute.value.fullPath).toBe('/glass/usage')
  })

  it('没有 redirect 时回玻璃 dashboard', async () => {
    api.on('post', '/login', { data: { ok: true } })
    await mountLoginAt('/glass/login')

    await submitLogin()

    expect(router.currentRoute.value.path).toBe('/glass/dashboard')
  })

  it('开放重定向被挡住', async () => {
    api.on('post', '/login', { data: { ok: true } })
    await mountLoginAt('/glass/login?redirect=%2F%2Fevil.com')

    await submitLogin()

    expect(router.currentRoute.value.path).toBe('/glass/dashboard')
  })
})
