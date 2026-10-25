import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'
import { fitColumns, fitScrollX, useContainerWidth, UPSTREAM_FIT_COLUMNS, KEYS_FIT_COLUMNS } from './useTableFit'

/**
 * useTableFit 的行为契约：
 *   - fitColumns：容器够宽 → 全部理想宽度；变窄 → 按各列可压缩空间比例分摊缺口，
 *     每列夹在 [min, wide]；缺口再大 → 各列停在下限
 *   - fitScrollX：永不超过容器宽（不出横向滚动条的不变量）；容器宽未知 → 列宽总和
 *   - useContainerWidth：ResizeObserver 优先，缺失时退回 window resize；
 *     量不出布局（happy-dom）时保持 0，视图按理想宽度渲染；scope 停止即解绑
 */

const TOTAL_WIDE = UPSTREAM_FIT_COLUMNS.reduce((s, c) => s + c.wide, 0)
const TOTAL_MIN = UPSTREAM_FIT_COLUMNS.reduce((s, c) => s + c.min, 0)

function sum(w: Record<string, number>) {
  return Object.values(w).reduce((a, b) => a + b, 0)
}

describe('fitColumns', () => {
  it('容器宽度未知（0 / 负数）时全部取理想宽度', () => {
    for (const c of [0, -100]) {
      const w = fitColumns(c, UPSTREAM_FIT_COLUMNS)
      for (const col of UPSTREAM_FIT_COLUMNS) expect(w[col.key]).toBe(col.wide)
    }
  })

  it('容器足够宽时全部取理想宽度', () => {
    const w = fitColumns(TOTAL_WIDE + 500, UPSTREAM_FIT_COLUMNS)
    for (const col of UPSTREAM_FIT_COLUMNS) expect(w[col.key]).toBe(col.wide)
    expect(sum(w)).toBe(TOTAL_WIDE)
  })

  it('中间档：总和不超过容器宽，每列夹在 [min, wide] 之间', () => {
    const c = Math.floor((TOTAL_WIDE + TOTAL_MIN) / 2)
    const w = fitColumns(c, UPSTREAM_FIT_COLUMNS)
    expect(sum(w)).toBeLessThanOrEqual(c)
    for (const col of UPSTREAM_FIT_COLUMNS) {
      expect(w[col.key], col.key).toBeGreaterThanOrEqual(col.min)
      expect(w[col.key], col.key).toBeLessThanOrEqual(col.wide)
    }
  })

  it('操作列的可压缩空间最大：收窄时它让出的宽度最多（按钮换行是第一调节手段）', () => {
    const c = Math.floor((TOTAL_WIDE + TOTAL_MIN) / 2)
    const w = fitColumns(c, UPSTREAM_FIT_COLUMNS)
    const actionsGive = UPSTREAM_FIT_COLUMNS.find((col) => col.key === 'actions')!.wide - w.actions
    for (const col of UPSTREAM_FIT_COLUMNS) {
      expect(col.wide - w[col.key], col.key).toBeLessThanOrEqual(actionsGive)
    }
    expect(w.actions).toBeLessThan(440) // 容器一收窄，按钮就开始换行
  })

  it('缺口超过总压缩能力时各列停在下限（比例钳到 1）', () => {
    const w = fitColumns(TOTAL_MIN - 300, UPSTREAM_FIT_COLUMNS)
    for (const col of UPSTREAM_FIT_COLUMNS) expect(w[col.key]).toBe(col.min)
  })

  it('容器越窄列宽不增（单调，拖动窗口不会来回跳）', () => {
    let prev = fitColumns(TOTAL_WIDE, UPSTREAM_FIT_COLUMNS)
    for (let c = TOTAL_WIDE - 1; c >= TOTAL_MIN; c -= 137) {
      const w = fitColumns(c, UPSTREAM_FIT_COLUMNS)
      for (const col of UPSTREAM_FIT_COLUMNS) {
        expect(w[col.key], `${col.key}@${c}`).toBeLessThanOrEqual(prev[col.key])
      }
      prev = w
    }
  })
})

describe('fitScrollX', () => {
  it('永不超过容器宽度——不出横向滚动条的不变量，覆盖手机到宽屏', () => {
    for (const c of [320, 375, 640, 900, TOTAL_MIN, 1200, 1486, TOTAL_WIDE - 1, TOTAL_WIDE, 2500]) {
      expect(fitScrollX(fitColumns(c, UPSTREAM_FIT_COLUMNS), c), `container=${c}`).toBeLessThanOrEqual(c)
    }
  })

  it('容器宽度未知（<= 0）时退化为列宽总和', () => {
    const w = fitColumns(0, UPSTREAM_FIT_COLUMNS)
    expect(fitScrollX(w, 0)).toBe(sum(w))
  })
})

