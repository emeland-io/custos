// api/client.ts is the only HTTP client this project uses: a thin, typed
// wrapper around fetch. Same-origin, session-cookie authenticated
// (credentials: "include"); in token-only mode it also attaches a bearer
// token from api/token.ts. No HTTP client library is added (Global
// Constraints).
import type { ErrorBody } from '../types/error'
import { getToken } from './token'

export class ApiError extends Error {
  readonly status: number
  readonly body: ErrorBody

  constructor(status: number, body: ErrorBody) {
    super(body.error || `request failed with status ${status}`)
    this.status = status
    this.body = body
  }
}

function authHeaders(): Record<string, string> {
  const token = getToken()
  return token ? { Authorization: `Bearer ${token}` } : {}
}

async function parseBody(res: Response): Promise<unknown> {
  const text = await res.text()
  return text ? JSON.parse(text) : undefined
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { ...authHeaders() }
  let requestBody: BodyInit | undefined
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    requestBody = JSON.stringify(body)
  }
  const res = await fetch(path, { method, headers, body: requestBody, credentials: 'include' })
  if (res.status === 204) return undefined as T
  const data = await parseBody(res)
  if (!res.ok) {
    throw new ApiError(res.status, (data as ErrorBody) ?? { error: res.statusText })
  }
  return data as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  del: <T>(path: string, body?: unknown) => request<T>('DELETE', path, body),

  // putBlob uploads raw bytes to POST /api/blobs: the server stores the
  // request body verbatim and hashes it (internal/api/blob.go's
  // postBlob) — there is no JSON envelope or multipart form here.
  async putBlob(file: Blob): Promise<{ sha256: string; size: number }> {
    const res = await fetch('/api/blobs', {
      method: 'POST',
      headers: authHeaders(),
      body: file,
      credentials: 'include',
    })
    const data = await parseBody(res)
    if (!res.ok) {
      throw new ApiError(res.status, (data as ErrorBody) ?? { error: res.statusText })
    }
    return data as { sha256: string; size: number }
  },
}
