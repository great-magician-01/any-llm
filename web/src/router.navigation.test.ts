/**
 * 路由层还缺的契约（router.test.ts 只覆盖了登录态跳转）。
 *
 * 守的规则：
 *   - `/` 落到 dashboard（不是空白页）；
 *   - afterEach 按路由前缀给 body 加/去 glass-mode（毛玻璃的 teleport 浮层靠它）；
 *   - 两套主题前缀下的业务页面集合一一对应（加页面只加一边 = 回归）；
 *   - 未登录深链的当前契约：只跳登录页、不保留原目标（这是已知 UX 缺陷，
 *     用断言固定现状，改实现时这条会红，逼着一起改）。
 */
import { beforeEach, describe, expect, it } from 'vitest'
import router from './router'

/** 两套主题下都要存在的业务页面（不含 login——名字刻意不同） */
const BUSINESS_PAGES = ['dashboard', 'upstreams', 'aliases', 'keys', 'usage', 'conversations']

beforeEach(() => {
  localStorage.clear()
  document.body.classList.remove('glass-mode')
  sessionStorage.clear()
})

describe('router 根路径与主题类名', () => {
  // 注意：vue-router 对「导航到当前所在位置」会直接返回重复导航、跳过守卫，
  // 所以每个用例先把落点挪开，再访问 '/'（否则断言的是上一次用例的残留状态）。
  it('已登录访问 / 重定向到 dashboard', async () => {
    localStorage.setItem('authed', '1')
    await router.push('/keys')
    await router.push('/')

    expect(router.currentRoute.value.name).toBe('dashboard')
    expect(router.currentRoute.value.path).toBe('/dashboard')
  })

  it('未登录访问 / 仍先落到当前主题的登录页', async () => {
    await router.push('/keys') // 未登录 → 被弹回 login
    await router.push('/')

    expect(router.currentRoute.value.name).toBe('login')
  })

  // 规则：glass-mode 只属于 /glass/*；经典页面上必须摘掉（否则经典页的浮层也变毛玻璃）
  it('afterEach 按前缀增删 body.glass-mode', async () => {
    localStorage.setItem('authed', '1')
    await router.push('/keys')
    expect(document.body.classList.contains('glass-mode')).toBe(false)

    await router.push('/glass/keys')
    expect(document.body.classList.contains('glass-mode')).toBe(true)

    await router.push('/keys')
    expect(document.body.classList.contains('glass-mode')).toBe(false)
  })

  // 规则：登录成功后用来防重载循环的 sessionStorage 标记，每次导航都要清掉
  it('afterEach 清掉「动态 import 失败已重载」标记', async () => {
    localStorage.setItem('authed', '1')
    sessionStorage.setItem('reloaded-on-import-fail', '1')
    await router.push('/dashboard')

    expect(sessionStorage.getItem('reloaded-on-import-fail')).toBeNull()
  })
})

describe('router 双主题前缀 parity', () => {
  // 规则：/x 与 /glass/x 必须成对存在；只加一边的路由在这里就会暴露
  it('每个业务页面在经典与毛玻璃前缀下都存在', () => {
    const routes = router.getRoutes()
    const names = routes.map((r) => r.name).filter(Boolean)
    const paths = routes.map((r) => r.path)

    for (const page of BUSINESS_PAGES) {
      expect(names, `缺少经典路由 ${page}`).toContain(page)
      expect(names, `缺少毛玻璃路由 glass-${page}`).toContain(`glass-${page}`)
      expect(paths, `缺少路径 /${page}`).toContain(`/${page}`)
      expect(paths, `缺少路径 /glass/${page}`).toContain(`/glass/${page}`)
    }
  })

  // 规则：glass 侧不许多页面也不许少页面，页面集合必须逐一对应
  it('两套前缀的页面集合完全一一对应', () => {
    const paths = router.getRoutes().map((r) => r.path)
    const classic = paths
      .filter((p) => !p.startsWith('/glass') && p !== '/')
      .map((p) => p.replace(/^\//, ''))
      .sort()
    const glass = paths
      .filter((p) => p.startsWith('/glass/'))
      .map((p) => p.replace(/^\/glass\//, ''))
      .sort()

    expect(glass).toEqual(classic)
  })

  // 规则：登录页是唯一名字不同的成对页面，两边都要能被解析到
  it('登录页两边都有，且各自有独立的登录后落地页', () => {
    const names = router.getRoutes().map((r) => r.name).filter(Boolean)
    expect(names).toContain('login')
    expect(names).toContain('glass-login')
    expect(router.resolve('/login').name).toBe('login')
    expect(router.resolve('/glass/login').name).toBe('glass-login')
  })
})

describe('router 未登录深链', () => {
  // 规则（当前契约，非理想）：未登录深链只跳登录页，原目标被丢弃——登录后用户
  // 回不到他想去的页面。断言锁住现状，改动这里必须同步更新本用例与报告。
  it('未登录访问深链只跳登录页，不保留 redirect 目标（已知 UX 缺陷）', async () => {
    await router.push('/usage?range=7d')

    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.fullPath).toBe('/login')
    expect(router.currentRoute.value.query).toEqual({})
  })

  // 规则：玻璃深链同样只跳玻璃登录页（不能把人扔到经典主题的登录页）
  it('未登录访问毛玻璃深链同样只跳玻璃登录页', async () => {
    await router.push('/glass/usage')

    expect(router.currentRoute.value.name).toBe('glass-login')
    expect(router.currentRoute.value.fullPath).toBe('/glass/login')
    expect(router.currentRoute.value.query).toEqual({})
  })

  // 规则：有会话时深链（含 query）要原样直达，不能被守卫改写
  it('已登录时深链正常直达（不经过登录页）', async () => {
    localStorage.setItem('authed', '1')
    await router.push('/usage?range=7d')

    expect(router.currentRoute.value.name).toBe('usage')
    expect(router.currentRoute.value.query).toEqual({ range: '7d' })
  })
})
