<script setup lang="ts">
import { useRouter, useRoute } from 'vue-router'
import client from '../api/client'

const router = useRouter()
const route = useRoute()

async function logout() {
  await client.post('/logout')
  router.push('/login')
}

const menuItems = [
  { label: '上游管理', key: 'upstreams' },
  { label: 'API 密钥', key: 'keys' },
  { label: '用量统计', key: 'usage' },
]
</script>

<template>
  <n-layout has-sider style="height:100vh">
    <n-layout-sider bordered>
      <n-menu :value="route.name as string" :options="menuItems" @update:value="(v: string) => router.push({ name: v })" />
      <n-button text style="position:absolute;bottom:16px;left:16px" @click="logout">退出</n-button>
    </n-layout-sider>
    <n-layout-content style="padding:24px">
      <router-view />
    </n-layout-content>
  </n-layout>
</template>
