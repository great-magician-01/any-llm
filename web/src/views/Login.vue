<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import client from '../api/client'

const router = useRouter()
const password = ref('')
const loading = ref(false)
const error = ref('')

async function login() {
  loading.value = true
  error.value = ''
  try {
    await client.post('/login', { password: password.value })
    router.push('/upstreams')
  } catch {
    error.value = '密码错误'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div style="display:flex;justify-content:center;align-items:center;height:100vh">
    <n-card title="any-llm 管理" style="width:400px">
      <n-input v-model:value="password" type="password" placeholder="管理员密码" @keyup.enter="login" />
      <n-button type="primary" :loading="loading" block style="margin-top:16px" @click="login">登录</n-button>
      <p v-if="error" style="color:red;margin-top:8px">{{ error }}</p>
    </n-card>
  </div>
</template>
