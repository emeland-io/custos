// Types and calls for the custos JSON API.

export interface Meta {
  id: string
  createdAt: string
  updatedAt: string
}

export interface Versioned {
  version: string
  previousVersions: string[]
  superseded: boolean
}

export interface Root extends Meta {
  displayName: string
  description: string
  entryNodeId: string
}

export interface RequiredAttestation {
  predicateType: string
  subjectName?: string
  requiredIdentities?: string[]
}

export interface Node extends Meta, Versioned {
  displayName: string
  parentId?: string
  description: string
  requiredAttestation: RequiredAttestation
  missingReason: string
}

export interface Leaf extends Meta, Versioned {
  parentId: string
  description: string
}

export interface Seed extends Meta {
  displayName: string
  description: string
  rootId: string
}

export interface Shoot extends Meta {
  seedId: string
  leafId: string
  content: string
  author: string
}

export type VerificationStatus = 'VERIFIED' | 'FAILED' | 'UNSIGNED' | 'UNVERIFIABLE'

export interface Subject {
  name?: string
  uri?: string
  digest?: Record<string, string>
}

export interface Verification {
  status: VerificationStatus
  identities?: string[]
  error?: string
  date: string
  requirementMet: boolean
  requirementErrors?: string[]
}

export interface Attestation extends Meta {
  seedId: string
  nodeId: string
  raw?: unknown
  format: string
  predicateType: string
  subjects: Subject[]
  verification: Verification
}

export interface TreeNode extends Node {
  children: TreeNode[]
  leaves: Leaf[]
}

export interface PathEntry {
  id: string
  displayName: string
}

export interface AttestationSummary {
  id: string
  nodeId: string
  predicateType: string
  status: VerificationStatus
  requirementMet: boolean
  createdAt: string
}

export interface Dangling {
  kind: string
  id: string
  field: string
  target: string
}

export interface SeedStatus {
  seed: Seed
  missingShoots: { leaf: Leaf; path: PathEntry[]; stale: Shoot[] }[]
  missingAttestations: {
    node: Node
    path: PathEntry[]
    stale: AttestationSummary[]
    rejected: AttestationSummary[]
  }[]
  orphaned: Dangling[]
  treeError?: string
  leaves: number
  answeredLeaves: number
  nodes: number
  attestedNodes: number
  percent: number
}

