import type { GlobalThemeOverrides } from 'naive-ui'

/**
 * 界面主题注册表：一个主题 = 明暗 + 品牌色系 + 整套表面色。
 *
 * 早期版本把「明暗」和「配色（accent）」做成两个正交维度：明暗管表面，配色只换
 * 品牌色，所以选了初号机背景仍是默认的 navy。合并后只有一个维度——用户选中的
 * 主题（<html data-theme="…">）同时决定：
 *   - mode：naive-ui 走 darkTheme 还是 lightTheme（App.vue / GlassShell.vue）；
 *   - 品牌色：primary / error 色族、菜单激活色、CSS 的 --brand 系列变量；
 *   - 表面色：body / 卡片 / 浮层 / 输入框 / 侧栏 / 描边 / 悬停层次，机体主题
 *     连背景及其光晕一起换（surface 字段，仅暗色主题需要）。
 *
 * 五个主题：深色 / 浅色是默认蓝的两档明暗，三个机体主题各自是一套完整的暗色皮肤
 * （EVA 的机体色本身就是深底，浅色版不做）。
 *
 * 同步约定（themes.test.ts 把关）：
 *   - 每个主题的 css 段必须原样出现在 style.css 的对应主题块里；surface 段的
 *     表面变量同样如此；glass.css 的机体主题块与品牌值一致；
 *   - index.html 的内联脚本在首帧前打 data-theme，合法值清单与 THEME_IDS 同步；
 *   - 页面与样式里不再出现旧的 data-accent 维度。
 */

export type ThemeId = 'dark' | 'light' | 'eva-01' | 'eva-02' | 'eva-00'

export type ThemeMode = 'dark' | 'light'

export const THEME_IDS: ThemeId[] = ['dark', 'light', 'eva-01', 'eva-02', 'eva-00']

/** naive-ui 的一个语义色族（common 里的 *Color / *ColorHover / *ColorPressed / *ColorSuppl） */
interface ColorFamily {
  color: string
  hover: string
  pressed: string
  suppl: string
}

/**
 * 暗色主题的表面调色板。tint 是描边 / 悬停 / 表头 / 斑马纹这些半透明层次的
 * 统一基底 rgb（"148, 163, 184"），alpha 档位在 surfaceVar()/naive 覆盖里统一
 * 派生，换主题只换这一个三元组。默认深色的 tint 与各档 alpha 就是原 navy 皮肤
 * 的取值，所以未选机体主题时渲染结果与合并前完全一致。
 */
export interface ThemeSurface {
  /** body 底色（--bg / bodyColor / Layout.color） */
  bg: string
  /** 卡片 / 表格 / 操作区表面（--surface / cardColor） */
  card: string
  /** 第二档表面：会话面板、卡片内嵌块（--surface-2，CSS 专用） */
  surface2: string
  /** Modal / Dialog 浮层 */
  modal: string
  /** Popover / Dropdown / 气泡浮层 */
  popover: string
  /** 输入框内凹表面 */
  input: string
  /** 侧栏底色 */
  sider: string
  /** 图表浮层底色（--tip-bg） */
  tipBg: string
  /** 登录卡底色（--login-card-bg） */
  loginCardBg: string
  /** 半透明层次的基底 rgb 三元组 */
  tint: string
}

export interface ThemeDef {
  id: ThemeId
  /** 选择器里的显示名 */
  name: string
  /** 一句话说明，选择器里做副标题 */
  hint: string
  /** naive-ui 明暗主题与 CSS 的明暗分支都由它决定 */
  mode: ThemeMode
  /** 选择器圆点：主色 → 渐变副色的两色渐变 */
  swatch: [string, string]
  /** 选择器缩略图的底色（与该主题 body 底色一致） */
  preview: string
  /** naive-ui 主色族。undefined = 沿用 classic / glass theme.ts 内置的默认蓝 */
  primary?: ColorFamily
  /** 仅当主色与语义 error 撞车时覆盖（明日香的红 → 品红） */
  error?: ColorFamily
  /** 主色 rgb 三元组，供菜单激活底色的 rgba 派生 */
  rgb: string
  /** 菜单激活文字色（对比度底线见 themes.test.ts） */
  menu: string
  /** CSS 变量取值（style.css / glass.css 的对应主题块与此同步） */
  css: {
    brand: string
    brand2: string
    /** 暗底下的悬停高亮（--brand-hover 的暗色取值） */
    hover: string
    /** 135° 品牌渐变（.text-grad / 品牌圆标） */
    grad: string
    /** body 背景两团品牌光晕 */
    glow1: string
    glow2: string
    selection: string
    /** 品牌色 40% 左右的描边（按钮 hover 边框等） */
    border: string
    /** 浅色主题专供：白底上的深一档悬停 */
    hoverDeep?: string
    /** 浅色主题专供：白底上收一档的光晕 */
    glow1Light?: string
    glow2Light?: string
  }
  /** 暗色主题的表面调色板；浅色主题的表面值内置在 style.css 的 light 块 */
  surface?: ThemeSurface
}

