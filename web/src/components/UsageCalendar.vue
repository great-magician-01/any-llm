<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import type { UsageDayStat } from '@/api/usage'
import { formatCompact, formatInt } from '@/utils/format'
import { buildCalendarGrid, type CalendarCell } from '@/utils/calendar'

const props = defineProps<{ stats: UsageDayStat[] }>()

const CELL = 11
const GAP = 3
const STEP = CELL + GAP
const PAD_L = 24 // 左侧星期标签
const PAD_T = 16 // 顶部月份标签

const grid = computed(() => buildCalendarGrid(props.stats))
const total = computed(() => props.stats.reduce((a, s) => a + s.total_tokens, 0))
const svgWidth = computed(() => PAD_L + grid.value.cols * STEP - GAP + 2)
const svgHeight = PAD_T + 7 * STEP - GAP + 2

const cellX = (c: CalendarCell) => PAD_L + c.col * STEP
const cellY = (c: CalendarCell) => PAD_T + c.row * STEP

// 行 0 是周日；GitHub 惯例只标周一/周三/周五
const weekdays = [
  { row: 1, t: '一' },
  { row: 3, t: '三' },
  { row: 5, t: '五' },
]

// 窄屏网格横向滚动时，提示框在滚动容器外，要补偿 scrollLeft 才能跟住格子
const scrollEl = ref<HTMLElement | null>(null)
const scrollLeft = ref(0)
const viewWidth = ref(0)
function onScroll() {
  scrollLeft.value = scrollEl.value?.scrollLeft ?? 0
  viewWidth.value = scrollEl.value?.clientWidth ?? 0
}
onMounted(() => {
  viewWidth.value = scrollEl.value?.clientWidth ?? 0
})

const hover = ref<CalendarCell | null>(null)
// 提示框悬在格子上方；上三行翻转到下方，避免顶出卡片
const tipStyle = computed(() => {
  const c = hover.value
  if (!c) return {}
  const view = viewWidth.value || svgWidth.value
  const cx = cellX(c) + CELL / 2 - scrollLeft.value
  const left = Math.min(Math.max(cx, 78), view - 78)
  if (c.row <= 2) {
    return { left: `${left}px`, top: `${cellY(c) + CELL + 6}px`, transform: 'translateX(-50%)' }
  }
  return { left: `${left}px`, top: `${cellY(c) - 6}px`, transform: 'translate(-50%, -100%)' }
})
</script>

<template>
  <n-card title="每日 Token 用量" class="panel cal-card">
    <template #header-extra>
      <span class="cal-total">近一年共 <b class="mono">{{ formatCompact(total) }}</b> token</span>
    </template>
    <div class="cal-wrap">
      <div ref="scrollEl" class="cal-scroll" @scroll="onScroll">
        <svg :width="svgWidth" :height="svgHeight" class="cal-svg" @mouseleave="hover = null">
          <text
            v-for="m in grid.monthLabels"
            :key="'m' + m.col"
            :x="PAD_L + m.col * STEP"
            y="10"
            class="cal-label"
          >{{ m.text }}</text>
          <text
            v-for="w in weekdays"
            :key="'w' + w.row"
            x="0"
            :y="PAD_T + w.row * STEP + 9"
            class="cal-label"
          >{{ w.t }}</text>
          <rect
            v-for="c in grid.cells"
            :key="c.date"
            :x="cellX(c)"
            :y="cellY(c)"
            :width="CELL"
            :height="CELL"
            rx="2.5"
            class="cell"
            :class="'lv' + c.level"
            @mouseenter="hover = c"
          />
        </svg>
      </div>
      <div v-if="hover" class="cal-tip" :style="tipStyle">
        <div class="tip-title">{{ hover.date }}</div>
        <div class="tip-row">
          <span class="tip-name">Token</span>
          <span class="tip-val">{{ formatInt(hover.tokens) }}</span>
        </div>
        <div class="tip-row">
          <span class="tip-name">请求</span>
          <span class="tip-val">{{ formatInt(hover.requests) }}</span>
        </div>
        <div v-if="hover.errors > 0" class="tip-row">
          <span class="tip-name">失败</span>
          <span class="tip-val tip-err">{{ formatInt(hover.errors) }}</span>
        </div>
      </div>
    </div>
    <div class="cal-foot">
      <span class="cal-legend-label">少</span>
      <span v-for="i in 5" :key="i" class="cell legend-cell" :class="'lv' + (i - 1)"></span>
      <span class="cal-legend-label">多</span>
    </div>
  </n-card>
</template>

<style scoped>
.cal-card {
  margin-top: 20px;
  /* 色阶：暗色（含毛玻璃暗色）走品牌蓝的亮度递增 ramp；浅色在下方覆盖。
     fill 与 background 写在一起：格子（svg rect）吃 fill，图例（span）吃 background，
     各自忽略用不上的那条，一套类名两用。 */
  --lv0: rgba(148, 163, 184, 0.1);
  --lv1: #1e3a6e;
  --lv2: #2a55a3;
  --lv3: #3d78e8;
  --lv4: #7ba3ff;
}
.cal-total {
  font-size: 12px;
  color: var(--text-4);
}
.cal-total b {
  color: var(--brand-hover);
  font-size: 13px;
}
.cal-wrap {
  position: relative;
}
.cal-scroll {
  overflow-x: auto;
  padding-bottom: 2px;
}
.cal-svg {
  display: block;
}
.cal-label {
  font-size: 9.5px;
  fill: var(--text-4);
}
.cell {
  fill: var(--lv0);
  background: var(--lv0);
  transition: stroke 0.1s ease;
}
.cell.lv1 {
  fill: var(--lv1);
  background: var(--lv1);
}
.cell.lv2 {
  fill: var(--lv2);
  background: var(--lv2);
}
.cell.lv3 {
  fill: var(--lv3);
  background: var(--lv3);
}
.cell.lv4 {
  fill: var(--lv4);
  background: var(--lv4);
}
.cell:hover {
  stroke: rgba(148, 163, 184, 0.7);
  stroke-width: 1;
}
:root[data-theme='light'] .cal-card {
  --lv0: rgba(15, 23, 42, 0.06);
  --lv1: #c9dbff;
  --lv2: #96b8ff;
  --lv3: #5b8cff;
  --lv4: #4673e8;
}
:root[data-theme='light'] .cell:hover {
  stroke: rgba(15, 23, 42, 0.35);
}
.cal-tip {
  position: absolute;
  pointer-events: none;
  min-width: 120px;
  padding: 8px 10px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--tip-bg);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(6px);
  z-index: 5;
}
.tip-title {
  font-size: 11.5px;
  font-weight: 600;
  color: var(--text-2);
  margin-bottom: 5px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
.tip-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  line-height: 1.7;
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
.tip-err {
  color: #fb7185;
}
.cal-foot {
  margin-top: 8px;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 3px;
}
.cal-legend-label {
  font-size: 11px;
  color: var(--text-4);
  padding: 0 4px;
}
.legend-cell {
  width: 11px;
  height: 11px;
  border-radius: 2.5px;
  display: inline-block;
}
</style>
