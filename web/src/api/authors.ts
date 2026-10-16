// Endpoint functions for the Task-Author and Processor-Author views
// (plan 4d). Every JSON call goes through the shared client's `api`
// object (plan 4c's actual export: api.get/post/put/del, not a standalone
// request() function as an earlier draft of this plan assumed — fixed
// during cross-plan review once 4c's api/client.ts was readable); this
// file is the only place plan 4d touches that client. getRunLog alone
// calls fetch itself, because its endpoint answers text/plain, with the
// client's same-origin, credentials: 'include' setup — and, since 4c's
// client also attaches an Authorization: Bearer header in token-only mode
// (see api/token.ts), getRunLog attaches the same header itself so the
// log request is not left unauthenticated in that mode (also found during
// cross-plan review; flagged by this plan's own "Decisions" as an open
// question, now resolved). This mirrors BookView.vue's fetchBook helper
// (plan 4c, Task 12), which hit the identical problem for its own
// text/markdown and text/html endpoints and was fixed with this exact
// pattern — re-verified against BookView.vue's real code during this task.
//
// PUT /api/catalog/drafts/{id}/processors (editing processors.yaml) is a
// real, registered endpoint (internal/catalogdraft/http.go) that this file
// deliberately has no function for: the plan's "Decisions beyond the
// architecture note" records this as a scoped-out gap (spec §6.1/§5.7 give
// the Processor-Author only registry list, run history/logs and dry run —
// no registry editor), not an oversight.
import { ApiError, api } from './client'
import { getToken } from './token'
import type {
  CatalogJSON,
  CatalogProblem,
  DecodedConflict,
  Draft,
  DraftDetail,
  GroupRequest,
  GroupsIndexRequest,
  NewVersionRequest,
  PublishResponse,
  ResolutionJSON,
  TaskHistory,
} from '../types/taskAuthor'
import type {
  DryRunReport,
  DryRunStarted,
  ProcessorInfo,
  RunRecord,
  WorkspaceSummary,
} from '../types/processorAuthor'

const seg = encodeURIComponent

/* ---------- catalog main (read-only; internal/api/catalog.go) ---------- */

export function getCatalog(): Promise<CatalogJSON> {
  return api.get<CatalogJSON>('/api/catalog')
}

export function getCatalogTask(id: string): Promise<TaskHistory> {
  return api.get<TaskHistory>(`/api/catalog/tasks/${seg(id)}`)
}

/* ---------- catalog drafts (plan 4b) ---------- */

export async function listDrafts(): Promise<Draft[]> {
  return (await api.get<Draft[] | null>('/api/catalog/drafts')) ?? []
}

export function createDraft(title: string): Promise<Draft> {
  return api.post<Draft>('/api/catalog/drafts', { title })
}

export async function getDraft(id: string): Promise<DraftDetail> {
  const d = await api.get<DraftDetail>(`/api/catalog/drafts/${seg(id)}`)
  // Go encodes empty slices as null unless the handler says otherwise.
  return {
    draft: d.draft,
    catalog: { commit: d.catalog.commit, groups: d.catalog.groups ?? [], tasks: d.catalog.tasks ?? [] },
    problems: d.problems ?? [],
  }
}

export async function deleteDraft(id: string): Promise<void> {
  await api.del<unknown>(`/api/catalog/drafts/${seg(id)}`)
}

export async function putTaskVersion(draftId: string, taskId: string, body: NewVersionRequest): Promise<void> {
  await api.put<unknown>(`/api/catalog/drafts/${seg(draftId)}/tasks/${seg(taskId)}`, body)
}

export async function putGroup(draftId: string, slug: string, body: GroupRequest): Promise<void> {
  await api.put<unknown>(`/api/catalog/drafts/${seg(draftId)}/groups/${seg(slug)}`, body)
}

export async function putGroupsIndex(draftId: string, groups: string[]): Promise<void> {
  const body: GroupsIndexRequest = { groups }
  await api.put<unknown>(`/api/catalog/drafts/${seg(draftId)}/groups-index`, body)
}

/** Publishes a draft; resolutions is empty for the first attempt. */
export function publishDraft(draftId: string, resolutions: Record<string, ResolutionJSON>): Promise<PublishResponse> {
  const body = Object.keys(resolutions).length > 0 ? { resolutions } : {}
  return api.post<PublishResponse>(`/api/catalog/drafts/${seg(draftId)}/publish`, body)
}

/* ---------- processors, runs, dry runs (internal/runs/api.go) ---------- */

export async function listProcessors(): Promise<ProcessorInfo[]> {
  return (await api.get<ProcessorInfo[] | null>('/api/processors')) ?? []
}

