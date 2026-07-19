import { beforeEach, describe, expect, it } from 'vitest'
import router from './router'

describe('router auth guard', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('redirects unauthenticated users to /login', async () => {
    await router.push('/upstreams')
    expect(router.currentRoute.value.name).toBe('login')
  })

  it('lets authenticated users through to protected pages', async () => {
    localStorage.setItem('authed', '1')
    await router.push('/keys')
    expect(router.currentRoute.value.name).toBe('keys')
  })

  it('bounces authenticated users away from /login', async () => {
    localStorage.setItem('authed', '1')
    await router.push('/login')
    expect(router.currentRoute.value.name).toBe('upstreams')
  })
})
