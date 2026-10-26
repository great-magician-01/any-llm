<script setup lang="ts">
import { NPopover } from 'naive-ui'
import { computed, ref } from 'vue'
import { useTheme } from '../composables/useTheme'
import { THEMES, type ThemeDef } from '@/utils/themes'
import AppIcon from './AppIcon.vue'

const { theme, setTheme } = useTheme()

/** 选中即收起：主题是一次性的选择，不是多选/连续调参 */
const show = ref(false)
const current = computed(() => THEMES.find((t) => t.id === theme.value) ?? THEMES[0])
const swatchOf = (t: ThemeDef) => `linear-gradient(135deg, ${t.swatch[0]}, ${t.swatch[1]})`

function pick(t: ThemeDef) {
  setTheme(t.id)
  show.value = false
}
</script>

<template>
  <n-popover v-model:show="show" trigger="click" placement="bottom-end" :show-arrow="false">
    <template #trigger>
      <button
        type="button"
        class="theme-picker"
        :aria-label="`界面主题：${current.name}`"
        :title="`界面主题：${current.name}`"
      >
        <span class="dot" :style="{ background: current.preview }">
          <span class="dot-core" :style="{ background: swatchOf(current) }"></span>
        </span>
      </button>
    </template>
    <div class="theme-menu">
      <div class="menu-title">界面主题</div>
      <button
        v-for="t in THEMES"
        :key="t.id"
        type="button"
        class="theme-option"
        :class="{ on: t.id === theme }"
        @click="pick(t)"
      >
        <span class="thumb" :style="{ background: t.preview }">
          <span class="thumb-core" :style="{ background: swatchOf(t) }"></span>
        </span>
        <span class="option-text">
          <span class="option-name">{{ t.name }}</span>
          <span class="option-hint">{{ t.hint }}</span>
        </span>
        <AppIcon v-if="t.id === theme" name="check" :size="14" class="option-check" />
      </button>
    </div>
  </n-popover>
</template>

<style scoped>
/* 按钮外观沿用原 ThemeToggle / AccentPicker：同尺寸同边框，
   内容换成「当前主题底色 + 品牌渐变点」的两层缩略图 */
.theme-picker {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 26px;
  flex: none;
  padding: 0;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
  background: var(--surface-2);
  cursor: pointer;
  transition:
    border-color 0.15s ease,
    background 0.15s ease;
}
.theme-picker:hover {
  border-color: var(--brand-border);
}
.dot {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 15px;
  height: 15px;
  border-radius: 4px;
  flex: none;
  box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.2);
}
.dot-core {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}
.theme-menu {
  min-width: 168px;
}
.menu-title {
  font-size: 12px;
  color: var(--text-3);
  padding: 2px 4px 8px;
}
.theme-option {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 7px 8px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--text-2);
  font-size: 13px;
  cursor: pointer;
  text-align: left;
  transition:
    color 0.15s ease,
    background 0.15s ease;
}
.theme-option:hover {
  color: var(--text);
  background: rgba(148, 163, 184, 0.1);
}
.theme-option.on {
  color: var(--brand-hover);
  font-weight: 600;
}
/* 缩略图：主题底色 + 居中品牌渐变点，一眼区分明暗与机体色 */
.thumb {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 20px;
  border-radius: 6px;
  flex: none;
  box-shadow: inset 0 0 0 1px var(--border-soft);
}
.thumb-core {
  width: 9px;
  height: 9px;
  border-radius: 50%;
}
.option-text {
  flex: 1;
  display: flex;
  flex-direction: column;
  line-height: 1.25;
}
.option-hint {
  font-size: 11px;
  font-weight: 400;
  color: var(--text-4);
}
.option-check {
  color: var(--brand-hover);
}
</style>
