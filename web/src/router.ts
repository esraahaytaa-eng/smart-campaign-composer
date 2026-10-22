import { createRouter, createWebHistory, type RouterHistory } from 'vue-router'
import { setUnauthorizedHandler } from './api'
import { decide } from './guard'
import type { Key } from './i18n'
import { clearSession, loadSession } from './session'
import AppLayout from './layouts/AppLayout.vue'
import LoginView from './views/LoginView.vue'

declare module 'vue-router' {
  interface RouteMeta {
    public?: boolean
    title?: Key // the page's name in the dictionaries (D44)
  }
}

export function createAppRouter(history: RouterHistory = createWebHistory()) {
  const router = createRouter({
    history,
    routes: [
      { path: '/login', name: 'login', component: LoginView, meta: { public: true, title: 'page.signIn' } },
      {
        path: '/',
        component: AppLayout,
        children: [
          { path: '', name: 'dashboard', component: () => import('./views/DashboardView.vue'), meta: { title: 'page.dashboard' } },
          { path: 'campaigns', name: 'campaigns', component: () => import('./views/CampaignsView.vue'), meta: { title: 'page.campaigns' } },
          { path: 'campaigns/new', name: 'new-campaign', component: () => import('./views/NewCampaignView.vue'), meta: { title: 'page.newCampaign' } },
          { path: 'campaigns/:id(\\d+)', name: 'campaign', component: () => import('./views/CampaignView.vue'), meta: { title: 'page.campaign' } },
          { path: 'campaigns/:id(\\d+)/content', name: 'campaign-content', component: () => import('./views/ContentView.vue'), meta: { title: 'page.content' } },
          { path: 'campaigns/:id(\\d+)/review', name: 'campaign-review', component: () => import('./views/ReviewView.vue'), meta: { title: 'page.review' } },
          { path: ':pathMatch(.*)*', name: 'not-found', component: () => import('./views/NotFoundView.vue'), meta: { title: 'page.notFound' } },
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
