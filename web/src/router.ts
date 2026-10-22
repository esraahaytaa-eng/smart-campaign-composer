import { createRouter, createWebHistory, type RouterHistory } from 'vue-router'
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

export function createAppRouter(history: RouterHistory = createWebHistory()) {
  const router = createRouter({
    history,
    routes: [
      { path: '/login', name: 'login', component: LoginView, meta: { public: true, title: 'Sign in' } },
      {
        path: '/',
        component: AppLayout,
        children: [
          { path: '', name: 'dashboard', component: () => import('./views/DashboardView.vue'), meta: { title: 'Dashboard' } },
          { path: 'campaigns', name: 'campaigns', component: () => import('./views/CampaignsView.vue'), meta: { title: 'Campaigns' } },
          { path: 'campaigns/new', name: 'new-campaign', component: () => import('./views/NewCampaignView.vue'), meta: { title: 'New campaign' } },
          { path: 'campaigns/:id(\\d+)', name: 'campaign', component: () => import('./views/CampaignView.vue'), meta: { title: 'Campaign' } },
          { path: ':pathMatch(.*)*', name: 'not-found', component: () => import('./views/NotFoundView.vue'), meta: { title: 'Page not found' } },
        ],
      },
    ],
  })

  router.beforeEach(async (to) => decide(to, (await loadSession()) !== null))

  // Any API call that finds the session expired sends the marketer to sign in
  // again and brings them back afterwards.
  setUnauthorizedHandler(() => {
    clearSession()
    const current = router.currentRoute.value
    if (!current.meta.public) router.push(decide(current, false) as Exclude<ReturnType<typeof decide>, true>)
  })
  return router
}
