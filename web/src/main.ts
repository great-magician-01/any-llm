import { createApp } from 'vue'
import naive from 'naive-ui'
import App from './App.vue'
import router from './router'
import './style.css'
import './glass/glass.css'

const app = createApp(App)
app.use(router)
app.use(naive)
app.mount('#app')