export async function listWorkspaces(): Promise<WorkspaceSummary[]> {
  return (await api.get<WorkspaceSummary[] | null>('/api/workspaces')) ?? []
}

export async function listRuns(workspace: string): Promise<RunRecord[]> {
  return (await api.get<RunRecord[] | null>(`/api/workspaces/${seg(workspace)}/runs`)) ?? []
}

export function retryRun(workspace: string, run: string): Promise<RunRecord> {
  return api.post<RunRecord>(`/api/workspaces/${seg(workspace)}/runs/${seg(run)}/retry`)
}

/**
 * The run's log (the processor's stderr), as text. Calls fetch directly
 * (the endpoint answers text/plain, which the JSON client cannot return),
 * but still attaches the same Authorization: Bearer header client.ts uses
 * in token-only mode (api/token.ts's getToken()), so the log request is
 * not left unauthenticated under a pasted token — found during cross-plan
 * review once 4c's client.ts (which does attach that header) was readable,
 * and confirmed against BookView.vue's fetchBook, which hit the identical
 * problem for the book endpoints and was fixed with this exact pattern.
 */
export async function getRunLog(workspace: string, run: string): Promise<string> {
  const token = getToken()
  const headers: Record<string, string> = { Accept: 'text/plain' }
  if (token) headers.Authorization = `Bearer ${token}`
  const res = await fetch(`/api/workspaces/${seg(workspace)}/runs/${seg(run)}/log`, {
    credentials: 'include',
    headers,
  })
  const text = await res.text()
  if (!res.ok) {
    let msg = text
    try {
      const b: unknown = JSON.parse(text)
      if (isRecord(b) && typeof b.error === 'string') msg = b.error
    } catch {
      // not JSON: keep the text
    }
    throw new Error(`Loading the log failed (HTTP ${res.status}): ${msg}`)
  }
  return text
}

export function startDryRun(name: string, image: string): Promise<DryRunStarted> {
  return api.post<DryRunStarted>(`/api/processors/${seg(name)}/dry-run`, { image })
}

export function getDryRun(id: string): Promise<DryRunReport> {
  return api.get<DryRunReport>(`/api/dry-runs/${seg(id)}`)
}

/* ---------- base64 text (merge conflicts and resolutions carry []byte) ---------- */

export function decodeBase64Text(b64: string): string {
  const bin = atob(b64)
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return new TextDecoder().decode(bytes)
}

export function encodeBase64Text(text: string): string {
  const bytes = new TextEncoder().encode(text)
  let bin = ''
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]!)
  return btoa(bin)
}

/* ---------- errors ---------- */

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** The HTTP status of a failed request, or null when it never got one. */
export function statusOf(e: unknown): number | null {
  return e instanceof ApiError ? e.status : null
}

/** A sentence to show for a failed request. */
export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.status === 401) return 'You are not signed in, or your session has expired. Sign in again.'
    if (e.status === 403) return 'Your account does not have the role this action needs.'
    const body: unknown = e.body
    if (isRecord(body) && typeof body.error === 'string' && body.error !== '') return body.error
    if (typeof body === 'string' && body !== '') return `HTTP ${e.status}: ${body}`
    return `HTTP ${e.status}`
  }
  if (e instanceof Error) return e.message
  return String(e)
}

/** The validation problems of a 422 (or any error body that has them). */
export function problemsOf(e: unknown): CatalogProblem[] {
  if (!(e instanceof ApiError)) return []
  const body: unknown = e.body
  if (!isRecord(body) || !Array.isArray(body.problems)) return []
  const out: CatalogProblem[] = []
  for (const p of body.problems) {
    if (isRecord(p) && typeof p.path === 'string' && typeof p.rule === 'string' && typeof p.message === 'string') {
      out.push({ path: p.path, rule: p.rule, message: p.message })
    }
  }
  return out
}

/**
 * The decoded conflicts of a 409 conflictBody, or null when e is anything
 * else — including a 409 without conflicts (main moved during the
 * compare-and-swap), which the caller treats as a plain error.
 */
export function conflictsOf(e: unknown): DecodedConflict[] | null {
  if (!(e instanceof ApiError) || e.status !== 409) return null
  const body: unknown = e.body
  if (!isRecord(body) || !Array.isArray(body.conflicts)) return null
  const out: DecodedConflict[] = []
  for (const c of body.conflicts) {
    if (!isRecord(c) || typeof c.path !== 'string') continue
    out.push({
      path: c.path,
      kind: typeof c.kind === 'string' ? c.kind : 'other',
      ours: typeof c.ours === 'string' ? decodeBase64Text(c.ours) : null,
      theirs: typeof c.theirs === 'string' ? decodeBase64Text(c.theirs) : null,
    })
  }
  return out.length > 0 ? out : null
}
