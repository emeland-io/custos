// Mirrors internal/auth/auth.go's Roles and the bodies of GET /auth/me
// (internal/auth/http.go's handleMe) and GET /auth/config (handleConfig,
// architecture note ruling 18 — the token-only-mode signal stores/auth.ts
// needs).

export interface Roles {
  admin: boolean
  task_author: boolean
  processor_author: boolean
  engineer: string[] // workspace ids, or "*" for every workspace
}

export interface Principal {
  name: string
  email: string
  roles: Roles
}

export interface AuthConfig {
  oidc: boolean
}
