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

export default router
