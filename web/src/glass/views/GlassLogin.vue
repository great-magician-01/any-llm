<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import client from '../../api/client'
import BrandMark from '../../components/BrandMark.vue'

const router = useRouter()
const password = ref('')
const loading = ref(false)
const error = ref('')

async function login() {
  loading.value = true
  error.value = ''
  try {
    await client.post('/login', { password: password.value })
    localStorage.setItem('authed', '1')
    router.push('/glass/dashboard')
  } catch {
    error.value = '密码错误'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-card">
      <div class="logo-glow">
        <BrandMark :size="52" />
      </div>
      <h1 class="title text-grad">any-llm</h1>
      <p class="subtitle">统一 LLM 网关 · 管理后台</p>
      <n-input
        v-model:value="password"
        type="password"
        size="large"
        placeholder="管理员密码"
        show-password-on="click"
        style="width: 100%"
        @keyup.enter="login"
      />
      <n-button
        type="primary"
        size="large"
        :loading="loading"
        block
        class="login-btn"
        @click="login"
      >
        进入控制台
      </n-button>
      <n-alert v-if="error" type="error" :bordered="false" class="login-alert">
        {{ error }}
      </n-alert>
      <p class="foot">OpenAI / Anthropic 兼容 · 多上游聚合</p>
      <button class="switch-link" @click="router.push('/login')">切换到经典版</button>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  position: relative;
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  overflow: hidden;
}
.login-card {
  position: relative;
  width: 380px;
  padding: 44px 38px 30px;
  background: rgba(255, 255, 255, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.16);
  border-radius: 22px;
  box-shadow:
    0 24px 64px rgba(3, 8, 24, 0.5),
    inset 0 1px 0 rgba(255, 255, 255, 0.12);
  backdrop-filter: blur(24px) saturate(1.5);
  -webkit-backdrop-filter: blur(24px) saturate(1.5);
  display: flex;
  flex-direction: column;
  align-items: center;
}
.login-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 24px;
  right: 24px;
  height: 1px;
  background: linear-gradient(90deg, transparent, rgba(123, 163, 255, 0.8), rgba(78, 216, 240, 0.8), transparent);
}
.logo-glow {
  padding: 6px;
  border-radius: 20px;
  animation: pulse 3.5s ease-in-out infinite;
}
@keyframes pulse {
  0%,
  100% {
    filter: drop-shadow(0 0 10px rgba(123, 163, 255, 0.35));
  }
  50% {
    filter: drop-shadow(0 0 22px rgba(78, 216, 240, 0.5));
  }
}
.title {
  margin: 16px 0 0;
  font-size: 24px;
  font-weight: 700;
  letter-spacing: 0.01em;
}
.subtitle {
  margin: 6px 0 28px;
  font-size: 13px;
  color: var(--text-3);
  letter-spacing: 0.04em;
}
.login-btn {
  margin-top: 16px;
  font-weight: 600;
  box-shadow: 0 6px 20px rgba(123, 163, 255, 0.35);
}
.login-alert {
  margin-top: 14px;
}
.foot {
  margin: 26px 0 0;
  font-size: 11px;
  letter-spacing: 0.08em;
  color: var(--text-4);
}
.switch-link {
  margin-top: 12px;
  padding: 4px 8px;
  border: none;
  background: transparent;
  color: var(--text-4);
  font-size: 12px;
  cursor: pointer;
  transition: color 0.15s ease;
}
.switch-link:hover {
  color: var(--brand-hover);
}
</style>
