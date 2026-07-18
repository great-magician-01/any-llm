<script setup lang="ts">
import { zhCN, dateZhCN } from 'naive-ui'
import { themeOverrides } from './theme'
</script>

<template>
  <n-config-provider :theme-overrides="themeOverrides" :locale="zhCN" :date-locale="dateZhCN">
    <n-message-provider>
      <router-view />
    </n-message-provider>
  </n-config-provider>
</template>
