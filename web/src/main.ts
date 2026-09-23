import { createApp } from 'vue'
import naive from 'naive-ui'
import App from './App.vue'
import router from './router'
import { initTheme } from './composables/useTheme'
import './style.css'
import '@/themes/glass/glass.css'

// 首帧前定好 <html data-theme>（index.html 的内联脚本已预置，这里补齐状态与监听）
initTheme()

const app = createApp(App)
app.use(router)
app.use(naive)
app.mount('#app')
