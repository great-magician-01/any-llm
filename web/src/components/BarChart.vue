<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

export interface BarSeries {
  name: string
  color: string
  values: number[]
}

const props = withDefaults(
  defineProps<{
    labels: string[]
    series: BarSeries[]
    /** stacked（堆叠）或 grouped（并排） */
    mode?: 'stacked' | 'grouped'
    height?: number
    /** 纵轴与提示框中的数值格式化 */
    formatValue?: (n: number) => string
    /** 附加绘制的折线（如成功率），数值域 [lineMin, lineMax] */
    line?: { name: string; color: string; values: (number | null)[] }
    lineMin?: number
    lineMax?: number
    formatLine?: (n: number) => string
  }>(),
  { mode: 'stacked', height: 260, lineMin: 0, lineMax: 100 },
)

const fmt = (n: number) => (props.formatValue ? props.formatValue(n) : String(n))

const wrap = ref<HTMLElement>()
const width = ref(560)
let ro: ResizeObserver | undefined
onMounted(() => {
  if (wrap.value) {
    width.value = wrap.value.clientWidth || width.value
    ro = new ResizeObserver((es) => {
      const w = es[0]?.contentRect.width
      if (w) width.value = w
    })
    ro.observe(wrap.value)
  }
})
onBeforeUnmount(() => ro?.disconnect())

const padL = 46
const padR = computed(() => (props.line ? 46 : 12))
const padT = 14
const padB = 26

const plotW = computed(() => Math.max(10, width.value - padL - padR.value))
const plotH = computed(() => Math.max(10, props.height - padT - padB))

const totals = computed(() =>
  props.labels.map((_, i) =>
    props.series.reduce((a, s) => a + Math.max(0, s.values[i] ?? 0), 0),
  ),
)
const maxTotal = computed(() => Math.max(...totals.value, 0))

// 紧凑纵轴刻度：1/2/2.5/5 × 10^k
function niceStep(max: number, target: number): number {
  if (max <= 0) return 1
  const rough = max / target
  const pow = Math.pow(10, Math.floor(Math.log10(rough)))
  for (const m of [1, 2, 2.5, 5, 10]) {
    if (m * pow >= rough) return m * pow
  }
  return 10 * pow
}
const yStep = computed(() => niceStep(maxTotal.value, 4))
const yMax = computed(() => Math.max(yStep.value, Math.ceil(maxTotal.value / yStep.value) * yStep.value))
const yTicks = computed(() => {
  const t: number[] = []
  for (let v = 0; v <= yMax.value + 1e-9; v += yStep.value) t.push(Math.round(v * 100) / 100)
  return t
})

const y = (v: number) => padT + plotH.value - (Math.max(0, v) / yMax.value) * plotH.value

const slotW = computed(() => plotW.value / Math.max(1, props.labels.length))
const groupW = computed(() => Math.min(56, slotW.value * 0.62))
const barGap = 3
const barW = computed(() => {
  if (props.mode === 'stacked') return groupW.value
  const n = Math.max(1, props.series.length)
  return Math.max(2, (groupW.value - barGap * (n - 1)) / n)
})
const groupX = (i: number) => padL + slotW.value * i + (slotW.value - groupW.value) / 2
const barX = (i: number, si: number) =>
  props.mode === 'stacked' ? groupX(i) : groupX(i) + si * (barW.value + barGap)

const labelEvery = computed(() => Math.max(1, Math.ceil(props.labels.length / Math.max(1, Math.floor(plotW.value / 52)))))

// 顶部圆角矩形（最后 1px 用直角，避免细柱形变）
function topRoundRect(x: number, yTop: number, w: number, h: number): string {
  const r = Math.min(3, w / 2)
  if (h <= 1) return `M${x} ${yTop}h${w}v${h}h${-w}z`
  const rr = Math.min(r, h / 2)
  return `M${x} ${yTop + h}v-${h - rr}a${rr} ${rr} 0 0 1 ${rr} ${-rr}h${w - 2 * rr}a${rr} ${rr} 0 0 1 ${rr} ${rr}v${h - rr}z`
}

interface BarRect {
  key: string
  i: number
  si: number
  x: number
  y: number
  w: number
  h: number
  color: string
}
const rects = computed<BarRect[]>(() => {
  const out: BarRect[] = []
  for (let i = 0; i < props.labels.length; i++) {
    let base = 0
    props.series.forEach((s, si) => {
      const v = Math.max(0, s.values[i] ?? 0)
      if (v <= 0) return
      const h = Math.max(1.5, (v / yMax.value) * plotH.value)
      const yTop = props.mode === 'stacked' ? y(base + v) : y(v)
      out.push({ key: `${si}:${i}`, i, si, x: barX(i, si), y: yTop, w: barW.value, h, color: s.color })
      base += v
    })
  }
  return out
})

const linePts = computed(() => {
  if (!props.line) return []
  const span = props.lineMax - props.lineMin || 1
  return props.line.values
    .map((v, i) => {
      if (v == null) return null
      const cx = padL + slotW.value * i + slotW.value / 2
      const cy = padT + plotH.value - ((v - props.lineMin) / span) * plotH.value
      return { x: cx, y: cy, i }
    })
    .filter(Boolean) as { x: number; y: number; i: number }[]
})
const linePath = computed(() =>
  linePts.value.length ? 'M' + linePts.value.map((p) => `${p.x} ${p.y}`).join('L') : '',
)
const lineTicks = computed(() => {
  if (!props.line) return []
  const mid = props.lineMin + (props.lineMax - props.lineMin) / 2
  return [
    { v: props.lineMin, y: padT + plotH.value },
    { v: mid, y: padT + plotH.value / 2 },
    { v: props.lineMax, y: padT },
  ]
})

