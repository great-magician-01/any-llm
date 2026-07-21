<script setup lang="ts">
import { h } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import client from '../api/client'
import BrandMark from './BrandMark.vue'
import AppIcon, { type IconName } from './AppIcon.vue'

const router = useRouter()
const route = useRoute()

async function logout() {
  await client.post('/logout')
  localStorage.removeItem('authed')
  router.push('/login')
}

function item(label: string, key: string, icon: IconName) {
  return {
    label,
    key,
    icon: () => h(AppIcon, { name: icon, size: 16 }),
  }
}

const menuItems = [
  item('概览', 'dashboard', 'dashboard'),
  item('上游管理', 'upstreams', 'layers'),
  item('API 密钥', 'keys', 'key'),
  item('用量统计', 'usage', 'chart'),
]
</script>

<template>
  <n-layout has-sider style="height: 100vh">
    <n-layout-sider :width="228" class="sider">
      <div class="sider-inner">
        <div class="brand">
          <BrandMark :size="34" />
          <div class="brand-text">
            <span class="brand-name text-grad">any-llm</span>
            <span class="brand-sub">LLM GATEWAY</span>
          </div>
        </div>
        <n-menu
          :value="route.name as string"
          :options="menuItems"
          :indent="18"
          @update:value="(v: string) => router.push({ name: v })"
        />
        <div class="sider-footer">
          <div class="admin-chip">
            <span class="admin-avatar">A</span>
            <span class="admin-name">管理员</span>
          </div>
          <button class="logout-btn" title="退出登录" @click="logout">
            <AppIcon name="logout" :size="15" />
            <span>退出登录</span>
          </button>
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
  border-right: 1px solid var(--border-soft);
  background: linear-gradient(180deg, rgba(91, 140, 255, 0.05) 0%, rgba(10, 15, 29, 0) 240px);
}
.sider-inner {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: 18px 12px 14px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 2px 10px 20px;
}
.brand-text {
  display: flex;
  flex-direction: column;
  line-height: 1.25;
}
.brand-name {
  font-size: 16px;
  font-weight: 700;
  letter-spacing: 0.01em;
}
.brand-sub {
  font-size: 10px;
  letter-spacing: 0.18em;
  color: var(--text-4);
}
.sider-footer {
  margin-top: auto;
  padding-top: 12px;
  border-top: 1px solid var(--border-soft);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.admin-chip {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.admin-avatar {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: var(--grad);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 0 10px rgba(91, 140, 255, 0.4);
}
.admin-name {
  font-size: 13px;
  color: var(--text-2);
  white-space: nowrap;
}
.logout-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--text-3);
  font-size: 13px;
  cursor: pointer;
  transition:
    color 0.15s ease,
    background 0.15s ease;
}
.logout-btn:hover {
  color: #fb7185;
  background: rgba(251, 113, 133, 0.08);
}
.content {
  background: transparent;
}
.page {
  max-width: 1180px;
  margin: 0 auto;
  padding: 30px 32px 56px;
}
</style>
