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
  const hasSession = document.cookie.includes('s=')
  if (to.name !== 'login' && !hasSession) {
    return { name: 'login' }
  }
  if (to.name === 'login' && hasSession) {
    return { name: 'upstreams' }
  }
})

export default router
