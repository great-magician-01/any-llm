import { createRouter, createWebHashHistory } from 'vue-router'
import Login from '@/views/Login.vue'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/login', name: 'login', component: Login },
    {
      path: '/',
      component: () => import('@/views/Layout.vue'),
      children: [
        { path: '', redirect: '/dashboard' },
        { path: 'dashboard', name: 'dashboard', component: () => import('@/views/Dashboard.vue') },
        { path: 'upstreams', name: 'upstreams', component: () => import('@/views/Upstreams.vue') },
        { path: 'aliases', name: 'aliases', component: () => import('@/views/Aliases.vue') },
        { path: 'keys', name: 'keys', component: () => import('@/views/Keys.vue') },
        { path: 'usage', name: 'usage', component: () => import('@/views/Usage.vue') },
        { path: 'conversations', name: 'conversations', component: () => import('@/views/Conversations.vue') },
      ],
    },
    {
      // 液态玻璃风格页面套件：与经典版共用同一套视图（@/views），仅多一层
      // GlassShell 皮肤壳（嵌套主题 provider + 玻璃背景），视觉差异全部由
      // glass.css 的 .glass-root 变量覆盖吸收
      path: '/glass',
      component: () => import('@/themes/glass/GlassShell.vue'),
      children: [
        { path: 'login', name: 'glass-login', component: () => import('@/views/Login.vue') },
        {
          path: '',
          component: () => import('@/views/Layout.vue'),
          children: [
            { path: '', redirect: '/glass/dashboard' },
            { path: 'dashboard', name: 'glass-dashboard', component: () => import('@/views/Dashboard.vue') },
            { path: 'upstreams', name: 'glass-upstreams', component: () => import('@/views/Upstreams.vue') },
            { path: 'aliases', name: 'glass-aliases', component: () => import('@/views/Aliases.vue') },
            { path: 'keys', name: 'glass-keys', component: () => import('@/views/Keys.vue') },
            { path: 'usage', name: 'glass-usage', component: () => import('@/views/Usage.vue') },
            { path: 'conversations', name: 'glass-conversations', component: () => import('@/views/Conversations.vue') },
          ],
        },
      ],
    },
  ],
})

router.beforeEach((to, _from) => {
  // Session cookie is HttpOnly, so JS cannot read it; use a localStorage flag
  // set on login as a UI hint. Real auth is still enforced by the API (401).
  const hasSession = localStorage.getItem('authed') === '1'
  const isGlass = to.path.startsWith('/glass')
  const loginName = isGlass ? 'glass-login' : 'login'
  const homeName = isGlass ? 'glass-dashboard' : 'dashboard'
  if (to.name !== loginName && !hasSession) {
    // 深链（含 query/hash）不能丢：带上 ?redirect= 让登录后回到用户本来要去的
    // 页面。目标是本主题落地页时不必带——主题根路径在守卫之前就被重定向规则
    // 解析成了 dashboard，带上只会让 URL 变脏。
    const homePath = isGlass ? '/glass/dashboard' : '/dashboard'
    const rootPath = isGlass ? '/glass' : '/'
    if (to.fullPath === homePath || to.fullPath === rootPath) {
      return { name: loginName }
    }
    return { name: loginName, query: { redirect: to.fullPath } }
  }
  if (to.name === loginName && hasSession) {
    return { name: homeName }
  }
})

// A stale index.html (cached by the browser) may reference chunk files with
// old hashes that no longer exist on the server. Vue Router surfaces the
// failed dynamic import as a navigation error; reload once to fetch the
// fresh index.html. The sessionStorage flag prevents a reload loop when the
// chunk is genuinely missing.
router.onError((error) => {
  if (error?.message?.includes('Failed to fetch dynamically imported module')) {
    const flag = 'reloaded-on-import-fail'
    if (!sessionStorage.getItem(flag)) {
      sessionStorage.setItem(flag, '1')
      window.location.reload()
    }
  }
})

router.afterEach((to) => {
  // 玻璃套件的浮层（Modal/Popover/Message 等 teleport 到 body）靠 body.glass-mode 加毛玻璃
  document.body.classList.toggle('glass-mode', to.path.startsWith('/glass'))
  sessionStorage.removeItem('reloaded-on-import-fail')
})

export default router
