import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import AccentPicker from './AccentPicker.vue'
import { ACCENT_STORAGE_KEY, initTheme } from '@/composables/useTheme'
import { bodyHas, clickButton, flush, mountPage, type AnyWrapper } from '@/test/ui'

/**
 * 配色选择器：popover 内容 teleport 到 body，断言全部打在 document.body 上
 * （与 Upstreams 等页面测试同一套约定）。useTheme 是模块级单例，先 initTheme
 * 把 watcher 装好，setAccent 的落盘 / 同步行为才会生效。
 */

let wrapper: AnyWrapper | null = null

beforeEach(() => {
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
  document.documentElement.removeAttribute('data-accent')
  initTheme()
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
})

async function openPicker(): Promise<HTMLButtonElement> {
  wrapper = mountPage(AccentPicker)
  await flush()
  const trigger = document.querySelector('.accent-picker') as HTMLButtonElement
  if (!trigger) throw new Error('未找到配色选择器按钮')
  trigger.click()
  await flush()
  return trigger
}

describe('AccentPicker', () => {
  it('初始显示默认配色，菜单列出全部四个选项', async () => {
    const trigger = await openPicker()
    expect(trigger.getAttribute('aria-label')).toBe('配色主题：默认')
    for (const name of ['默认', '初号机', '明日香', '凌波丽']) {
      expect(bodyHas(name)).toBe(true)
    }
  })

  it('选择初号机：落盘、同步 <html data-accent>、标记选中', async () => {
    const trigger = await openPicker()
    await clickButton('初号机')
    expect(document.documentElement.dataset.accent).toBe('eva-01')
    expect(localStorage.getItem(ACCENT_STORAGE_KEY)).toBe('eva-01')
    // 触发按钮的提示跟随当前配色
    expect(trigger.getAttribute('aria-label')).toBe('配色主题：初号机')

    // 重新打开：初号机带选中态，切回默认也走同一套行为
    trigger.click()
    await flush()
    await clickButton('默认')
    expect(document.documentElement.dataset.accent).toBe('default')
    expect(localStorage.getItem(ACCENT_STORAGE_KEY)).toBe('default')
  })
})
