<script setup lang="ts">
import { NTooltip } from 'naive-ui'
import { computed } from 'vue'
import { useTheme } from '../composables/useTheme'
import AppIcon from './AppIcon.vue'

const { isDark, toggle } = useTheme()

/** 图标指向「点一下会切到哪」，而不是当前所处模式。 */
const icon = computed(() => (isDark.value ? 'sun' : 'moon'))
const tip = computed(() => (isDark.value ? '切换到浅色主题' : '切换到深色主题'))
</script>

<template>
  <n-tooltip trigger="hover">
    <template #trigger>
      <button type="button" class="theme-toggle" :aria-label="tip" @click="toggle">
        <AppIcon :name="icon" :size="15" />
      </button>
    </template>
    {{ tip }}
  </n-tooltip>
</template>

<style scoped>
.theme-toggle {
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
  color: var(--text-3);
  cursor: pointer;
  transition:
    color 0.15s ease,
    border-color 0.15s ease,
    background 0.15s ease;
}
.theme-toggle:hover {
  color: var(--brand-hover);
  border-color: rgba(91, 140, 255, 0.4);
}
</style>
