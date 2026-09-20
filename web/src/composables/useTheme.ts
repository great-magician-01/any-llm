import { computed, ref, watch } from 'vue'

/**
 * 全局明暗主题。
 *
 * 单一数据源优先级：localStorage（用户手选）> 系统 prefers-color-scheme > 暗色。
 * 状态通过 `<html data-theme="light|dark">` 暴露给 CSS（见 style.css / glass/glass.css），
 * 同时被 App.vue / glass/GlassShell.vue 用来切换 naive-ui 的 darkTheme / lightTheme。
 *
 * 注意：index.html 里有一段等价的极简内联脚本，负责在首帧绘制前打好 data-theme，
 * 两处逻辑必须保持一致（内联脚本在打包前，无法 import 本模块）。
 */
export type ThemeMode = 'light' | 'dark'

export const THEME_STORAGE_KEY = 'any-llm-theme'

const mode = ref<ThemeMode>('dark')
let watcherInstalled = false
let systemQuery: MediaQueryList | undefined
/** 是否为用户主动切换：只有主动切换才落盘并停止跟随系统。 */
let manualChoice = false

function readStoredMode(): ThemeMode | null {
  try {
    const v = localStorage.getItem(THEME_STORAGE_KEY)
    return v === 'light' || v === 'dark' ? v : null
  } catch {
    return null // 隐私模式下读不到 localStorage，退回默认
  }
}

function prefersLight(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-color-scheme: light)').matches
  )
}

/** 与 index.html 内联脚本保持一致的解析顺序。 */
export function resolveInitialMode(): ThemeMode {
  return readStoredMode() ?? (prefersLight() ? 'light' : 'dark')
}

function apply(m: ThemeMode) {
  document.documentElement.dataset.theme = m
}

function onSystemThemeChange(e: MediaQueryListEvent) {
  mode.value = e.matches ? 'light' : 'dark'
}

/** 用户手选过一次就不再跟随系统，避免系统主题一变就被改回去。 */
function detachSystemListener() {
  systemQuery?.removeEventListener('change', onSystemThemeChange)
  systemQuery = undefined
}

/** main.ts 启动时调用一次：定好初值，并让后续变更同步到 <html> 与 localStorage。 */
export function initTheme(): void {
  mode.value = resolveInitialMode()
  apply(mode.value)

  // 没手动选过主题时跟随系统；user choice 之后由 watcher 里解绑
  if (!readStoredMode() && typeof window.matchMedia === 'function') {
    systemQuery = window.matchMedia('(prefers-color-scheme: light)')
    systemQuery.addEventListener('change', onSystemThemeChange)
  }

  if (watcherInstalled) return
  watcherInstalled = true
  watch(
    mode,
    (m) => {
      apply(m)
      // 只有用户主动切换才落盘：否则系统偏好会被固化成一条本地记录，
      // 之后就再也跟随不了系统变化了（initTheme 的初始化变更同样不算）。
      if (!manualChoice) return
      try {
        localStorage.setItem(THEME_STORAGE_KEY, m)
      } catch {
        // 忽略：无痕模式等场景下主题仅本次会话生效
      }
      detachSystemListener()
    },
    // 同步执行：改动只是给 <html> 加属性，同步应用才不会出现「按钮已切换但
    // 页面还是旧配色」的中间帧（默认的 pre flush 要等到下一个微任务）。
    { flush: 'sync' },
  )
}

export function useTheme() {
  return {
    mode,
    isDark: computed(() => mode.value === 'dark'),
    setMode(m: ThemeMode) {
      manualChoice = true
      mode.value = m
    },
    toggle() {
      manualChoice = true
      mode.value = mode.value === 'dark' ? 'light' : 'dark'
    },
  }
}