// 悬浮提示
const hover = ref<{ i: number; x: number; y: number } | null>(null)
function onMove(e: MouseEvent) {
  const el = wrap.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const mx = e.clientX - rect.left
  const my = e.clientY - rect.top
  if (mx < padL || mx > width.value - padR.value || my < padT || my > padT + plotH.value) {
    hover.value = null
    return
  }
  const i = Math.min(props.labels.length - 1, Math.max(0, Math.floor((mx - padL) / slotW.value)))
  hover.value = { i, x: mx, y: my }
}
const tipStyle = computed(() => {
  if (!hover.value) return {}
  const i = hover.value.i
  const cx = padL + slotW.value * i + slotW.value / 2
  const flip = cx > width.value * 0.62
  const top = Math.max(6, padT + 14)
  return flip
    ? { right: `${width.value - cx + 10}px`, top: `${top}px` }
    : { left: `${cx + 10}px`, top: `${top}px` }
})
const guideX = computed(() =>
  hover.value ? padL + slotW.value * hover.value.i + slotW.value / 2 : 0,
)
const dayTotal = computed(() => (hover.value ? totals.value[hover.value.i] : 0))
const hasTotalRow = computed(() => props.series.length > 1 && props.mode === 'stacked')
</script>

<template>
  <div ref="wrap" class="chart-wrap" :style="{ height: height + 'px' }" @mousemove="onMove" @mouseleave="hover = null">
    <svg :width="width" :height="height" class="chart-svg">
      <!-- 网格与纵轴 -->
      <g v-for="t in yTicks" :key="'y' + t">
        <line :x1="padL" :x2="width - padR" :y1="y(t)" :y2="y(t)" class="grid-line" />
        <text :x="padL - 8" :y="y(t) + 3.5" text-anchor="end" class="tick">{{ fmt(t) }}</text>
      </g>
      <!-- 横轴标签 -->
      <text
        v-for="(lb, i) in labels"
        v-show="i % labelEvery === 0"
        :key="'x' + i"
        :x="padL + slotW * i + slotW / 2"
        :y="height - 8"
        text-anchor="middle"
        class="tick"
      >{{ lb }}</text>
      <!-- 数据柱 -->
      <path
        v-for="r in rects"
        :key="r.key"
        :d="topRoundRect(r.x, r.y, r.w, r.h)"
        :fill="r.color"
        :fill-opacity="hover && hover.i !== r.i ? 0.35 : mode === 'grouped' && r.si > 0 ? 0.85 : 1"
      />
      <!-- 折线叠加 -->
      <template v-if="line">
        <path :d="linePath" fill="none" :stroke="line.color" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
        <circle
          v-for="p in linePts"
          :key="'p' + p.i"
          :cx="p.x"
          :cy="p.y"
          :r="hover && hover.i === p.i ? 4 : 2.5"
          :fill="line.color"
        />
        <text v-for="t in lineTicks" :key="'l' + t.v" :x="width - padR + 8" :y="t.y + 3.5" class="tick">
          {{ formatLine ? formatLine(t.v) : t.v }}
        </text>
      </template>
      <!-- 悬浮参考线 -->
      <line
        v-if="hover"
        :x1="guideX"
        :x2="guideX"
        :y1="padT"
        :y2="padT + plotH"
        class="guide-line"
      />
    </svg>
    <div v-if="hover" class="chart-tip" :style="tipStyle">
      <div class="tip-title">{{ labels[hover.i] }}</div>
      <div v-for="s in series" :key="s.name" class="tip-row">
        <span class="tip-dot" :style="{ background: s.color }"></span>
        <span class="tip-name">{{ s.name }}</span>
        <span class="tip-val">{{ fmt(s.values[hover.i] ?? 0) }}</span>
      </div>
      <div v-if="line" class="tip-row">
        <span class="tip-dot" :style="{ background: line.color }"></span>
        <span class="tip-name">{{ line.name }}</span>
        <span class="tip-val">{{ line.values[hover.i] == null ? '—' : (formatLine ? formatLine(line.values[hover.i]!) : line.values[hover.i]) }}</span>
      </div>
      <div v-if="hasTotalRow" class="tip-row tip-total">
        <span class="tip-name">合计</span>
        <span class="tip-val">{{ fmt(dayTotal) }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.chart-wrap {
  position: relative;
  width: 100%;
}
.chart-svg {
  display: block;
}
.grid-line {
  stroke: var(--border-soft);
  stroke-width: 1;
}
.guide-line {
  stroke: rgba(148, 163, 184, 0.35);
  stroke-dasharray: 3 3;
}
.tick {
  font-size: 10.5px;
  fill: var(--text-4);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
path {
  transition: fill-opacity 0.15s ease;
}
.chart-tip {
  position: absolute;
  pointer-events: none;
  min-width: 132px;
  padding: 8px 10px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: rgba(10, 15, 26, 0.94);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(6px);
  z-index: 5;
}
.tip-title {
  font-size: 11.5px;
  font-weight: 600;
  color: var(--text-2);
  margin-bottom: 5px;
}
.tip-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  line-height: 1.7;
}
.tip-dot {
  width: 7px;
  height: 7px;
  border-radius: 2px;
  flex: none;
}
.tip-name {
  color: var(--text-3);
  flex: 1;
}
.tip-val {
  color: var(--text);
  font-weight: 600;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
.tip-total {
  margin-top: 3px;
  padding-top: 4px;
  border-top: 1px solid var(--border-soft);
}
</style>
