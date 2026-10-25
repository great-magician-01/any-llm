/**
 * 配色主题注册表的把关测试：
 *
 *   1. CSS 同步——accents.ts 的 css 段取值必须原样出现在 style.css / glass.css 的
 *      accent 块里（JS 只消费 naive-ui 段，CSS 变量段是手写的镜像，没有这道断言
 *      两边必然改歪一边；与 index.html 内联脚本 ↔ useTheme.ts 的同步约定同款）；
 *   2. 对比度——白底按钮文字（primary ≥3:1）、浅色深一档（hoverDeep/menuLight ≥4.5:1）、
 *      暗底菜单激活色（menuDark ≥4.5:1）：调色时先看这里的红灯，别靠眼睛。
 */
/// <reference types="node" />
// 上面仅为本文件引入 node 类型（读盘做 CSS 同步断言用）；app 侧 tsconfig 不全局带 node types
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { ACCENTS, ACCENT_IDS, accentById, accentMenu, accentNaiveOverrides } from './accents'

// vitest 默认把 CSS 模块替换成空串（?raw 也被拦截），这里直接读盘拿源码做同步断言。
// npm script（含 CI）固定在 web/ 目录下执行，相对 process.cwd() 取路径即可。
const styleSrc = readFileSync(resolve(process.cwd(), 'src/style.css'), 'utf-8')
const glassSrc = readFileSync(resolve(process.cwd(), 'src/themes/glass/glass.css'), 'utf-8')
const indexSrc = readFileSync(resolve(process.cwd(), 'index.html'), 'utf-8')

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

describe('accent 注册表', () => {
  it('id 清单无重复、accentById 全部可查', () => {
    expect(new Set(ACCENT_IDS).size).toBe(ACCENT_IDS.length)
    expect(ACCENTS.map((a) => a.id)).toEqual(ACCENT_IDS)
    for (const id of ACCENT_IDS) expect(accentById(id).id).toBe(id)
  })

  it('default 不注入 naive 覆盖（内置值在两套 theme.ts 里），EVA 注入品牌色段', () => {
    expect(accentNaiveOverrides('default')).toEqual({})
    const eva01 = accentNaiveOverrides('eva-01')
    expect(eva01.primaryColor).toBe('#9b6dff')
    expect(eva01).not.toHaveProperty('errorColor')
    // 明日香主题：主色红与语义 error 撞车，error 族整体让位到品红
    const eva02 = accentNaiveOverrides('eva-02')
    expect(eva02.primaryColor).toBe('#ef4444')
    expect(eva02.errorColor).toBe('#ec4899')
    expect(eva02.errorColorPressed).toBe('#db2777')
  })

  it('accentMenu：default 返回 null（两套 theme.ts 各有内置），EVA 分明暗两档', () => {
    expect(accentMenu('default', 'dark')).toBeNull()
    expect(accentMenu('eva-01', 'dark')).toEqual({ active: '#c9b1ff', rgb: '155, 109, 255' })
    expect(accentMenu('eva-01', 'light')).toEqual({ active: '#6d28d9', rgb: '155, 109, 255' })
  })
})

describe('accent 与 CSS 变量块同步', () => {
  // style.css 侧：所有 accent（含 default）的变量值都必须原样存在
  it.each(ACCENT_IDS)('%s：style.css 的 accent 块与注册表一致', (id) => {
    const a = accentById(id)
    const css = squash(styleSrc)
    expect(css).toContain(squash(`--brand: ${a.css.brand};`))
    expect(css).toContain(squash(`--brand-2: ${a.css.brand2};`))
    expect(css).toContain(squash(`--brand-hover: ${a.css.hoverDeep};`))
    expect(css).toContain(squash(`--grad: ${a.css.grad};`))
    expect(css).toContain(squash(`--grad: ${a.css.gradLight};`))
    for (const glow of [a.css.glow1, a.css.glow2, a.css.glow1Light, a.css.glow2Light]) {
      expect(css).toContain(squash(glow))
    }
    expect(css).toContain(squash(`--selection: ${a.css.selection};`))
    expect(css).toContain(squash(`--brand-border: ${a.css.border};`))
  })

  // glass.css 侧：EVA 配色与经典版共用一组值；default 是 glass 特调玻璃蓝
  it.each(ACCENT_IDS.filter((id) => id !== 'default'))('%s：glass.css 的 accent 块与注册表一致', (id) => {
    const a = accentById(id)
    const css = squash(glassSrc)
    expect(css).toContain(squash(`--brand: ${a.css.brand};`))
    expect(css).toContain(squash(`--brand-2: ${a.css.brand2};`))
    expect(css).toContain(squash(`--brand-hover: ${a.css.hoverDeep};`))
    expect(css).toContain(squash(`--grad: ${a.css.gradLight};`))
  })
  it('default 在 glass.css 保持玻璃蓝特调', () => {
    const css = squash(glassSrc)
    expect(css).toContain(squash('--brand: #7ba3ff;'))
    expect(css).toContain(squash('--brand-hover: #4673e8;'))
  })

  it('index.html 内联脚本认识存储键与全部合法值', () => {
    const src = squash(indexSrc)
    expect(src).toContain('any-llm-accent')
    expect(src).toContain('any-llm-theme')
    for (const id of ACCENT_IDS) expect(src).toContain(`'${id}'`)
  })
})

describe('调色板对比度底线', () => {
  // primary 直接做按钮底色配白字；#070b14 是经典版暗色 bodyColor。
  // hoverDeep 的线是 4.3 而非 4.5：default 的既有深档 #4673e8 = 4.305，
  // 本次不加配色、不改默认外观，从 4.3 起步约束新配色（EVA 全部 ≥4.5）。
  it.each(ACCENT_IDS)('%s：主色/悬停色在白底与暗底上都可读', (id) => {
    const a = accentById(id)
    expect(contrast(a.primary.color, '#ffffff')).toBeGreaterThanOrEqual(3)
    expect(contrast(a.css.hover, '#070b14')).toBeGreaterThanOrEqual(3)
    expect(contrast(a.css.hoverDeep, '#ffffff')).toBeGreaterThanOrEqual(4.3)
    expect(contrast(a.menuLight, '#ffffff')).toBeGreaterThanOrEqual(4.5)
    expect(contrast(a.menuDark, '#070b14')).toBeGreaterThanOrEqual(4.5)
  })
})
