import { defineStore } from 'pinia'
import { api, ApiError } from '../api/client'
import { setToken, clearToken } from '../api/token'
import type { Principal, AuthConfig } from '../types/auth'

export type AuthMode = 'loading' | 'authenticated' | 'unauthenticated' | 'token-only'

let initPromise: Promise<void> | null = null

export const useAuthStore = defineStore('auth', {
  state: () => ({
    principal: null as Principal | null,
    mode: 'loading' as AuthMode,
  }),
  actions: {
    // init loads GET /auth/config first (always 200, unauthenticated) to
    // learn whether OIDC is configured, then GET /auth/me for the caller's
    // identity. /auth/me always answers 401 when there is no caller,
    // whether or not OIDC is on — it never signals token-only mode by
    // status code (architecture note ruling 18, added during cross-plan
    // review: 4a's own endpoint table only ever documented 401 here, which
    // disagreed with an earlier draft of this plan that read a 404 as the
    // token-only signal; /auth/config is the fix, not a change to this
    // endpoint's semantics).
    async init(): Promise<void> {
      let oidc = true
      try {
        oidc = (await api.get<AuthConfig>('/auth/config')).oidc
      } catch {
        // /auth/config itself failing is unexpected; default to assuming
        // OIDC is configured, which sends an unauthenticated user to the
        // real /auth/login redirect (the safer of the two failure modes).
      }
      try {
        this.principal = await api.get<Principal>('/auth/me')
        this.mode = 'authenticated'
      } catch (err) {
        this.principal = null
        if (!(err instanceof ApiError)) throw err
        this.mode = oidc ? 'unauthenticated' : 'token-only'
      }
    },

    // ensureInit runs init() exactly once per page load, however many
    // callers (App.vue's onMounted, the router guard) ask for it.
    ensureInit(): Promise<void> {
      if (!initPromise) initPromise = this.init()
      return initPromise
    },

    // login starts a real OIDC redirect: a full page navigation, not a
    // client-side route (GET /auth/login 302s to the IdP).
    login(): void {
      window.location.href = '/auth/login'
    },

    // loginWithToken is the token-only-mode fallback: it stores the
    // pasted personal access token and re-runs init() so it is sent as
    // Authorization: Bearer on the next /auth/me.
    async loginWithToken(token: string): Promise<void> {
      setToken(token)
      await this.init()
      if (this.mode !== 'authenticated') {
        clearToken()
        throw new Error('that token was not accepted')
      }
    },

    async logout(): Promise<void> {
      try {
        await api.post('/auth/logout')
      } finally {
        clearToken()
        this.principal = null
        this.mode = 'unauthenticated'
        window.location.href = '/login'
      }
    },

    // has mirrors internal/auth.Roles.Has (admin implies every role
    // everywhere; engineer is scoped to workspaceId or "*") for UI
    // gating only — the server remains the actual authority on every
    // request regardless of what this returns.
    has(role: 'admin' | 'task-author' | 'processor-author' | 'engineer', workspaceId?: string): boolean {
      const roles = this.principal?.roles
      if (!roles) return false
      if (roles.admin) return true
      switch (role) {
        case 'admin':
          return false
        case 'task-author':
          return roles.task_author
        case 'processor-author':
          return roles.processor_author
        case 'engineer':
          return !!workspaceId && (roles.engineer.includes('*') || roles.engineer.includes(workspaceId))
      }
    },
  },
})
