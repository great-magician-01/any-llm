import { beforeEach, describe, expect, it, vi } from 'vitest'
import { LEGACY_ACCENT_STORAGE_KEY, THEME_STORAGE_KEY } from './useTheme'

type ThemeModule = typeof import('./useTheme')

/**
 * useTheme 是模块级单例（theme / manualChoice / watcherInstalled 都挂在模块作用域上），
 * 而 vitest 只做文件级隔离、不重置模块状态：同一个实例跨用例复用会让断言依赖执行顺序
 * ——比如 manualChoice 被前面的用例置为 true 后不会复位，后续 initTheme 一旦改变 theme
 * 就会误写 localStorage，「跟随系统时不落盘」那条用例便会莫名其妙地失败。
 * 因此每个用例先 resetModules 再取一份全新实例。
 *
 * THEME_STORAGE_KEY / LEGACY_ACCENT_STORAGE_KEY 是纯常量（不持有状态），静态 import 即可。
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
  it('没有本地记录、系统无偏好时默认深色', async () => {
    const { resolveInitialTheme, initTheme, useTheme } = await freshTheme()
    expect(resolveInitialTheme()).toBe('dark')
    initTheme()
    const { theme, mode, isDark } = useTheme()
    expect(theme.value).toBe('dark')
    expect(mode.value).toBe('dark')
    expect(isDark.value).toBe(true)
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('跟随系统的浅色偏好', async () => {
    const { resolveInitialTheme } = await freshTheme()
    stubSystemPreference(true)
    expect(resolveInitialTheme()).toBe('light')
  })

  it('本地主题记录优先于系统偏好', async () => {
    const { resolveInitialTheme } = await freshTheme()
    localStorage.setItem(THEME_STORAGE_KEY, 'dark')
    stubSystemPreference(true)
    expect(resolveInitialTheme()).toBe('dark')
  })

  it('忽略非法的本地记录', async () => {
    const { resolveInitialTheme } = await freshTheme()
    localStorage.setItem(THEME_STORAGE_KEY, 'rainbow')
    stubSystemPreference(true)
    expect(resolveInitialTheme()).toBe('light')
  })

  it('setTheme 同步 <html> 并落盘，机体主题同时决定暗色与品牌色', async () => {
    const { initTheme, useTheme } = await freshTheme()
    initTheme()
    const { setTheme, theme, mode, isDark } = useTheme()

    setTheme('eva-01')
    expect(document.documentElement.dataset.theme).toBe('eva-01')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('eva-01')
    expect(theme.value).toBe('eva-01')
    expect(mode.value).toBe('dark')
    expect(isDark.value).toBe(true)

    setTheme('light')
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(isDark.value).toBe(false)
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
  })

  it('选中的就是当前值也落盘（跟随系统的状态下等于确认了默认值）', async () => {
    const { initTheme, useTheme } = await freshTheme()
    stubSystemPreference(true)
    initTheme()
    useTheme().setTheme('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
  })

  it('initTheme 读取已保存的主题并立即生效', async () => {
    const { initTheme, useTheme } = await freshTheme()
    localStorage.setItem(THEME_STORAGE_KEY, 'eva-02')
    initTheme()
    expect(document.documentElement.dataset.theme).toBe('eva-02')
    expect(useTheme().theme.value).toBe('eva-02')
    expect(useTheme().isDark.value).toBe(true)
  })

  it('跟随系统时不会把偏好写进 localStorage（否则就被固化了）', async () => {
    const { initTheme } = await freshTheme()
    stubSystemPreference(true)
    initTheme()
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBeNull()
  })
})

describe('useTheme: 旧配色（accent）迁移', () => {
  it('旧配色 eva-01 迁移成同名主题，并清掉旧键、写回新键', async () => {
    const { initTheme, useTheme } = await freshTheme()
    localStorage.setItem(LEGACY_ACCENT_STORAGE_KEY, 'eva-01')
    initTheme()
    expect(useTheme().theme.value).toBe('eva-01')
    expect(document.documentElement.dataset.theme).toBe('eva-01')
    expect(localStorage.getItem(LEGACY_ACCENT_STORAGE_KEY)).toBeNull()
    // 写回新键：下次启动不再依赖旧记录（也解释了为什么不能只删旧键）
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('eva-01')
  })

  it('旧配色优先于旧的明暗记录（旧模型里浅色 + 初号机会塌缩成初号机暗色皮肤）', async () => {
    const { initTheme, useTheme } = await freshTheme()
    localStorage.setItem(LEGACY_ACCENT_STORAGE_KEY, 'eva-02')
    localStorage.setItem(THEME_STORAGE_KEY, 'light')
    initTheme()
    expect(useTheme().theme.value).toBe('eva-02')
  })

  it('旧配色的 default 不是一个主题，退回已保存的明暗', async () => {
    const { initTheme, useTheme } = await freshTheme()
    localStorage.setItem(LEGACY_ACCENT_STORAGE_KEY, 'default')
    localStorage.setItem(THEME_STORAGE_KEY, 'light')
    initTheme()
    expect(useTheme().theme.value).toBe('light')
    // 迁完也清掉：default 在新模型里没有对应记录
    expect(localStorage.getItem(LEGACY_ACCENT_STORAGE_KEY)).toBeNull()
  })

  it('迁移后停止跟随系统（旧记录也算用户选过）', async () => {
    const { initTheme, useTheme } = await freshTheme()
    stubSystemPreference(true)
    localStorage.setItem(LEGACY_ACCENT_STORAGE_KEY, 'eva-00')
    initTheme()
    // 系统偏好是浅色，但旧配色记录生效后不应再被系统变更改回去
    expect(useTheme().theme.value).toBe('eva-00')
  })
})
