// api/token.ts holds the personal access token LoginView's token-only-mode
// fallback pastes in. Normal web sessions never use this (architecture
// note: "there is no token entry in the normal web login flow"). It is a
// plain module, not a Pinia store, so api/client.ts can read it without
// importing stores/auth.ts, which itself imports client.ts.
const STORAGE_KEY = 'custos-token'

let token: string | null = (() => {
  try {
    return sessionStorage.getItem(STORAGE_KEY)
  } catch {
    return null // a private window or blocked storage: re-paste after reload
  }
})()

export function getToken(): string | null {
  return token
}

export function setToken(t: string): void {
  token = t
  try {
    sessionStorage.setItem(STORAGE_KEY, t)
  } catch {
    // ignore: the token still works for this page's lifetime
  }
}

export function clearToken(): void {
  token = null
  try {
    sessionStorage.removeItem(STORAGE_KEY)
  } catch {
    // ignore
  }
}
