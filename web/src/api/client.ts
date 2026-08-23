import axios from 'axios'

const client = axios.create({
  baseURL: '/api/admin',
  withCredentials: true,
  headers: { 'Content-Type': 'application/json' },
})

client.interceptors.response.use(
  (r) => r,
  (err) => {
    if (err.response?.status === 401) {
      // 玻璃套件（#/glass/*）回到玻璃登录页，经典版回到 #/login
      const isGlass = window.location.hash.startsWith('#/glass')
      const loginHash = isGlass ? '#/glass/login' : '#/login'
      if (window.location.hash !== loginHash) {
        localStorage.removeItem('authed')
        window.location.hash = loginHash
      }
    }
    return Promise.reject(err)
  },
)

export default client
