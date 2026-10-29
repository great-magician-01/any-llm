<script setup lang="ts">
import { computed, h } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import client from '@/api/client'
import BrandMark from '@/components/BrandMark.vue'
import AppIcon, { type IconName } from '@/components/AppIcon.vue'
import ThemePicker from '@/components/ThemePicker.vue'
import { isGlassPath, suitePath, switchSuite } from '@/utils/suite'

const router = useRouter()
const route = useRoute()
/** 一套视图服务两套 URL 树：glass 下所有内部跳转都要带 /glass 前缀 */
const isGlass = computed(() => isGlassPath(route.path))

async function logout() {
  try {
    await client.post('/logout')
  } catch {
    // 登出接口失败也要清本地态并回登录页，否则用户卡在一个即将失效的会话里
  }
  localStorage.removeItem('authed')
  router.push(suitePath(route.path, '/login'))
}

function item(label: string, path: string, icon: IconName) {
  return {
    label,
    // 菜单 key 直接用当前套件内的完整路径：高亮与跳转都天然正确
    key: suitePath(route.path, path),
    icon: () => h(AppIcon, { name: icon, size: 16 }),
  }
}

const menuItems = [
  item('概览', '/dashboard', 'dashboard'),
  item('上游管理', '/upstreams', 'layers'),
  item('模型别名', '/aliases', 'link'),
  item('API 密钥', '/keys', 'key'),
  item('用量统计', '/usage', 'chart'),
  item('账单', '/bills', 'wallet'),
  item('对话记录', '/conversations', 'chat'),
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
          :value="route.path"
          :options="menuItems"
          :indent="18"
          @update:value="(v: string) => router.push(v)"
        />
        <div class="sider-footer">
          <span class="admin-name">管理员</span>
          <div class="footer-actions">
            <ThemePicker />
            <button class="logout-btn" title="退出登录" aria-label="退出登录" @click="logout">
              <AppIcon name="logout" :size="15" />
            </button>
          </div>
        </div>
        <div class="suite-switch" @click="router.push(switchSuite(route.fullPath))">
          <AppIcon name="swap" :size="14" />
          <span>{{ isGlass ? '切换到经典版' : '切换到液态玻璃版' }}</span>
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
  background: var(--sider-bg);
  backdrop-filter: var(--sider-blur);
  -webkit-backdrop-filter: var(--sider-blur);
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
.admin-name {
  font-size: 13px;
  color: var(--text-2);
  white-space: nowrap;
}
.footer-actions {
  display: flex;
  align-items: center;
  gap: 4px;
}
/* 图标按钮外观对齐 ThemePicker（同尺寸同边框），悬停转危险色 */
.logout-btn {
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
.logout-btn:hover {
  color: #fb7185;
  border-color: rgba(251, 113, 133, 0.45);
  background: rgba(251, 113, 133, 0.08);
}
.suite-switch {
  margin-top: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 7px 10px;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
  background: var(--suite-bg);
  box-shadow: var(--suite-glow);
  color: var(--text-3);
  font-size: 12px;
  cursor: pointer;
  transition:
    color 0.15s ease,
    border-color 0.15s ease,
    background 0.15s ease,
    box-shadow 0.15s ease;
}
.suite-switch:hover {
  color: var(--brand-hover);
  border-color: var(--brand-border);
  background: var(--suite-bg-hover);
  box-shadow: var(--suite-glow-hover);
}
.content {
  background: transparent;
}
/* 宽屏时放大内容上限让表格各列完整显示；窄屏自然收缩，由表格自身的
   scroll-x 提供横向滚动（见 Upstreams/Usage 等页） */
.page {
  max-width: 1600px;
  margin: 0 auto;
  padding: 30px 32px 56px;
}
</style>
