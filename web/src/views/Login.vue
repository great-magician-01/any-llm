<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import client from '../api/client'
import BrandMark from '../components/BrandMark.vue'

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
    router.push('/')
  } catch {
    error.value = '密码错误'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="orb orb-1"></div>
    <div class="orb orb-2"></div>
    <div class="grid-overlay"></div>

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
  background: #060a13;
}
.orb {
  position: absolute;
  border-radius: 50%;
  filter: blur(90px);
  opacity: 0.5;
  animation: drift 14s ease-in-out infinite alternate;
}
.orb-1 {
  width: 480px;
  height: 480px;
  top: -160px;
  right: -80px;
  background: radial-gradient(circle, rgba(91, 140, 255, 0.55), transparent 65%);
}
.orb-2 {
  width: 420px;
  height: 420px;
  bottom: -140px;
  left: -100px;
  background: radial-gradient(circle, rgba(34, 211, 238, 0.4), transparent 65%);
  animation-delay: -7s;
}
@keyframes drift {
  from {
    transform: translate(0, 0) scale(1);
  }
  to {
    transform: translate(-40px, 40px) scale(1.12);
  }
}
.grid-overlay {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(148, 163, 184, 0.05) 1px, transparent 1px),
    linear-gradient(90deg, rgba(148, 163, 184, 0.05) 1px, transparent 1px);
  background-size: 44px 44px;
  mask-image: radial-gradient(ellipse 70% 60% at 50% 45%, #000 30%, transparent 75%);
  -webkit-mask-image: radial-gradient(ellipse 70% 60% at 50% 45%, #000 30%, transparent 75%);
}
.login-card {
  position: relative;
  width: 380px;
  padding: 44px 38px 30px;
  background: rgba(14, 21, 38, 0.72);
  border: 1px solid rgba(148, 163, 184, 0.16);
  border-radius: 20px;
  box-shadow:
    0 24px 64px rgba(2, 6, 18, 0.6),
    inset 0 1px 0 rgba(255, 255, 255, 0.05);
  backdrop-filter: blur(18px);
  -webkit-backdrop-filter: blur(18px);
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
  background: linear-gradient(90deg, transparent, rgba(91, 140, 255, 0.7), rgba(34, 211, 238, 0.7), transparent);
}
.logo-glow {
  padding: 6px;
  border-radius: 20px;
  animation: pulse 3.5s ease-in-out infinite;
}
@keyframes pulse {
  0%,
  100% {
    filter: drop-shadow(0 0 10px rgba(91, 140, 255, 0.35));
  }
  50% {
    filter: drop-shadow(0 0 22px rgba(34, 211, 238, 0.5));
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
  color: #8fa0b8;
  letter-spacing: 0.04em;
}
.login-btn {
  margin-top: 16px;
  font-weight: 600;
  box-shadow: 0 6px 20px rgba(91, 140, 255, 0.35);
}
.login-alert {
  margin-top: 14px;
}
.foot {
  margin: 26px 0 0;
  font-size: 11px;
  letter-spacing: 0.08em;
  color: #5b6b82;
}
</style>
