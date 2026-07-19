import { beforeEach, describe, expect, it } from 'vitest'
import type { AxiosAdapter } from 'axios'
import client from './client'

// rejectWith returns an adapter that always fails with the given HTTP status,
// so the response interceptor can be exercised without a real server.
function rejectWith(status: number): AxiosAdapter {
  return async (config) => {
    throw { response: { status }, config }
  }
}

describe('api client 401 interceptor', () => {
  beforeEach(() => {
    localStorage.clear()
    window.location.hash = '#/upstreams'
  })

  it('clears the authed flag and redirects to #/login on 401', async () => {
    localStorage.setItem('authed', '1')
    client.defaults.adapter = rejectWith(401)

    await expect(client.get('/upstreams')).rejects.toBeTruthy()

    expect(localStorage.getItem('authed')).toBeNull()
    expect(window.location.hash).toBe('#/login')
  })

  it('does not redirect for non-401 errors', async () => {
    localStorage.setItem('authed', '1')
    client.defaults.adapter = rejectWith(500)

    await expect(client.get('/upstreams')).rejects.toBeTruthy()

    expect(localStorage.getItem('authed')).toBe('1')
    expect(window.location.hash).toBe('#/upstreams')
  })

  it('does not redirect when already on #/login', async () => {
    window.location.hash = '#/login'
    client.defaults.adapter = rejectWith(401)

    await expect(client.get('/upstreams')).rejects.toBeTruthy()

    expect(window.location.hash).toBe('#/login')
  })
})
