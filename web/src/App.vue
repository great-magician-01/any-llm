<script setup lang="ts">
import { computed } from 'vue'
import { darkTheme, lightTheme, zhCN, dateZhCN } from 'naive-ui'
import { useTheme } from './composables/useTheme'
import { darkThemeOverrides, lightThemeOverrides } from './theme'

const { isDark } = useTheme()
const naiveTheme = computed(() => (isDark.value ? darkTheme : lightTheme))
const naiveOverrides = computed(() => (isDark.value ? darkThemeOverrides : lightThemeOverrides))
</script>

<template>
  <n-config-provider
    :theme="naiveTheme"
    :theme-overrides="naiveOverrides"
    :locale="zhCN"
    :date-locale="dateZhCN"
  >
    <n-message-provider>
      <router-view />
    </n-message-provider>
  </n-config-provider>
</template>
