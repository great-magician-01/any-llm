import { beforeEach, describe, expect, it, vi } from 'vitest'
import { THEME_STORAGE_KEY } from './useTheme'

type ThemeModule = typeof import('./useTheme')

/**
 * useTheme 是模块级单例（mode / manualChoice / watcherInstalled 都挂在模块作用域上），
 * 而 vitest 只做文件级隔离、不重置模块状态：同一个实例跨用例复用会让断言依赖执行顺序
 * ——比如 manualChoice 被前面的用例置为 true 后不会复位，后续 initTheme 一旦改变 mode
 * 就会误写 localStorage，「跟随系统时不落盘」那条用例便会莫名其妙地失败。
 * 因此每个用例先 resetModules 再取一份全新实例。
 *
 * THEME_STORAGE_KEY 是纯常量（不持有状态），静态 import 即可。
 */
async function freshTheme(): Promise<ThemeModule> {
  vi.resetModules()
  return await import('./useTheme')
}

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
  it('没有本地记录、系统无偏好时默认暗色', async () => {
    const { resolveInitialMode, initTheme, useTheme } = await freshTheme()
    expect(resolveInitialMode()).toBe('dark')
    initTheme()
    const { mode, isDark } = useTheme()
    expect(mode.value).toBe('dark')
    expect(isDark.value).toBe(true)
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('跟随系统的浅色偏好', async () => {
    const { resolveInitialMode } = await freshTheme()
    stubSystemPreference(true)
    expect(resolveInitialMode()).toBe('light')
  })

  it('本地记录优先于系统偏好', async () => {
    const { resolveInitialMode } = await freshTheme()
    localStorage.setItem(THEME_STORAGE_KEY, 'dark')
    stubSystemPreference(true)
    expect(resolveInitialMode()).toBe('dark')
  })

  it('忽略非法的本地记录', async () => {
    const { resolveInitialMode } = await freshTheme()
    localStorage.setItem(THEME_STORAGE_KEY, 'rainbow')
    stubSystemPreference(true)
    expect(resolveInitialMode()).toBe('light')
  })

  it('toggle 切换明暗、同步 <html> 并写回 localStorage', async () => {
    const { initTheme, useTheme } = await freshTheme()
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

  it('setMode 同样落盘', async () => {
    const { initTheme, useTheme } = await freshTheme()
    initTheme()
    useTheme().setMode('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
    expect(document.documentElement.dataset.theme).toBe('light')
  })

  it('initTheme 读取已保存的浅色并立即生效', async () => {
    const { initTheme, useTheme } = await freshTheme()
    localStorage.setItem(THEME_STORAGE_KEY, 'light')
    initTheme()
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(useTheme().isDark.value).toBe(false)
  })

  it('跟随系统时不会把偏好写进 localStorage（否则就被固化了）', async () => {
    const { initTheme } = await freshTheme()
    stubSystemPreference(true)
    initTheme()
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBeNull()
  })
})