/** 深色默认主题的表面色：与合并前的 navy 皮肤逐值一致 */
const DARK_SURFACE: ThemeSurface = {
  bg: '#070b14',
  card: '#0e1526',
  surface2: '#111a2e',
  modal: '#101828',
  popover: '#141d33',
  input: '#0a101f',
  sider: '#0a0f1d',
  tipBg: 'rgba(10, 15, 26, 0.94)',
  loginCardBg: 'rgba(14, 21, 38, 0.72)',
  tint: '148, 163, 184',
}

export const THEMES: ThemeDef[] = [
  {
    id: 'dark',
    name: '深色',
    hint: '默认蓝 · 深空',
    mode: 'dark',
    swatch: ['#5b8cff', '#22d3ee'],
    preview: DARK_SURFACE.bg,
    rgb: '91, 140, 255',
    menu: '#8fb0ff',
    css: {
      brand: '#5b8cff',
      brand2: '#22d3ee',
      hover: '#7ba3ff',
      grad: 'linear-gradient(135deg, #5b8cff 0%, #22d3ee 100%)',
      glow1: 'rgba(91, 140, 255, 0.14)',
      glow2: 'rgba(34, 211, 238, 0.08)',
      selection: 'rgba(91, 140, 255, 0.35)',
      border: 'rgba(91, 140, 255, 0.4)',
    },
    surface: DARK_SURFACE,
  },
  {
    id: 'light',
    name: '浅色',
    hint: '默认蓝 · 冷白',
    mode: 'light',
    swatch: ['#5b8cff', '#22d3ee'],
    preview: '#f4f6fb',
    rgb: '91, 140, 255',
    menu: '#3b6fe0',
    css: {
      brand: '#5b8cff',
      brand2: '#22d3ee',
      hover: '#7ba3ff',
      grad: 'linear-gradient(135deg, #5b8cff 0%, #22d3ee 100%)',
      glow1: 'rgba(91, 140, 255, 0.14)',
      glow2: 'rgba(34, 211, 238, 0.08)',
      selection: 'rgba(91, 140, 255, 0.35)',
      border: 'rgba(91, 140, 255, 0.4)',
      hoverDeep: '#4673e8',
      glow1Light: 'rgba(91, 140, 255, 0.1)',
      glow2Light: 'rgba(34, 211, 238, 0.07)',
    },
  },
  {
    // 初号机：紫 + 荧光绿，渐变即机体温色，底色是紫黑
    id: 'eva-01',
    name: '初号机',
    hint: 'EVA-01 · 紫绿',
    mode: 'dark',
    swatch: ['#9b6dff', '#a3e635'],
    preview: '#0a0616',
    rgb: '155, 109, 255',
    primary: { color: '#9b6dff', hover: '#b58cff', pressed: '#7d4fe0', suppl: '#b58cff' },
    menu: '#c9b1ff',
    css: {
      brand: '#9b6dff',
      brand2: '#a3e635',
      hover: '#b58cff',
      grad: 'linear-gradient(135deg, #9b6dff 0%, #a3e635 100%)',
      glow1: 'rgba(155, 109, 255, 0.18)',
      glow2: 'rgba(163, 230, 53, 0.08)',
      selection: 'rgba(155, 109, 255, 0.35)',
      border: 'rgba(155, 109, 255, 0.4)',
    },
    surface: {
      bg: '#0a0616',
      card: '#150e2a',
      surface2: '#1a1234',
      modal: '#170f2e',
      popover: '#1d1440',
      input: '#100a20',
      sider: '#0d0820',
      tipBg: 'rgba(16, 10, 30, 0.94)',
      loginCardBg: 'rgba(21, 14, 42, 0.72)',
      tint: '167, 139, 250',
    },
  },
  {
    // 明日香（二号机）：红 + 橙。主色红与语义 error 撞车，error 族让位到品红
    id: 'eva-02',
    name: '明日香',
    hint: 'EVA-02 · 红橙',
    mode: 'dark',
    swatch: ['#ef4444', '#fb923c'],
    preview: '#140608',
    rgb: '239, 68, 68',
    primary: { color: '#ef4444', hover: '#f87171', pressed: '#dc2626', suppl: '#f87171' },
    error: { color: '#ec4899', hover: '#f472b6', pressed: '#db2777', suppl: '#f472b6' },
    menu: '#fca5a5',
    css: {
      brand: '#ef4444',
      brand2: '#fb923c',
      hover: '#f87171',
      grad: 'linear-gradient(135deg, #ef4444 0%, #fb923c 100%)',
      glow1: 'rgba(239, 68, 68, 0.16)',
      glow2: 'rgba(251, 146, 60, 0.09)',
      selection: 'rgba(239, 68, 68, 0.32)',
      border: 'rgba(239, 68, 68, 0.4)',
    },
    surface: {
      bg: '#140608',
      card: '#220d12',
      surface2: '#291017',
      modal: '#261015',
      popover: '#2d1219',
      input: '#1b090c',
      sider: '#18080b',
      tipBg: 'rgba(28, 10, 14, 0.94)',
      loginCardBg: 'rgba(34, 13, 18, 0.72)',
      tint: '248, 113, 113',
    },
  },
  {
    // 凌波丽（零号机）：淡蓝 + 苍黄。淡蓝在白底上不可作主色，primary 取中间调
    id: 'eva-00',
    name: '凌波丽',
    hint: 'EVA-00 · 蓝黄',
    mode: 'dark',
    swatch: ['#7dd3fc', '#fde68a'],
    preview: '#04101a',
    rgb: '56, 168, 232',
    primary: { color: '#2f9bdc', hover: '#55b5ea', pressed: '#1f86c4', suppl: '#55b5ea' },
    menu: '#a8dcf5',
    css: {
      brand: '#2f9bdc',
      brand2: '#fcd34d',
      hover: '#55b5ea',
      grad: 'linear-gradient(135deg, #2f9bdc 0%, #fcd34d 100%)',
      glow1: 'rgba(56, 168, 232, 0.17)',
      glow2: 'rgba(252, 211, 77, 0.07)',
      selection: 'rgba(56, 168, 232, 0.3)',
      border: 'rgba(56, 168, 232, 0.45)',
    },
    surface: {
      bg: '#04101a',
      card: '#0a1a29',
      surface2: '#0d2133',
      modal: '#0b1d2e',
      popover: '#0f2639',
      input: '#071523',
      sider: '#061420',
      tipBg: 'rgba(8, 19, 31, 0.94)',
      loginCardBg: 'rgba(10, 26, 41, 0.72)',
      tint: '125, 211, 252',
    },
  },
]

