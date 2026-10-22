import { createApp } from 'vue'
import App from './App.vue'
import { createAppRouter } from './router'
import './styles.css'

const router = createAppRouter()
router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · Smart Campaign Composer` : 'Smart Campaign Composer'
})

createApp(App).use(router).mount('#app')
