<script setup lang="ts">
import { computed } from 'vue'
import { darkTheme, lightTheme, zhCN, dateZhCN } from 'naive-ui'
import { useTheme } from '@/composables/useTheme'
import { darkGlassThemeOverrides, lightGlassThemeOverrides } from './theme'

const { isDark, accent } = useTheme()
const naiveTheme = computed(() => (isDark.value ? darkTheme : lightTheme))
const naiveOverrides = computed(() =>
  isDark.value ? darkGlassThemeOverrides(accent.value) : lightGlassThemeOverrides(accent.value),
)
</script>

<template>
  <n-config-provider
    :theme="naiveTheme"
    :theme-overrides="naiveOverrides"
    :locale="zhCN"
    :date-locale="dateZhCN"
  >
    <div class="glass-root">
      <div class="glass-bg"></div>
      <div class="glass-bg-noise"></div>
      <router-view />
    </div>
  </n-config-provider>
</template>
