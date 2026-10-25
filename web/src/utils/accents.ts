import type { GlobalThemeOverrides } from 'naive-ui'

/**
 * 配色主题（accent）：与明暗（data-theme）正交的第二个主题维度。
 *
 * 明暗管「表面」（背景 / 卡片 / 文字 / 边框），accent 管「品牌色系」——
 * naive-ui 的 primary / error 族、菜单激活色，以及 CSS 侧的 --brand 系列变量、
 * 背景光晕、选区颜色。状态与持久化在 useTheme.ts（localStorage 键 any-llm-accent，
 * 以 <html data-accent="…"> 暴露给 CSS）。
 *
 * 同步约定（accents.test.ts 把关）：
 *   - 每个 accent 的 css 段取值必须与 style.css 的 :root[data-accent=…] 块、
 *     glass.css 的 accent 块一致（EVA 配色两侧共用同一组值；default 的 CSS 值
 *     内置于 style.css / glass.css，classic 与 glass 的默认蓝各有一套）；
 *   - default 的 naive-ui 值内置于 classic / glass 两套 theme.ts（明暗各有微调），
 *     不经这里；EVA 配色的 primary / error 族经 accentNaiveOverrides 注入，
 *     classic 与 glass 共用。
 */

export type AccentId = 'default' | 'eva-01' | 'eva-02' | 'eva-00'

export const ACCENT_IDS: AccentId[] = ['default', 'eva-01', 'eva-02', 'eva-00']

/** naive-ui 的一个语义色族（common 里的 *Color / *ColorHover / *ColorPressed / *ColorSuppl） */
interface ColorFamily {
  color: string
  hover: string
  pressed: string
  suppl: string
}

export interface AccentDef {
  id: AccentId
  /** 选择器里的显示名 */
  name: string
  /** 选择器圆点：主色 → 渐变副色的两色渐变 */
  swatch: [string, string]
  /** 主色 rgb 三元组，供 rgba() / color-mix 派生（激活底色、光晕等） */
  rgb: string
  /** naive-ui 主色族。白底对比度约束见 accents.test.ts（主色 ≥3:1，深档 ≥4.5:1） */
  primary: ColorFamily
  /** 仅当主色与语义 error 撞车时覆盖（明日香主题的红 → 品红）；undefined = 沿用内置 error 色 */
  error?: ColorFamily
  /** 菜单激活文字色：暗底用浅调、白底用深调 */
  menuDark: string
  menuLight: string
  /** CSS 变量取值（style.css / glass.css 的 accent 块与此同步） */
  css: {
    brand: string
    brand2: string
    /** 暗底下的悬停高亮 */
    hover: string
    /** 白底上的深一档（--brand-hover 的浅色模式取值） */
    hoverDeep: string
    /** 135° 品牌渐变（.text-grad / 品牌圆标） */
    grad: string
    /** 浅色模式渐变（淡色端在白底上不可见时换深一档） */
    gradLight: string
    /** body 背景两团品牌光晕（暗色 / 浅色各一组，浅色更收） */
    glow1: string
    glow2: string
    glow1Light: string
    glow2Light: string
    selection: string
    /** 品牌色 40% 左右的描边（按钮 hover 边框等） */
    border: string
  }
}

