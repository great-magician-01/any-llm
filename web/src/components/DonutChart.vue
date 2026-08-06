<script setup lang="ts">
import { computed, ref } from 'vue'

export interface DonutSlice {
  name: string
  value: number
  color: string
}

const props = withDefaults(
  defineProps<{
    slices: DonutSlice[]
    size?: number
    thickness?: number
    /** 中心主文案（默认显示总计） */
    centerLabel?: string
    centerValue?: string
    formatValue?: (n: number) => string
  }>(),
  { size: 180, thickness: 22 },
)

const fmt = (n: number) => (props.formatValue ? props.formatValue(n) : String(n))

const total = computed(() => props.slices.reduce((a, s) => a + Math.max(0, s.value), 0))
const centerText = computed(() => props.centerValue ?? fmt(total.value))

const cx = computed(() => props.size / 2)
const r = computed(() => (props.size - props.thickness) / 2 - 2)
const circumference = computed(() => 2 * Math.PI * r.value)

interface Seg {
  key: number
  color: string
  dash: string
  offset: number
  frac: number
}
const segs = computed<Seg[]>(() => {
  const t = total.value
  if (t <= 0) return []
  // 极小扇区保留最小可见弧长，再按比例归一化
  const minArc = 2.5
  const raw = props.slices.map((s) => Math.max(0, s.value))
  let fracs = raw.map((v) => v / t)
  const tiny = fracs.filter((f) => f > 0 && f * circumference.value < minArc).length
  if (tiny > 0 && tiny < fracs.length) {
    const minFrac = minArc / circumference.value
    const boosted = fracs.map((f) => (f > 0 && f < minFrac ? minFrac : f))
    const sum = boosted.reduce((a, b) => a + b, 0)
    fracs = boosted.map((f) => f / sum)
  }
  const gap = fracs.filter((f) => f > 0).length > 1 ? 1.5 : 0
  const out: Seg[] = []
  let acc = 0
  fracs.forEach((f, i) => {
    if (f <= 0) return
    const arc = f * circumference.value
    out.push({
      key: i,
      color: props.slices[i].color,
      dash: `${Math.max(0.5, arc - gap)} ${circumference.value - Math.max(0.5, arc - gap)}`,
      offset: -(acc * circumference.value),
      frac: f,
    })
    acc += f
  })
  return out
})

const hover = ref<{ i: number; x: number; y: number } | null>(null)
function onEnter(i: number, e: MouseEvent) {
  const el = (e.currentTarget as SVGElement).closest('.donut-wrap') as HTMLElement
  const rect = el?.getBoundingClientRect()
  hover.value = { i, x: e.clientX - (rect?.left ?? 0), y: e.clientY - (rect?.top ?? 0) }
}
const tipStyle = computed(() => {
  if (!hover.value) return {}
  const flipX = hover.value.x > props.size * 0.55
  return flipX
    ? { right: `${props.size - hover.value.x + 8}px`, top: `${Math.max(4, hover.value.y - 10)}px` }
    : { left: `${hover.value.x + 8}px`, top: `${Math.max(4, hover.value.y - 10)}px` }
})
function pct(i: number): string {
  const t = total.value
  if (t <= 0) return '—'
  return ((Math.max(0, props.slices[i].value) / t) * 100).toFixed(1) + '%'
}
</script>

<template>
  <div class="donut-wrap" :style="{ width: size + 'px', height: size + 'px' }" @mouseleave="hover = null">
    <svg :width="size" :height="size" :viewBox="`0 0 ${size} ${size}`">
      <g :transform="`rotate(-90 ${cx} ${cx})`">
        <!-- 底环 -->
        <circle :cx="cx" :cy="cx" :r="r" fill="none" class="track" :stroke-width="thickness" />
        <circle
          v-for="s in segs"
          :key="s.key"
          :cx="cx"
          :cy="cx"
          :r="r"
          fill="none"
          :stroke="s.color"
          :stroke-width="hover && hover.i === s.key ? thickness + 4 : thickness"
          :stroke-dasharray="s.dash"
          :stroke-dashoffset="s.offset"
          :stroke-opacity="hover && hover.i !== s.key ? 0.3 : 1"
          class="seg"
          @mouseenter="onEnter(s.key, $event)"
        />
      </g>
      <text :x="cx" :y="cx - 4" text-anchor="middle" class="center-value">{{ centerText }}</text>
      <text :x="cx" :y="cx + 15" text-anchor="middle" class="center-label">{{ centerLabel ?? '总计' }}</text>
    </svg>
    <div v-if="hover" class="donut-tip" :style="tipStyle">
      <span class="tip-dot" :style="{ background: slices[hover.i].color }"></span>
      <span class="tip-name">{{ slices[hover.i].name }}</span>
      <span class="tip-val">{{ fmt(slices[hover.i].value) }} · {{ pct(hover.i) }}</span>
    </div>
  </div>
</template>

<style scoped>
.donut-wrap {
  position: relative;
  flex: none;
}
.track {
  stroke: rgba(148, 163, 184, 0.1);
}
.seg {
  transition:
    stroke-width 0.15s ease,
    stroke-opacity 0.15s ease;
  cursor: default;
}
.center-value {
  font-size: 17px;
  font-weight: 700;
  fill: var(--text);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
.center-label {
  font-size: 10.5px;
  fill: var(--text-4);
}
.donut-tip {
  position: absolute;
  pointer-events: none;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 9px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: rgba(10, 15, 26, 0.94);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(6px);
  font-size: 11.5px;
  white-space: nowrap;
  z-index: 5;
}
.tip-dot {
  width: 7px;
  height: 7px;
  border-radius: 2px;
  flex: none;
}
.tip-name {
  color: var(--text-3);
}
.tip-val {
  color: var(--text);
  font-weight: 600;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
</style>
