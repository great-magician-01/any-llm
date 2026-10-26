/**
 * 界面主题注册表的把关测试：
 *
 *   1. CSS 同步——themes.ts 的 css / surface 段取值必须原样出现在 style.css 的
 *      对应主题块里，glass.css 的机体主题块品牌值与注册表一致（CSS 变量段是手写
 *      的镜像，没有这道断言两边必然改歪一边；与 index.html 内联脚本 ↔ useTheme.ts
 *      的同步约定同款）；
 *   2. 结构——五个主题、只有浅色主题带 light 明暗，机体主题都带完整表面调色板，
 *      旧的 data-accent 维度不再出现；
 *   3. 对比度——主色配白字（≥3:1）、菜单激活色对主题底色（≥4.5:1）、悬停色
 *      对主题底色（≥3:1）：调色时先看这里的红灯，别靠眼睛。
 */
/// <reference types="node" />
// 上面仅为本文件引入 node 类型（读盘做 CSS 同步断言用）；app 侧 tsconfig 不全局带 node types
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { rgba, THEMES, THEME_IDS, themeById, themeNaiveOverrides, type ThemeId } from './themes'

// vitest 默认把 CSS 模块替换成空串（?raw 也被拦截），这里直接读盘拿源码做同步断言。
// npm script（含 CI）固定在 web/ 目录下执行，相对 process.cwd() 取路径即可。
const styleSrc = readFileSync(resolve(process.cwd(), 'src/style.css'), 'utf-8')
const glassSrc = readFileSync(resolve(process.cwd(), 'src/themes/glass/glass.css'), 'utf-8')
const indexSrc = readFileSync(resolve(process.cwd(), 'index.html'), 'utf-8')

/** 三个机体主题：都是暗色皮肤，整块换表面色与玻璃流光背景 */
const EVA_THEME_IDS: ThemeId[] = ['eva-01', 'eva-02', 'eva-00']

/** 去掉全部空白再比对：CSS 里值可以随意换行，语义由非空白序列决定 */
function squash(s: string): string {
  return s.replace(/\s+/g, '')
}

function luminance(hex: string): number {
  const m = hex.replace('#', '')
  if (!/^[0-9a-fA-F]{6}$/.test(m)) throw new Error(`断言辅助只收 6 位 hex，收到 ${hex}`)
  const lin = (c: number) => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4))
  const [r, g, b] = [0, 2, 4].map((i) => lin(parseInt(m.slice(i, i + 2), 16) / 255))
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrast(a: string, b: string): number {
  const [l1, l2] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (l1 + 0.05) / (l2 + 0.05)
}

