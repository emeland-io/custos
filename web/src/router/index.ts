import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('../views/LoginView.vue'), meta: { public: true } },
    // "/" and "/w/:id" share DashboardView.vue: without an id it renders
    // a workspace picker, with one it renders that workspace's dashboard
    // (the architecture note's route table does not list a root route;
    // see this plan's "Decisions" §2).
    { path: '/', name: 'dashboard-root', component: () => import('../views/engineer/DashboardView.vue') },
    { path: '/w/:id', name: 'dashboard', component: () => import('../views/engineer/DashboardView.vue') },
    { path: '/w/:id/tasks/:taskId', name: 'answer', component: () => import('../views/engineer/AnswerView.vue') },
    { path: '/w/:id/proposals', name: 'proposals', component: () => import('../views/engineer/ProposalsView.vue') },
    { path: '/w/:id/book', name: 'book', component: () => import('../views/engineer/BookView.vue') },
  ],
})

// beforeEach ensures the auth store is initialised exactly once, then
// redirects: an unauthenticated principal with OIDC configured goes to
// the real backend login (a full page navigation, not a client route —
// architecture note, Plan 4c); in token-only mode (no /auth/login to
// redirect to — it 404s per Plan 4a) it goes to the SPA's own /login
// route instead, which shows the paste-a-token fallback.
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  await auth.ensureInit()
  if (to.meta.public) return true
  if (auth.mode === 'authenticated') return true
  if (auth.mode === 'token-only') {
    return to.name === 'login' ? true : { name: 'login' }
  }
  window.location.href = '/auth/login'
  return false
})

export default router
