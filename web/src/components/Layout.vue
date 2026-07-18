<script setup lang="ts">
import { useRouter, useRoute } from 'vue-router'
import client from '../api/client'
import BrandMark from './BrandMark.vue'

const router = useRouter()
const route = useRoute()

async function logout() {
  await client.post('/logout')
  localStorage.removeItem('authed')
  router.push('/login')
}

const menuItems = [
  { label: '上游管理', key: 'upstreams' },
  { label: 'API 密钥', key: 'keys' },
  { label: '用量统计', key: 'usage' },
]
</script>

<template>
  <n-layout has-sider style="height: 100vh">
    <n-layout-sider :width="216" class="sider">
      <div class="sider-inner">
        <div class="brand">
          <BrandMark :size="32" />
          <div class="brand-text">
            <span class="brand-name">any-llm</span>
            <span class="brand-sub">LLM Gateway</span>
          </div>
        </div>
        <n-menu
          :value="route.name as string"
          :options="menuItems"
          :indent="16"
          @update:value="(v: string) => router.push({ name: v })"
        />
        <div class="sider-footer">
          <n-button text class="logout-btn" @click="logout">退出登录</n-button>
        </div>
      </div>
    </n-layout-sider>
    <n-layout-content class="content">
      <div class="page">
        <router-view />
      </div>
    </n-layout-content>
  </n-layout>
</template>

<style scoped>
.sider {
  border-right: 1px solid var(--border);
}
.sider-inner {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: 16px 12px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 10px 18px;
}
.brand-text {
  display: flex;
  flex-direction: column;
  line-height: 1.25;
}
.brand-name {
  font-size: 15px;
  font-weight: 600;
  color: var(--text);
}
.brand-sub {
  font-size: 11px;
  color: var(--text-3);
}
.sider-footer {
  margin-top: auto;
  padding: 12px 10px 2px;
  border-top: 1px solid var(--border);
}
.logout-btn {
  font-size: 13px;
  color: var(--text-3);
}
.logout-btn:hover {
  color: var(--brand);
}
.content {
  background: var(--bg);
}
.page {
  max-width: 1080px;
  margin: 0 auto;
  padding: 28px 32px 48px;
}
</style>
