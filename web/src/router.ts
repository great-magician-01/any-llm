import { createRouter, createWebHashHistory } from 'vue-router'
import Login from './views/Login.vue'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/login', name: 'login', component: Login },
    {
      path: '/',
      component: () => import('./components/Layout.vue'),
      children: [
        { path: '', redirect: '/dashboard' },
        { path: 'dashboard', name: 'dashboard', component: () => import('./views/Dashboard.vue') },
        { path: 'upstreams', name: 'upstreams', component: () => import('./views/Upstreams.vue') },
        { path: 'keys', name: 'keys', component: () => import('./views/Keys.vue') },
        { path: 'usage', name: 'usage', component: () => import('./views/Usage.vue') },
        { path: 'conversations', name: 'conversations', component: () => import('./views/Conversations.vue') },
      ],
    },
    {
      // 毛玻璃风格页面套件：与经典版一一对应，仅视觉风格不同
      path: '/glass',
      component: () => import('./glass/GlassShell.vue'),
      children: [
        { path: 'login', name: 'glass-login', component: () => import('./glass/views/GlassLogin.vue') },
        {
          path: '',
          component: () => import('./glass/GlassLayout.vue'),
          children: [
            { path: '', redirect: '/glass/dashboard' },
            { path: 'dashboard', name: 'glass-dashboard', component: () => import('./glass/views/GlassDashboard.vue') },
            { path: 'upstreams', name: 'glass-upstreams', component: () => import('./glass/views/GlassUpstreams.vue') },
            { path: 'keys', name: 'glass-keys', component: () => import('./glass/views/GlassKeys.vue') },
            { path: 'usage', name: 'glass-usage', component: () => import('./glass/views/GlassUsage.vue') },
            { path: 'conversations', name: 'glass-conversations', component: () => import('./glass/views/GlassConversations.vue') },
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
