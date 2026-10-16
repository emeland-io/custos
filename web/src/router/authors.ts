// Routes and role helper of the Task-Author and Processor-Author views
// (plan 4d). router/index.ts (plan 4c) spreads authorRoutes into its route
// list; App.vue links to /catalog/drafts and /processors.
import { computed, type ComputedRef } from 'vue'
import type { RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '../stores/auth'

export type AuthorRole = 'task_author' | 'processor_author'

/**
 * Whether the signed-in principal has role; admin implies every role
 * (§6.4). Views render a "needs the … role" message and make no request
 * when this is false, so a typed-in URL never produces a 403.
 */
export function useAuthorRole(role: AuthorRole): ComputedRef<boolean> {
  const auth = useAuthStore()
  return computed(() => {
    const p = auth.principal
    return p != null && (p.roles.admin || p.roles[role])
  })
}

/** A route param or query value as one string; '' when absent. */
export function routeParam(v: string | null | (string | null)[] | undefined): string {
  if (typeof v === 'string') return v
  if (Array.isArray(v)) return v[0] ?? ''
  return ''
}

export const authorRoutes: RouteRecordRaw[] = [
  {
    path: '/catalog/drafts',
    name: 'catalog-drafts',
    component: () => import('../views/taskauthor/DraftsView.vue'),
  },
  {
    path: '/catalog/drafts/:draftId',
    name: 'catalog-draft',
    component: () => import('../views/taskauthor/DraftsView.vue'),
  },
  {
    path: '/catalog/drafts/:draftId/groups/:slug',
    name: 'catalog-draft-group',
    component: () => import('../views/taskauthor/GroupTreeView.vue'),
  },
  {
    path: '/catalog/drafts/:draftId/tasks/:taskId',
    name: 'catalog-draft-task',
    component: () => import('../views/taskauthor/TaskEditorView.vue'),
  },
  {
    path: '/processors',
    name: 'processors',
    component: () => import('../views/processorauthor/RegistryView.vue'),
  },
  {
    path: '/processors/:name/runs',
    name: 'processor-runs',
    component: () => import('../views/processorauthor/RunsView.vue'),
  },
  // plan 4d: later tasks insert their routes above this line
]