export interface Key {
  file: string
  identity: string
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

async function call<T>(method: string, path: string, body?: unknown, raw = false): Promise<T> {
  const init: RequestInit = { method, headers: {} }
  if (body !== undefined) {
    init.body = raw ? (body as string) : JSON.stringify(body)
    ;(init.headers as Record<string, string>)['Content-Type'] = 'application/json'
  }
  const res = await fetch('/api' + path, init)
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new ApiError(res.status, data.error ?? res.statusText)
  return data as T
}

export type NodeInput = Pick<Node, 'displayName' | 'description' | 'requiredAttestation' | 'missingReason'> & {
  parentId?: string
  version?: string
}
export type LeafInput = Pick<Leaf, 'parentId' | 'description'> & { version?: string }

export const api = {
  roots: () => call<Root[]>('GET', '/roots'),
  root: (id: string) => call<Root>('GET', `/roots/${id}`),
  createRoot: (r: Pick<Root, 'displayName' | 'description' | 'entryNodeId'>) => call<Root>('POST', '/roots', r),
  updateRoot: (id: string, r: Pick<Root, 'displayName' | 'description' | 'entryNodeId'>) =>
    call<Root>('PUT', `/roots/${id}`, r),
  deleteRoot: (id: string) => call<void>('DELETE', `/roots/${id}`),
  tree: (rootId: string) => call<TreeNode>('GET', `/roots/${rootId}/tree`),

  nodes: (all = false) => call<Node[]>('GET', `/nodes${all ? '?all=true' : ''}`),
  node: (id: string) => call<Node>('GET', `/nodes/${id}`),
  createNode: (n: NodeInput) => call<Node>('POST', '/nodes', n),
  updateNode: (id: string, n: NodeInput) => call<Node>('PUT', `/nodes/${id}`, n),
  deleteNode: (id: string) => call<void>('DELETE', `/nodes/${id}`),
  subtree: (id: string) => call<TreeNode>('GET', `/nodes/${id}/tree`),
  nodePath: (id: string) => call<Node[]>('GET', `/nodes/${id}/path`),
  nodeHistory: (id: string) => call<Node[]>('GET', `/nodes/${id}/history`),
  newNodeVersion: (id: string, n: NodeInput) => call<Node>('POST', `/nodes/${id}/versions`, n),

  leaves: (all = false) => call<Leaf[]>('GET', `/leaves${all ? '?all=true' : ''}`),
  leaf: (id: string) => call<Leaf>('GET', `/leaves/${id}`),
  createLeaf: (l: LeafInput) => call<Leaf>('POST', '/leaves', l),
  updateLeaf: (id: string, l: LeafInput) => call<Leaf>('PUT', `/leaves/${id}`, l),
  deleteLeaf: (id: string) => call<void>('DELETE', `/leaves/${id}`),
  leafPath: (id: string) => call<Node[]>('GET', `/leaves/${id}/path`),
  leafHistory: (id: string) => call<Leaf[]>('GET', `/leaves/${id}/history`),
  newLeafVersion: (id: string, l: LeafInput) => call<Leaf>('POST', `/leaves/${id}/versions`, l),

  seeds: () => call<Seed[]>('GET', '/seeds'),
  seed: (id: string) => call<Seed>('GET', `/seeds/${id}`),
  createSeed: (s: Pick<Seed, 'displayName' | 'description' | 'rootId'>) => call<Seed>('POST', '/seeds', s),
  updateSeed: (id: string, s: Pick<Seed, 'displayName' | 'description'>) => call<Seed>('PUT', `/seeds/${id}`, s),
  deleteSeed: (id: string) => call<void>('DELETE', `/seeds/${id}`),
  seedStatus: (id: string) => call<SeedStatus>('GET', `/seeds/${id}/status`),
  seedShoots: (id: string) => call<Shoot[]>('GET', `/seeds/${id}/shoots`),
  seedAttestations: (id: string) => call<Attestation[]>('GET', `/seeds/${id}/attestations`),
  uploadAttestation: (seedId: string, nodeId: string, json: string) =>
    call<Attestation>('POST', `/seeds/${seedId}/nodes/${nodeId}/attestations`, json, true),

  createShoot: (s: Pick<Shoot, 'seedId' | 'leafId' | 'content' | 'author'>) => call<Shoot>('POST', '/shoots', s),
  updateShoot: (id: string, s: Pick<Shoot, 'content' | 'author'>) => call<Shoot>('PUT', `/shoots/${id}`, s),
  deleteShoot: (id: string) => call<void>('DELETE', `/shoots/${id}`),

  attestation: (id: string) => call<Attestation>('GET', `/attestations/${id}`),
  deleteAttestation: (id: string) => call<void>('DELETE', `/attestations/${id}`),
  reverify: (id: string) => call<Attestation>('POST', `/attestations/${id}/verify`),

  keys: () => call<Key[]>('GET', '/keys'),
  reloadKeys: () => call<Key[]>('POST', '/keys/reload'),
  dangling: () => call<Dangling[]>('GET', '/dangling'),
}

export function shortId(id: string): string {
  return id.slice(0, 8)
}

export function formatDate(iso: string): string {
  return new Date(iso).toLocaleString()
}

// leafTitle returns the first line of a Leaf's description without
// heading markers.
export function leafTitle(l: Pick<Leaf, 'description'>): string {
  const line = l.description.split('\n').find((s) => s.trim()) ?? ''
  return line.replace(/^#+\s*/, '').trim() || 'Untitled task'
}
