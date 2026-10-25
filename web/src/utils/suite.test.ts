import { describe, expect, it } from 'vitest'
import { isGlassPath, suitePath, switchSuite } from './suite'

describe('isGlassPath', () => {
  it('识别 /glass 前缀', () => {
    expect(isGlassPath('/glass')).toBe(true)
    expect(isGlassPath('/glass/dashboard')).toBe(true)
    expect(isGlassPath('/glass/login?redirect=%2Fkeys')).toBe(true)
    expect(isGlassPath('/dashboard')).toBe(false)
    expect(isGlassPath('/login')).toBe(false)
  })
})

describe('suitePath', () => {
  it('classic 下原样返回', () => {
    expect(suitePath('/dashboard', '/upstreams')).toBe('/upstreams')
    expect(suitePath('/keys', '/login')).toBe('/login')
    expect(suitePath('/login', '/')).toBe('/')
  })

  it('glass 下加 /glass 前缀', () => {
    expect(suitePath('/glass/dashboard', '/upstreams')).toBe('/glass/upstreams')
    expect(suitePath('/glass/login', '/')).toBe('/glass/')
    expect(suitePath('/glass/conversations', '/login')).toBe('/glass/login')
  })
})

describe('switchSuite', () => {
  it('经典页切到玻璃侧对应页', () => {
    expect(switchSuite('/dashboard')).toBe('/glass/dashboard')
    expect(switchSuite('/upstreams')).toBe('/glass/upstreams')
    expect(switchSuite('/conversations')).toBe('/glass/conversations')
  })

  it('玻璃页切回经典侧对应页', () => {
    expect(switchSuite('/glass/dashboard')).toBe('/dashboard')
    expect(switchSuite('/glass/usage')).toBe('/usage')
  })

  it('套件根路径互换', () => {
    expect(switchSuite('/')).toBe('/glass/')
    expect(switchSuite('/glass')).toBe('/')
  })

  it('登录页互切', () => {
    expect(switchSuite('/login')).toBe('/glass/login')
    expect(switchSuite('/glass/login')).toBe('/login')
  })

  it('保留 query，并翻译其中的 redirect 深链', () => {
    expect(switchSuite('/login?redirect=%2Fusage')).toBe('/glass/login?redirect=%2Fglass%2Fusage')
    expect(switchSuite('/glass/login?redirect=%2Fglass%2Fkeys')).toBe(
      '/login?redirect=%2Fkeys',
    )
  })

  it('redirect 是外链时原样保留，不翻译（登录落地侧另有站内校验）', () => {
    expect(switchSuite('/login?redirect=https%3A%2F%2Fevil.com')).toBe(
      '/glass/login?redirect=https%3A%2F%2Fevil.com',
    )
  })
})