export function themeById(id: ThemeId): ThemeDef {
  const t = THEMES.find((x) => x.id === id)
  if (!t) throw new Error(`未知主题：${id}`)
  return t
}

/** 主题的表面调色板：未选机体主题时退回默认深色（浅色主题自己的值在 theme.ts 里） */
export function surfaceOf(id: ThemeId): ThemeSurface {
  return themeById(id).surface ?? DARK_SURFACE
}

/** 半透明层次的统一格式（与 hand-written CSS 的书写保持一致，测试按此同步） */
export function rgba(tint: string, alpha: number): string {
  return `rgba(${tint}, ${alpha})`
}

type CommonOverrides = Partial<NonNullable<GlobalThemeOverrides['common']>>

/**
 * naive-ui common 段的品牌色覆盖：默认蓝主题返回空（两套 theme.ts 的内置值即默认蓝），
 * 机体主题返回 primary 族（+ 明日香的 error 族），由 theme.ts 展开在 commonShared 之后。
 */
export function themeNaiveOverrides(id: ThemeId): CommonOverrides {
  const t = themeById(id)
  if (!t.primary) return {}
  const out: CommonOverrides = {
    primaryColor: t.primary.color,
    primaryColorHover: t.primary.hover,
    primaryColorPressed: t.primary.pressed,
    primaryColorSuppl: t.primary.suppl,
  }
  if (t.error) {
    out.errorColor = t.error.color
    out.errorColorHover = t.error.hover
    out.errorColorPressed = t.error.pressed
    out.errorColorSuppl = t.error.suppl
  }
  return out
}
