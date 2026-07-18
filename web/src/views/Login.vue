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
    router.push('/upstreams')
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
      <BrandMark :size="44" />
      <h1 class="title">any-llm</h1>
      <p class="subtitle">LLM 网关管理后台</p>
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
        登录
      </n-button>
      <n-alert v-if="error" type="error" :bordered="false" class="login-alert">
        {{ error }}
      </n-alert>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: linear-gradient(160deg, #eef3fb 0%, #f7f9fc 55%, #f1f5f9 100%);
}
.login-card {
  width: 360px;
  padding: 40px 36px 32px;
  background: #fff;
  border: 1px solid #e8edf3;
  border-radius: 16px;
  box-shadow: 0 12px 32px rgba(15, 23, 42, 0.08);
  display: flex;
  flex-direction: column;
  align-items: center;
}
.title {
  margin: 14px 0 0;
  font-size: 22px;
  font-weight: 600;
  color: #0f172a;
}
.subtitle {
  margin: 4px 0 26px;
  font-size: 13px;
  color: #64748b;
}
.login-btn {
  margin-top: 14px;
}
.login-alert {
  margin-top: 14px;
}
</style>