export const ACCENTS: AccentDef[] = [
  {
    id: 'default',
    name: '默认',
    swatch: ['#5b8cff', '#22d3ee'],
    rgb: '91, 140, 255',
    primary: { color: '#5b8cff', hover: '#7ba3ff', pressed: '#4673e8', suppl: '#7ba3ff' },
    menuDark: '#8fb0ff',
    menuLight: '#3b6fe0',
    css: {
      brand: '#5b8cff',
      brand2: '#22d3ee',
      hover: '#7ba3ff',
      hoverDeep: '#4673e8',
      grad: 'linear-gradient(135deg, #5b8cff 0%, #22d3ee 100%)',
      gradLight: 'linear-gradient(135deg, #5b8cff 0%, #22d3ee 100%)',
      glow1: 'rgba(91, 140, 255, 0.14)',
      glow2: 'rgba(34, 211, 238, 0.08)',
      glow1Light: 'rgba(91, 140, 255, 0.1)',
      glow2Light: 'rgba(34, 211, 238, 0.07)',
      selection: 'rgba(91, 140, 255, 0.35)',
      border: 'rgba(91, 140, 255, 0.4)',
    },
  },
  {
    // 初号机：紫 + 荧光绿，渐变即机体温色
    id: 'eva-01',
    name: '初号机',
    swatch: ['#9b6dff', '#a3e635'],
    rgb: '155, 109, 255',
    primary: { color: '#9b6dff', hover: '#b58cff', pressed: '#7d4fe0', suppl: '#b58cff' },
    menuDark: '#c9b1ff',
    menuLight: '#6d28d9',
    css: {
      brand: '#9b6dff',
      brand2: '#a3e635',
      hover: '#b58cff',
      hoverDeep: '#6d28d9',
      grad: 'linear-gradient(135deg, #9b6dff 0%, #a3e635 100%)',
      gradLight: 'linear-gradient(135deg, #9b6dff 0%, #84cc16 100%)',
      glow1: 'rgba(155, 109, 255, 0.14)',
      glow2: 'rgba(163, 230, 53, 0.07)',
      glow1Light: 'rgba(155, 109, 255, 0.1)',
      glow2Light: 'rgba(163, 230, 53, 0.05)',
      selection: 'rgba(155, 109, 255, 0.35)',
      border: 'rgba(155, 109, 255, 0.4)',
    },
  },
  {
    // 明日香（二号机）：红 + 橙。主色红与语义 error 撞车，error 族让位到品红
    id: 'eva-02',
    name: '明日香',
    swatch: ['#ef4444', '#fb923c'],
    rgb: '239, 68, 68',
    primary: { color: '#ef4444', hover: '#f87171', pressed: '#dc2626', suppl: '#f87171' },
    error: { color: '#ec4899', hover: '#f472b6', pressed: '#db2777', suppl: '#f472b6' },
    menuDark: '#fca5a5',
    menuLight: '#dc2626',
    css: {
      brand: '#ef4444',
      brand2: '#fb923c',
      hover: '#f87171',
      hoverDeep: '#dc2626',
      grad: 'linear-gradient(135deg, #ef4444 0%, #fb923c 100%)',
      gradLight: 'linear-gradient(135deg, #ef4444 0%, #fb923c 100%)',
      glow1: 'rgba(239, 68, 68, 0.13)',
      glow2: 'rgba(251, 146, 60, 0.08)',
      glow1Light: 'rgba(239, 68, 68, 0.09)',
      glow2Light: 'rgba(251, 146, 60, 0.06)',
      selection: 'rgba(239, 68, 68, 0.32)',
      border: 'rgba(239, 68, 68, 0.4)',
    },
  },
  {
    // 凌波丽（零号机）：淡蓝 + 苍黄。淡蓝在白底上不可作主色，primary 取中间调、浅色走深档
    id: 'eva-00',
    name: '凌波丽',
    swatch: ['#7dd3fc', '#fde68a'],
    rgb: '56, 168, 232',
    primary: { color: '#2f9bdc', hover: '#55b5ea', pressed: '#1f86c4', suppl: '#55b5ea' },
    menuDark: '#a8dcf5',
    menuLight: '#0369a1',
    css: {
      brand: '#2f9bdc',
      brand2: '#fcd34d',
      hover: '#55b5ea',
      hoverDeep: '#0369a1',
      grad: 'linear-gradient(135deg, #2f9bdc 0%, #fcd34d 100%)',
      gradLight: 'linear-gradient(135deg, #2f9bdc 0%, #eab308 100%)',
      glow1: 'rgba(56, 168, 232, 0.13)',
      glow2: 'rgba(252, 211, 77, 0.06)',
      glow1Light: 'rgba(56, 168, 232, 0.09)',
      glow2Light: 'rgba(252, 211, 77, 0.05)',
      selection: 'rgba(56, 168, 232, 0.3)',
      border: 'rgba(56, 168, 232, 0.45)',
    },
  },
]

export function accentById(id: AccentId): AccentDef {
  const a = ACCENTS.find((x) => x.id === id)
  if (!a) throw new Error(`未知配色：${id}`)
  return a
}

type CommonOverrides = Partial<NonNullable<GlobalThemeOverrides['common']>>

/**
 * naive-ui common 段的品牌色覆盖：default 返回空（两套 theme.ts 的内置值即默认蓝），
 * EVA 配色返回 primary 族（+ 明日香的 error 族），由 theme.ts 展开在 commonShared 之后。
 */
export function accentNaiveOverrides(id: AccentId): CommonOverrides {
  if (id === 'default') return {}
  const a = accentById(id)
  const out: CommonOverrides = {
    primaryColor: a.primary.color,
    primaryColorHover: a.primary.hover,
    primaryColorPressed: a.primary.pressed,
    primaryColorSuppl: a.primary.suppl,
  }
  if (a.error) {
    out.errorColor = a.error.color
    out.errorColorHover = a.error.hover
    out.errorColorPressed = a.error.pressed
    out.errorColorSuppl = a.error.suppl
  }
  return out
}

/**
 * 菜单激活色：default 返回 null（classic / glass 的默认蓝各有内置取值与透明度），
 * EVA 配色返回激活文字色与主色 rgb——itemColorActive 的 rgba 透明度仍由各套 theme.ts 自己定。
 */
export function accentMenu(
  id: AccentId,
  mode: 'dark' | 'light',
): { active: string; rgb: string } | null {
  if (id === 'default') return null
  const a = accentById(id)
  return { active: mode === 'dark' ? a.menuDark : a.menuLight, rgb: a.rgb }
}
