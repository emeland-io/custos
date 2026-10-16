// Mirrors internal/runs/record.go's Record.
//
// Named RunRecord, not Record, so it never shadows TypeScript's built-in
// Record<K, V> utility type in a file that uses both.

export type RunState = 'queued' | 'running' | 'succeeded' | 'failed'
export type RunOutcome = 'proposed' | 'unchanged' | ''

export interface RunRecord {
  id: string
  workspace: string
  task: { id: string; version: string }
  answer_path: string
  answer_blob: string
  answer_commit: string
  processor: string
  image: string
  digest: string
  key: string
  reason: string
  retry_of?: string
  state: RunState
  outcome?: RunOutcome
  branch?: string
  error?: string
  queued: string
  started?: string
  finished?: string
}