// API 密钥页的列宽规格（KEYS_FIT_COLUMNS）走同一套 fitColumns/fitScrollX，
// 这里把同样的不变量跑一遍——当初就是它没设列宽，fixed 布局把 Key 列压到
// 不足一个字符宽，38 位 key 逐字竖排把行高撑爆。
describe('KEYS_FIT_COLUMNS', () => {
  const KEYS_WIDE = KEYS_FIT_COLUMNS.reduce((s, c) => s + c.wide, 0)
  const KEYS_MIN = KEYS_FIT_COLUMNS.reduce((s, c) => s + c.min, 0)

  it('每列 min <= wide，且 key 列理想宽度足以完整显示 38 位 key', () => {
    for (const col of KEYS_FIT_COLUMNS) {
      expect(col.min, col.key).toBeLessThanOrEqual(col.wide)
      expect(col.min, col.key).toBeGreaterThan(0)
    }
    // 「all-sk-」+ 32 位 base62 在 12px 等宽下约 274px，加上 chip 内边距、
    // 复制按钮与单元格内边距，低于 300 就一定会截断完整 key
    expect(KEYS_FIT_COLUMNS.find((c) => c.key === 'key')!.wide).toBeGreaterThanOrEqual(300)
  })

  it('容器收窄时各列夹在 [min, wide]、总和不超过容器宽；scroll-x 永不出横向滚动条', () => {
    for (const c of [320, 640, 900, KEYS_MIN, 1200, 1486, KEYS_WIDE - 1, KEYS_WIDE, 2500]) {
      const w = fitColumns(c, KEYS_FIT_COLUMNS)
      expect(sum(w), `sum@${c}`).toBeLessThanOrEqual(Math.max(c, KEYS_MIN))
      for (const col of KEYS_FIT_COLUMNS) {
        expect(w[col.key], `${col.key}@${c}`).toBeGreaterThanOrEqual(col.min)
        expect(w[col.key], `${col.key}@${c}`).toBeLessThanOrEqual(col.wide)
      }
      expect(fitScrollX(w, c), `scrollX@${c}`).toBeLessThanOrEqual(c)
    }
  })

  it('缺口超过总压缩能力时各列停在下限', () => {
    const w = fitColumns(KEYS_MIN - 100, KEYS_FIT_COLUMNS)
    for (const col of KEYS_FIT_COLUMNS) expect(w[col.key]).toBe(col.min)
  })
})

class FakeRO {
  static instances: FakeRO[] = []
  disconnected = false
  private cb: (entries: Array<{ contentRect: { width: number } }>) => void
  constructor(cb: (entries: Array<{ contentRect: { width: number } }>) => void) {
    this.cb = cb
    FakeRO.instances.push(this)
  }
  observe() {}
  unobserve() {}
  disconnect() {
    this.disconnected = true
  }
  fire(w: number) {
    this.cb([{ contentRect: { width: w } }])
  }
}

describe('useContainerWidth', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    FakeRO.instances = []
  })

  it('有 ResizeObserver 时跟随观测回调更新，scope 停止即解绑', async () => {
    vi.stubGlobal('ResizeObserver', FakeRO)
    const el = ref<HTMLElement | null>(document.createElement('div'))
    const scope = effectScope()
    const { width } = scope.run(() => useContainerWidth(el))!
    await nextTick() // watch 是 flush: 'post'，初始观测在此落地
    expect(FakeRO.instances).toHaveLength(1)

    FakeRO.instances[0].fire(800)
    expect(width.value).toBe(800)
    FakeRO.instances[0].fire(0) // 量出 0 视为未知，保持旧值不抖动
    expect(width.value).toBe(800)

    scope.stop()
    expect(FakeRO.instances[0].disconnected).toBe(true)
  })

  it('没有 ResizeObserver 时退回 window resize；happy-dom 量不出布局则保持 0', async () => {
    vi.stubGlobal('ResizeObserver', undefined) // happy-dom 自带 RO，显式摘掉才能走回退分支
    const el = ref<HTMLElement | null>(document.createElement('div'))
    const scope = effectScope()
    const { width } = scope.run(() => useContainerWidth(el))!
    await nextTick()
    expect(width.value).toBe(0) // happy-dom 的 getBoundingClientRect 全是 0

    // 换成可量出宽度的环境（模拟真实浏览器）：resize 事件触发重测
    el.value!.getBoundingClientRect = () => ({ width: 640 }) as DOMRect
    window.dispatchEvent(new Event('resize'))
    expect(width.value).toBe(640)

    scope.stop()
    window.dispatchEvent(new Event('resize'))
    expect(width.value).toBe(640) // 解绑后不再更新
  })
})
