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
 * （EVA 的机体色本身就是深底，浅色版不做）——不只换品牌色：文字色阶、代码块底色、
 * 语义色族（success/warning/error 按机体配色让位）与样式层的阴影 / 滚动条 / 蜂窝
 * 纹理 / 警示条纹装饰一起换（装饰的取值在 style.css / glass.css 的机体主题块里）。
 *
 * 同步约定（themes.test.ts 把关）：
 *   - 每个主题的 css 段必须原样出现在 style.css 的对应主题块里；surface 段的
 *     表面 / 文字 / 代码块变量同样如此；glass.css 的机体主题块与品牌值一致；
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
 * 暗色主题的文字色阶。机体主题的文字不再是默认 slate 灰，而是各自机体色的
 * 染色版本（初号机偏紫、明日香偏暖红、凌波丽偏冷蓝），否则紫黑底上飘着一层
 * 蓝灰字，一眼就是「只改了背景」。四档与 CSS 的 --text / --text-2 / --text-3 /
 * --text-4 以及 naive 的 textColor1/2/3、placeholder 一一对应。
 */
export interface ThemeText {
  /** 标题 / 强调文字（--text、textColor1、菜单悬停） */
  strong: string
  /** 正文（--text-2、textColor2/base、表格正文） */
  base: string
  /** 次要文字（--text-3、textColor3、表头） */
  muted: string
  /** 最弱文字（--text-4、placeholder、菜单图标） */
  faint: string
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
  /** 代码 / 工具调用块底色（--code-bg，IrContent 等） */
  codeBg: string
  /** 文字色阶（--text 系列与 naive 文字色共用） */
  text: ThemeText
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
  /** 语义色让位 / 呼应机体配色的覆盖。undefined = 沿用 theme.ts 内置的默认语义色 */
  error?: ColorFamily
  success?: ColorFamily
  warning?: ColorFamily
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
  codeBg: 'rgba(2, 6, 18, 0.5)',
  text: { strong: '#f1f5f9', base: '#cbd5e1', muted: '#8fa0b8', faint: '#5b6b82' },
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
    // 初号机：紫甲 + 荧光绿。表面是紫黑机体色，文字也染紫；success 让位给机体的
    // 荧光绿（绿 = 机体系统正常的呼应色，压深到 lime-600 保按钮白字可读）
    id: 'eva-01',
    name: '初号机',
    hint: 'EVA-01 · 紫绿',
    mode: 'dark',
    swatch: ['#9b6dff', '#a3e635'],
    preview: '#080413',
    rgb: '155, 109, 255',
    primary: { color: '#9b6dff', hover: '#b58cff', pressed: '#7d4fe0', suppl: '#b58cff' },
    success: { color: '#65a30d', hover: '#84cc16', pressed: '#4d7c0f', suppl: '#84cc16' },
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
      bg: '#080413',
      card: '#130c26',
      surface2: '#181031',
      modal: '#150d2b',
      popover: '#1b1240',
      input: '#0e081d',
      sider: '#0c0718',
      tipBg: 'rgba(14, 9, 27, 0.94)',
      loginCardBg: 'rgba(19, 12, 38, 0.72)',
      codeBg: 'rgba(6, 3, 14, 0.55)',
      text: { strong: '#f2eeff', base: '#d6cdf0', muted: '#a396cc', faint: '#8a7ab8' },
      tint: '167, 139, 250',
    },
  },
  {
    // 明日香（二号机）：红甲 + 橙。主色红与语义 error 撞车，error 让位到品红；
    // warning 让位给机体的橙（压深到 orange-600 保白字可读）
    id: 'eva-02',
    name: '明日香',
    hint: 'EVA-02 · 红橙',
    mode: 'dark',
    swatch: ['#ef4444', '#fb923c'],
    preview: '#100407',
    rgb: '239, 68, 68',
    primary: { color: '#ef4444', hover: '#f87171', pressed: '#dc2626', suppl: '#f87171' },
    error: { color: '#ec4899', hover: '#f472b6', pressed: '#db2777', suppl: '#f472b6' },
    warning: { color: '#ea580c', hover: '#fb923c', pressed: '#c2410c', suppl: '#fb923c' },
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
      bg: '#100407',
      card: '#1e0b0f',
      surface2: '#250d13',
      modal: '#220d12',
      popover: '#291017',
      input: '#170708',
      sider: '#150609',
      tipBg: 'rgba(24, 9, 12, 0.94)',
      loginCardBg: 'rgba(30, 11, 15, 0.72)',
      codeBg: 'rgba(10, 3, 5, 0.55)',
      text: { strong: '#fdf1ed', base: '#ead6d0', muted: '#c29a94', faint: '#a47d76' },
      tint: '248, 113, 113',
    },
  },
  {
    // 凌波丽（零号机）：淡蓝 + 苍黄。淡蓝在白底上不可作主色，primary 取中间调；
    // warning 的默认琥珀黄本身就贴近机体的苍黄，不再覆盖
    id: 'eva-00',
    name: '凌波丽',
    hint: 'EVA-00 · 蓝黄',
    mode: 'dark',
    swatch: ['#7dd3fc', '#fde68a'],
    preview: '#030d15',
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
      bg: '#030d15',
      card: '#081825',
      surface2: '#0b1f2f',
      modal: '#091b2b',
      popover: '#0e2436',
      input: '#051220',
      sider: '#051119',
      tipBg: 'rgba(7, 18, 28, 0.94)',
      loginCardBg: 'rgba(8, 24, 37, 0.72)',
      codeBg: 'rgba(2, 8, 13, 0.55)',
      text: { strong: '#eef8ff', base: '#cfdfec', muted: '#97b3c9', faint: '#7894ac' },
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
 * 机体主题返回 primary 族 + 注册表里声明的语义色族（明日香的 error 品红、warning 橙，
 * 初号机的 success 荧光绿），由 theme.ts 展开在 commonShared 之后。
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
  for (const name of ['error', 'success', 'warning'] as const) {
    const family = t[name]
    if (!family) continue
    out[`${name}Color`] = family.color
    out[`${name}ColorHover`] = family.hover
    out[`${name}ColorPressed`] = family.pressed
    out[`${name}ColorSuppl`] = family.suppl
  }
  return out
}
