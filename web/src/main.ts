import { createApp, watch } from 'vue'
import App from './App.vue'
import { locale, localeReady, t } from './i18n'
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

// A remembered Turkish is fetched first, so the first render is already in it (D58).
localeReady.then(() => createApp(App).use(router).mount('#app'))
