<script setup lang="ts">
import { darkTheme, zhCN, dateZhCN } from 'naive-ui'
import { glassThemeOverrides } from './theme'
</script>

<template>
  <n-config-provider
    :theme="darkTheme"
    :theme-overrides="glassThemeOverrides"
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
