import { createRouter, createWebHistory } from 'vue-router'
import { setUnauthorizedHandler } from './api'
import { decide } from './guard'
import { clearSession, loadSession } from './session'
import AppLayout from './layouts/AppLayout.vue'
import LoginView from './views/LoginView.vue'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    title?: string
  }
}

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: LoginView, meta: { public: true, title: 'Sign in' } },
    {
      path: '/',
      component: AppLayout,
      children: [
        { path: '', name: 'dashboard', component: () => import('./views/DashboardView.vue'), meta: { title: 'Dashboard' } },
        { path: 'campaigns', name: 'campaigns', component: () => import('./views/CampaignsView.vue'), meta: { title: 'Campaigns' } },
        { path: ':pathMatch(.*)*', name: 'not-found', component: () => import('./views/NotFoundView.vue'), meta: { title: 'Page not found' } },
      ],
    },
  ],
})

router.beforeEach(async (to) => decide(to, (await loadSession()) !== null))

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · Smart Campaign Composer` : 'Smart Campaign Composer'
})

// Any API call that finds the session expired sends the marketer to sign in
// again and brings them back afterwards.
setUnauthorizedHandler(() => {
  clearSession()
  const current = router.currentRoute.value
  if (!current.meta.public) router.push(decide(current, false) as Exclude<ReturnType<typeof decide>, true>)
})
