import { computed, ref, watch } from 'vue'
import { THEME_IDS, themeById, type ThemeId, type ThemeMode } from '@/utils/themes'

/**
 * 全局主题：单一维度（theme），一个主题同时决定明暗、品牌色和整套表面色。
 *
 * 解析顺序：localStorage 里已选的主题 > 旧版配色记录（迁移）> 系统 prefers-color-scheme
 * > 深色。选中的主题以 `<html data-theme="…">` 暴露给 CSS（见 style.css / glass.css），
 * 同时被 App.vue / glass/GlassShell.vue 用来切换 naive-ui 的 darkTheme / lightTheme。
 *
 * 主题没有「跟随系统」的语义，只有从未选择过时才跟随系统深/浅；一旦主动选过
 * （哪怕选的就是当前值）就落盘并停止跟随。旧版本的 `any-llm-accent` 键在启动
 * 迁移后删除——否则用户后来改主题时旧配色又会「复活」。
 *
 * 注意：index.html 里有一段等价的极简内联脚本，负责在首帧绘制前打好 data-theme，
 * 两处逻辑必须保持一致（内联脚本在打包前，无法 import 本模块）。
 */
export type { ThemeId, ThemeMode }

export const THEME_STORAGE_KEY = 'any-llm-theme'
/** 旧版正交的配色维度（default / eva-*），只在启动时用于迁移 */
export const LEGACY_ACCENT_STORAGE_KEY = 'any-llm-accent'

const theme = ref<ThemeId>('dark')
let watcherInstalled = false
let systemQuery: MediaQueryList | undefined
/** 是否为用户主动选择：只有主动选择才落盘并停止跟随系统。 */
let manualChoice = false

function isThemeId(v: string | null): v is ThemeId {
  return v !== null && (THEME_IDS as string[]).includes(v)
}

function readStoredTheme(): ThemeId | null {
  try {
    const v = localStorage.getItem(THEME_STORAGE_KEY)
    return isThemeId(v) ? v : null // 旧的 dark / light 值本身就是合法主题 id
  } catch {
    return null // 隐私模式下读不到 localStorage，退回默认
  }
}

/**
 * 旧版配色记录：只有 EVA 配色算得上一个独立主题（旧模型里它没有背景，只有品牌色）。
 * 旧模型的「明暗」两档在 EVA 上不再提供，统一落到机体主题的暗色皮肤。
 */
function readLegacyAccent(): ThemeId | null {
  try {
    const v = localStorage.getItem(LEGACY_ACCENT_STORAGE_KEY)
    return v === 'eva-01' || v === 'eva-02' || v === 'eva-00' ? v : null
  } catch {
    return null
  }
}

function clearLegacyAccent(): void {
  try {
    localStorage.removeItem(LEGACY_ACCENT_STORAGE_KEY)
  } catch {
    // 同上：无痕模式下没有可清理的记录
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
export function resolveInitialTheme(): ThemeId {
  return readLegacyAccent() ?? readStoredTheme() ?? (prefersLight() ? 'light' : 'dark')
}

function applyTheme(t: ThemeId) {
  document.documentElement.dataset.theme = t
}

function persistTheme(t: ThemeId) {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, t)
  } catch {
    // 忽略：无痕模式等场景下主题仅本次会话生效
  }
}

function onSystemThemeChange(e: MediaQueryListEvent) {
  theme.value = e.matches ? 'light' : 'dark'
}

/** 用户手选过一次就不再跟随系统，避免系统主题一变就被改回去。 */
function detachSystemListener() {
  systemQuery?.removeEventListener('change', onSystemThemeChange)
  systemQuery = undefined
}

/** main.ts 启动时调用一次：定好初值，并让后续变更同步到 <html> 与 localStorage。 */
export function initTheme(): void {
  const legacy = readLegacyAccent()
  const stored = readStoredTheme()
  const hadChoice = legacy !== null || stored !== null
  theme.value = legacy ?? stored ?? (prefersLight() ? 'light' : 'dark')
  applyTheme(theme.value)
  // 迁移只做一次：旧键删掉，同时把迁移结果写进新键——否则下次启动既没有旧记录
  // 也没有新记录，会退回系统偏好，用户选的机体主题就丢了。
  if (legacy !== null) persistTheme(legacy)
  clearLegacyAccent()

  // 没主动选过主题时跟随系统；选过之后由 setTheme 解绑
  if (!hadChoice && typeof window.matchMedia === 'function') {
    systemQuery = window.matchMedia('(prefers-color-scheme: light)')
    systemQuery.addEventListener('change', onSystemThemeChange)
  }

  if (watcherInstalled) return
  watcherInstalled = true
  watch(
    theme,
    (t) => {
      applyTheme(t)
      // 只有用户主动选择才落盘：否则系统偏好会被固化成一条本地记录，
      // 之后就再也跟随不了系统变化了（initTheme 的初始化变更同样不算）。
      if (!manualChoice) return
      persistTheme(t)
      detachSystemListener()
    },
    // 同步执行：改动只是给 <html> 加属性，同步应用才不会出现「按钮已切换但
    // 页面还是旧配色」的中间帧（默认的 pre flush 要等到下一个微任务）。
    { flush: 'sync' },
  )
}

export function useTheme() {
  return {
    theme,
    mode: computed<ThemeMode>(() => themeById(theme.value).mode),
    isDark: computed(() => themeById(theme.value).mode === 'dark'),
    setTheme(t: ThemeId) {
      manualChoice = true
      detachSystemListener()
      if (theme.value === t) {
        // 值与当前相同，watch 不会触发；显式落盘，否则「选了当前主题」
        // 在跟随系统的状态下等于什么都没发生。
        persistTheme(t)
      } else {
        theme.value = t
      }
    },
  }
}
