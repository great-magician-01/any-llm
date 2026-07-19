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
        { path: '', redirect: '/upstreams' },
        { path: 'upstreams', name: 'upstreams', component: () => import('./views/Upstreams.vue') },
        { path: 'keys', name: 'keys', component: () => import('./views/Keys.vue') },
        { path: 'usage', name: 'usage', component: () => import('./views/Usage.vue') },
      ],
    },
  ],
})

router.beforeEach((to, _from) => {
  // Session cookie is HttpOnly, so JS cannot read it; use a localStorage flag
  // set on login as a UI hint. Real auth is still enforced by the API (401).
  const hasSession = localStorage.getItem('authed') === '1'
  if (to.name !== 'login' && !hasSession) {
    return { name: 'login' }
  }
  if (to.name === 'login' && hasSession) {
    return { name: 'upstreams' }
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

router.afterEach(() => {
  sessionStorage.removeItem('reloaded-on-import-fail')
})

export default router
