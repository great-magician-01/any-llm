import { createRouter, createWebHashHistory } from 'vue-router'
import Login from '@/themes/classic/views/Login.vue'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/login', name: 'login', component: Login },
    {
      path: '/',
      component: () => import('@/themes/classic/Layout.vue'),
      children: [
        { path: '', redirect: '/dashboard' },
        { path: 'dashboard', name: 'dashboard', component: () => import('@/themes/classic/views/Dashboard.vue') },
        { path: 'upstreams', name: 'upstreams', component: () => import('@/themes/classic/views/Upstreams.vue') },
        { path: 'aliases', name: 'aliases', component: () => import('@/themes/classic/views/Aliases.vue') },
        { path: 'keys', name: 'keys', component: () => import('@/themes/classic/views/Keys.vue') },
        { path: 'usage', name: 'usage', component: () => import('@/themes/classic/views/Usage.vue') },
        { path: 'conversations', name: 'conversations', component: () => import('@/themes/classic/views/Conversations.vue') },
      ],
    },
    {
      // 毛玻璃风格页面套件：与经典版一一对应，仅视觉风格不同
      path: '/glass',
      component: () => import('@/themes/glass/GlassShell.vue'),
      children: [
        { path: 'login', name: 'glass-login', component: () => import('@/themes/glass/views/GlassLogin.vue') },
        {
          path: '',
          component: () => import('@/themes/glass/GlassLayout.vue'),
          children: [
            { path: '', redirect: '/glass/dashboard' },
            { path: 'dashboard', name: 'glass-dashboard', component: () => import('@/themes/glass/views/GlassDashboard.vue') },
            { path: 'upstreams', name: 'glass-upstreams', component: () => import('@/themes/glass/views/GlassUpstreams.vue') },
            { path: 'aliases', name: 'glass-aliases', component: () => import('@/themes/glass/views/GlassAliases.vue') },
            { path: 'keys', name: 'glass-keys', component: () => import('@/themes/glass/views/GlassKeys.vue') },
            { path: 'usage', name: 'glass-usage', component: () => import('@/themes/glass/views/GlassUsage.vue') },
            { path: 'conversations', name: 'glass-conversations', component: () => import('@/themes/glass/views/GlassConversations.vue') },
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
    return { name: loginName }
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
