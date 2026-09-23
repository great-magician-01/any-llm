import { beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick } from 'vue'
import { ACTIONS_COL_WIDTH, NARROW_BREAKPOINT, useNarrowScreen } from './useNarrowScreen'

/**
 * useNarrowScreen 的行为契约：
 *   - 初值同步读一次（首帧就落在对的档位上）
 *   - 之后只认 matchMedia 的 change 事件，不挂 resize
 *   - 组件卸载（effect scope 失效）时解绑
 *   - 环境里没有 matchMedia 时按宽屏渲染
 *
 * 用 effectScope 包住调用：composable 里的 onScopeDispose 只在 scope 内注册，
 * 直接调用的话卸载解绑那条断言无从验证。
 */
describe('useNarrowScreen', () => {
  let listeners: Array<(e: { matches: boolean }) => void>
  let removed: number
  let current: { matches: boolean }
  const matchMedia = vi.fn()

  beforeEach(() => {
    listeners = []
    removed = 0
    current = { matches: false }
    matchMedia.mockImplementation((query: string) => ({
      get matches() {
        return current.matches
      },
      media: query,
      onchange: null,
      addEventListener: (_: string, fn: (e: { matches: boolean }) => void) => listeners.push(fn),
      // 真的摘掉：只计数的话「解绑后不再响应」这条断言就毫无意义
      removeEventListener: (_: string, fn: (e: { matches: boolean }) => void) => {
        removed++
        listeners = listeners.filter((l) => l !== fn)
      },
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }))
    window.matchMedia = matchMedia as unknown as typeof window.matchMedia
  })

  function fire(matches: boolean) {
    current = { matches }
    for (const fn of listeners) fn({ matches })
  }

  /** 在独立 effectScope 里调用，便于单独 stop 验证解绑。 */
  function mount() {
    const scope = effectScope()
    const { narrow } = scope.run(() => useNarrowScreen())!
    return { scope, narrow }
  }

  it('宽屏初值，断点写进 media query', () => {
    const { narrow } = mount()
    expect(narrow.value).toBe(false)
    expect(matchMedia).toHaveBeenCalledWith(`(max-width: ${NARROW_BREAKPOINT}px)`)
  })

  it('窄屏初值', () => {
    current = { matches: true }
    const { narrow } = mount()
    expect(narrow.value).toBe(true)
  })

  it('跨过断点时切换档位', async () => {
    const { narrow } = mount()
    expect(narrow.value).toBe(false)
    fire(true)
    await nextTick()
    expect(narrow.value).toBe(true)
    fire(false)
    await nextTick()
    expect(narrow.value).toBe(false)
  })

  it('scope 停止后解绑监听，后续事件不再改动 narrow', async () => {
    const { scope, narrow } = mount()
    scope.stop()
    expect(removed).toBe(1)
    fire(true)
    await nextTick()
    expect(narrow.value).toBe(false)
  })

  it('两档操作列宽：窄档必须窄于宽档，否则按钮不会换行', () => {
    expect(ACTIONS_COL_WIDTH.narrow).toBeLessThan(ACTIONS_COL_WIDTH.wide)
  })
})
