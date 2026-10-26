import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import ThemePicker from './ThemePicker.vue'
import { THEME_STORAGE_KEY, initTheme } from '@/composables/useTheme'
import { bodyHas, flush, mountPage, type AnyWrapper } from '@/test/ui'

/**
 * 界面主题选择器：popover 内容 teleport 到 body，断言全部打在 document.body 上
 * （与 Upstreams 等页面测试同一套约定）。useTheme 是模块级单例，先 initTheme
 * 把 watcher 装好，setTheme 的落盘 / 同步行为才会生效。
 */

let wrapper: AnyWrapper | null = null

/** 固定系统偏好为深色：测试环境（happy-dom）的 matchMedia 默认值不可依赖。 */
function stubDarkSystemPreference() {
  window.matchMedia = ((query: string) => ({
    matches: false,
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
  stubDarkSystemPreference()
  initTheme()
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
})

async function openPicker(): Promise<HTMLButtonElement> {
  wrapper = mountPage(ThemePicker)
  await flush()
  const trigger = document.querySelector('.theme-picker') as HTMLButtonElement | null
  if (!trigger) throw new Error('未找到主题选择器按钮')
  trigger.click()
  await flush()
  return trigger
}

/** 选项按钮里还有一行配色说明，按显示名的前缀定位（不依赖整段文案）。 */
async function clickThemeOption(name: string): Promise<void> {
  const btn = Array.from(document.querySelectorAll<HTMLButtonElement>('.theme-option')).find(
    (b) => (b.textContent ?? '').replace(/\s+/g, '').startsWith(name),
  )
  if (!btn) {
    const names = Array.from(document.querySelectorAll('.theme-option .option-name')).map(
      (e) => e.textContent,
    )
    throw new Error(`未找到主题选项「${name}」，当前选项：${names.join(' | ')}`)
  }
  btn.click()
  await flush()
}

describe('ThemePicker', () => {
  it('初始显示深色，菜单列出全部五个主题', async () => {
    const trigger = await openPicker()
    expect(trigger.getAttribute('aria-label')).toBe('界面主题：深色')
    for (const name of ['深色', '浅色', '初号机', '明日香', '凌波丽']) {
      expect(bodyHas(name)).toBe(true)
    }
  })

  it('选择初号机：落盘、同步 <html data-theme>、标记选中', async () => {
    const trigger = await openPicker()
    await clickThemeOption('初号机')
    expect(document.documentElement.dataset.theme).toBe('eva-01')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('eva-01')
    // 触发按钮的提示跟随当前主题
    expect(trigger.getAttribute('aria-label')).toBe('界面主题：初号机')

    // 再选浅色：同一个控件同时管明暗与机体色
    trigger.click()
    await flush()
    await clickThemeOption('浅色')
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
    expect(trigger.getAttribute('aria-label')).toBe('界面主题：浅色')
  })
})
