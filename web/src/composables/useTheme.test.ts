import { beforeEach, describe, expect, it } from 'vitest'
import { THEME_STORAGE_KEY, initTheme, resolveInitialMode, useTheme } from './useTheme'

/** 固定系统偏好，避免测试结果被运行环境影响。 */
function stubSystemPreference(prefersLight: boolean) {
  window.matchMedia = ((query: string) => ({
    matches: prefersLight,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
}

beforeEach(() => {
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
  stubSystemPreference(false)
})

describe('useTheme', () => {
  it('没有本地记录、系统无偏好时默认暗色', () => {
    expect(resolveInitialMode()).toBe('dark')
    initTheme()
    const { mode, isDark } = useTheme()
    expect(mode.value).toBe('dark')
    expect(isDark.value).toBe(true)
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('跟随系统的浅色偏好', () => {
    stubSystemPreference(true)
    expect(resolveInitialMode()).toBe('light')
  })

  it('本地记录优先于系统偏好', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'dark')
    stubSystemPreference(true)
    expect(resolveInitialMode()).toBe('dark')
  })

  it('忽略非法的本地记录', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'rainbow')
    stubSystemPreference(true)
    expect(resolveInitialMode()).toBe('light')
  })

  it('toggle 切换明暗、同步 <html> 并写回 localStorage', () => {
    initTheme()
    const { toggle, isDark } = useTheme()

    toggle()
    expect(isDark.value).toBe(false)
    expect(document.documentElement.dataset.theme).toBe('light')

    toggle()
    expect(isDark.value).toBe(true)
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark')
  })

  it('setMode 同样落盘', () => {
    initTheme()
    useTheme().setMode('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
    expect(document.documentElement.dataset.theme).toBe('light')
  })

  it('initTheme 读取已保存的浅色并立即生效', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'light')
    initTheme()
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(useTheme().isDark.value).toBe(false)
  })

  it('跟随系统时不会把偏好写进 localStorage（否则就被固化了）', () => {
    stubSystemPreference(true)
    initTheme()
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBeNull()
  })
})
