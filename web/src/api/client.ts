import axios from 'axios'

const client = axios.create({
  baseURL: '/api/admin',
  withCredentials: true,
  headers: { 'Content-Type': 'application/json' },
})

client.interceptors.response.use(
  (r) => r,
  (err) => {
    if (err.response?.status === 401 && window.location.hash !== '#/login') {
      localStorage.removeItem('authed')
      window.location.hash = '#/login'
    }
    return Promise.reject(err)
  },
)

export default client
