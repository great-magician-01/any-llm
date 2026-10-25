<script setup lang="ts">
import { NPopover } from 'naive-ui'
import { computed } from 'vue'
import { useTheme } from '../composables/useTheme'
import { ACCENTS } from '@/utils/accents'
import AppIcon from './AppIcon.vue'

const { accent, setAccent } = useTheme()

const current = computed(() => ACCENTS.find((a) => a.id === accent.value) ?? ACCENTS[0])
const swatchOf = (a: (typeof ACCENTS)[number]) =>
  `linear-gradient(135deg, ${a.swatch[0]}, ${a.swatch[1]})`
</script>

<template>
  <n-popover trigger="click" placement="bottom-end" :show-arrow="false">
    <template #trigger>
      <button
        type="button"
        class="accent-picker"
        :aria-label="`配色主题：${current.name}`"
        :title="`配色主题：${current.name}`"
      >
        <span class="dot" :style="{ background: swatchOf(current) }"></span>
      </button>
    </template>
    <div class="accent-menu">
      <div class="menu-title">配色主题</div>
      <button
        v-for="a in ACCENTS"
        :key="a.id"
        type="button"
        class="accent-option"
        :class="{ on: a.id === accent }"
        @click="setAccent(a.id)"
      >
        <span class="dot" :style="{ background: swatchOf(a) }"></span>
        <span class="option-name">{{ a.name }}</span>
        <AppIcon v-if="a.id === accent" name="check" :size="14" class="option-check" />
      </button>
    </div>
  </n-popover>
</template>

<style scoped>
/* 按钮外观对齐 ThemeToggle：同尺寸同边框，内容换成当前配色的渐变色点 */
.accent-picker {
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
.accent-picker:hover {
  border-color: var(--brand-border);
}
.dot {
  width: 13px;
  height: 13px;
  border-radius: 50%;
  flex: none;
  box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.35);
}
.accent-menu {
  min-width: 148px;
}
.menu-title {
  font-size: 12px;
  color: var(--text-3);
  padding: 2px 4px 8px;
}
.accent-option {
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
.accent-option:hover {
  color: var(--text);
  background: rgba(148, 163, 184, 0.1);
}
.accent-option.on {
  color: var(--brand-hover);
  font-weight: 600;
}
.option-name {
  flex: 1;
}
.option-check {
  color: var(--brand-hover);
}
</style>