describe('主题注册表', () => {
  it('五个主题：默认蓝的深/浅两档 + 三个机体主题', () => {
    expect(THEME_IDS).toEqual(['dark', 'light', 'eva-01', 'eva-02', 'eva-00'])
    expect(THEMES.map((t) => t.id)).toEqual(THEME_IDS)
    expect(new Set(THEME_IDS).size).toBe(THEME_IDS.length)
    for (const id of THEME_IDS) expect(themeById(id).id).toBe(id)
  })

  it('浅色只有默认蓝一档，其余都是暗色且带完整表面调色板', () => {
    expect(THEMES.filter((t) => t.mode === 'light').map((t) => t.id)).toEqual(['light'])
    for (const t of THEMES) {
      if (t.mode === 'light') {
        expect(t.surface).toBeUndefined()
      } else {
        expect(t.surface?.bg).toMatch(/^#[0-9a-f]{6}$/)
        expect(t.surface?.tint).toMatch(/^\d{1,3}, \d{1,3}, \d{1,3}$/)
        expect(t.preview).toBe(t.surface?.bg)
      }
    }
  })

  it('默认蓝不注入 naive 覆盖（内置值在 theme.ts 里），机体主题注入品牌色段', () => {
    expect(themeNaiveOverrides('dark')).toEqual({})
    expect(themeNaiveOverrides('light')).toEqual({})
    const eva01 = themeNaiveOverrides('eva-01')
    expect(eva01.primaryColor).toBe('#9b6dff')
    expect(eva01).not.toHaveProperty('errorColor')
    // 明日香主题：主色红与语义 error 撞车，error 族整体让位到品红
    const eva02 = themeNaiveOverrides('eva-02')
    expect(eva02.primaryColor).toBe('#ef4444')
    expect(eva02.errorColor).toBe('#ec4899')
    expect(eva02.errorColorPressed).toBe('#db2777')
  })

  it('rgba 助手与 CSS 书写格式一致', () => {
    expect(rgba('148, 163, 184', 0.1)).toBe('rgba(148, 163, 184, 0.1)')
  })
})

describe('主题与 CSS 变量块同步', () => {
  // style.css 侧：每个主题的品牌色变量都必须原样存在
  it.each(THEME_IDS)('%s：style.css 的品牌变量与注册表一致', (id) => {
    const a = themeById(id)
    const css = squash(styleSrc)
    expect(css).toContain(squash(`--brand: ${a.css.brand};`))
    expect(css).toContain(squash(`--brand-2: ${a.css.brand2};`))
    expect(css).toContain(squash(`--brand-hover: ${a.css.hover};`))
    expect(css).toContain(squash(`--grad: ${a.css.grad};`))
    expect(css).toContain(squash(`--bg-glow-1: ${a.css.glow1};`))
    expect(css).toContain(squash(`--bg-glow-2: ${a.css.glow2};`))
    expect(css).toContain(squash(`--selection: ${a.css.selection};`))
    expect(css).toContain(squash(`--brand-border: ${a.css.border};`))
    if (a.css.hoverDeep) expect(css).toContain(squash(`--brand-hover: ${a.css.hoverDeep};`))
    if (a.css.glow1Light) expect(css).toContain(squash(`--bg-glow-1: ${a.css.glow1Light};`))
    if (a.css.glow2Light) expect(css).toContain(squash(`--bg-glow-2: ${a.css.glow2Light};`))
  })

  // 机体主题的表面变量：背景 / 卡片 / 描边 / 浮层一起换，缺一项就会有一块残留在 navy
  it.each(EVA_THEME_IDS)('%s：style.css 的整套表面变量与注册表一致', (id) => {
    const s = themeById(id).surface!
    const css = squash(styleSrc)
    expect(css).toContain(squash(`:root[data-theme='${id}']`))
    expect(css).toContain(squash(`--bg: ${s.bg};`))
    expect(css).toContain(squash(`--surface: ${s.card};`))
    expect(css).toContain(squash(`--surface-2: ${s.surface2};`))
    expect(css).toContain(squash(`--border: ${rgba(s.tint, 0.18)};`))
    expect(css).toContain(squash(`--border-soft: ${rgba(s.tint, 0.1)};`))
    expect(css).toContain(squash(`--tip-bg: ${s.tipBg};`))
    expect(css).toContain(squash(`--login-card-bg: ${s.loginCardBg};`))
    expect(css).toContain(squash(`--overlay-1: ${rgba(s.tint, 0.1)};`))
    expect(css).toContain(squash(`--overlay-2: ${rgba(s.tint, 0.12)};`))
  })

  // glass.css 侧：机体主题的品牌值与流光背景块
  it.each(EVA_THEME_IDS)('%s：glass.css 的机体主题块与注册表一致', (id) => {
    const a = themeById(id)
    const css = squash(glassSrc)
    expect(css).toContain(squash(`:root[data-theme='${id}'] .glass-root`))
    expect(css).toContain(squash(`--brand: ${a.css.brand};`))
    expect(css).toContain(squash(`--brand-2: ${a.css.brand2};`))
    expect(css).toContain(squash(`--brand-hover: ${a.css.hover};`))
    expect(css).toContain(squash(`--grad: ${a.css.grad};`))
  })
  it('default 在 glass.css 保持玻璃蓝特调', () => {
    const css = squash(glassSrc)
    expect(css).toContain(squash('--brand: #7ba3ff;'))
    expect(css).toContain(squash('--brand-hover: #4673e8;'))
  })

  it('旧的 data-accent 维度已移除（主题合并后只剩 data-theme）', () => {
    for (const src of [styleSrc, glassSrc, indexSrc]) expect(src).not.toContain('data-accent')
  })

  it('index.html 内联脚本认识主题存储键、旧配色键与全部合法值', () => {
    const src = squash(indexSrc)
    expect(src).toContain('any-llm-theme')
    expect(src).toContain('any-llm-accent')
    for (const id of THEME_IDS) expect(src).toContain(`'${id}'`)
  })
})

describe('调色板对比度底线', () => {
  // 主色直接做按钮底色配白字；菜单激活色落在卡片上（浅色主题按白底算）；
  // 悬停色在浅色主题用深一档的 hoverDeep。
  it.each(THEME_IDS)('%s：主色 / 菜单激活色 / 悬停色都可读', (id) => {
    const t = themeById(id)
    const onWhite = t.mode === 'light'
    const base = onWhite ? '#ffffff' : t.surface!.bg
    expect(contrast(t.primary?.color ?? '#5b8cff', '#ffffff')).toBeGreaterThanOrEqual(3)
    expect(contrast(t.menu, base)).toBeGreaterThanOrEqual(4.5)
    expect(contrast(onWhite ? t.css.hoverDeep! : t.css.hover, base)).toBeGreaterThanOrEqual(3)
  })
})
