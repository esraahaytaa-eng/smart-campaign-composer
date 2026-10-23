import { createApp, watch } from 'vue'
import App from './App.vue'
import { locale, t } from './i18n'
import { createAppRouter } from './router'
import './styles.css'

const router = createAppRouter()

// The tab title follows the page and the UI language (D44).
function setTitle() {
  const key = router.currentRoute.value.meta.title
  document.title = key ? t('app.title', { page: t(key) }) : t('app.name')
}
router.afterEach(setTitle)
watch(locale, setTitle)

createApp(App).use(router).mount('#app')
