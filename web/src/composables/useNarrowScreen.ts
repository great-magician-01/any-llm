import { onScopeDispose, readonly, ref } from 'vue'

/**
 * 视口宽度断点（两套皮肤的 Upstreams 页共用）。
 *
 * 上游管理页的操作列有 5 个按钮：宽屏一行铺开最顺手，窄屏还按一行算宽就把
 * 表格撑爆、横向滚动条拉老长。这里只做「宽/窄」两档，不逐像素重算——
 * 表格列宽是声明式配置，档位切换时重建一次 columns 即可，中间态没有意义。
 *
 * 两套皮肤各引一份自己的常量会让断点悄悄漂移（改一处忘一处），所以断点与
 * 两档列宽都收敛在这里。
 */

/** 窄于此宽度切到「按钮换行」档（视口宽度，含滚动条）。 */
export const NARROW_BREAKPOINT = 1400

/** 操作列宽度：宽屏一行 5 个按钮；窄屏收窄，按钮自然换行成两行。 */
export const ACTIONS_COL_WIDTH = { wide: 360, narrow: 250 } as const

function narrowQuery(): MediaQueryList | undefined {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return undefined // 测试环境（happy-dom）等没有 matchMedia：按宽屏渲染
  }
  return window.matchMedia(`(max-width: ${NARROW_BREAKPOINT}px)`)
}

/**
 * 视口是否窄于 NARROW_BREAKPOINT。
 *
 * 初值同步读一次（首帧就是对的），之后只监听 change 事件——不挂 resize，
 * 免得每次拖窗口都触发响应式更新。
 */
export function useNarrowScreen() {
  const query = narrowQuery()
  const narrow = ref(query?.matches ?? false)

  function onChange(e: MediaQueryListEvent) {
    narrow.value = e.matches
  }

  query?.addEventListener('change', onChange)
  // 组件卸载（或 effect scope 失效）时解绑：这两个页面会反复进出
  onScopeDispose(() => query?.removeEventListener('change', onChange))

  return { narrow: readonly(narrow) }
}
