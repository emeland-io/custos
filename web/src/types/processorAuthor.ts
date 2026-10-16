// Hand-written mirrors (ruling 4.4) of the processor, run and dry-run JSON
// used by the Processor-Author views (plan 4d). Sources of truth:
// internal/runs/record.go (Record, State, Outcome), internal/runs/dryrun.go
// (ProcessorInfo, DryRunReport, Sample), internal/runs/json.go (ItemJSON),
// internal/workspace/document.go (Verification), internal/api/workspaces.go
// (workspaceJSON).
//
// These duplicate web/src/types/runs.ts's RunRecord/RunState/RunOutcome and
// types/proposals.ts's Verification/ItemAction/ProposalItem under the same
// or similar names: plan 4c wrote its own hand-written mirrors for the
// Engineer persona's views, and this plan (4d) keeps its own beside them
// rather than importing across persona boundaries, per the plan's file
// list. Verified against the real internal/runs/*.go during this task —
// one difference from types/runs.ts worth flagging: that file's RunOutcome
// is `'proposed' | 'unchanged' | ''`, while runs.Outcome's own doc comment
// says it is "absent while queued/running and for failed runs", i.e. the
// JSON field is omitted (via `json:"outcome,omitempty"`), not sent as "" —
// this file's RunOutcome (below) is therefore `'proposed' | 'unchanged'`
// with the field itself marked optional on RunRecord, matching the Go
// struct tag exactly.
import type { TaskRef } from './taskAuthor'

/** runs.ProcessorInfo: GET /api/processors */
export interface ProcessorInfo {
  name: string
  image: string // digest-pinned reference, <name>@sha256:<hex>
  digest: string
  timeout: string // "60s" when processors.yaml leaves it out
  network: boolean
  secrets: string[]
  bound_tasks: string[] // catalog task UUIDs
}

/** runs.State */
export type RunState = 'queued' | 'running' | 'succeeded' | 'failed'

/** runs.Outcome; absent while queued/running and for failed runs */
export type RunOutcome = 'proposed' | 'unchanged'

/** runs.Record: GET /api/workspaces/{id}/runs (newest first) */
export interface RunRecord {
  id: string
  workspace: string
  task: TaskRef // answered task and the answer's task_version
  answer_path: string
  answer_blob: string
  answer_commit: string
  processor: string // processor name
  image: string
  digest: string
  key: string
  reason: string // "answer", "digest", "binding", "retry", "merge", "start-up"
  retry_of?: string
  state: RunState
  outcome?: RunOutcome
  branch?: string
  error?: string
  queued: string // RFC 3339
  started?: string
  finished?: string
}

/** api.workspaceJSON: GET /api/workspaces */
export interface WorkspaceSummary {
  id: string
  pin: string
  frozen: boolean
}

/** workspace.Verification */
export interface Verification {
  status: string
  signers?: string[]
}

/** match.Action */
export type ItemAction = 'added' | 'new-version' | 'unchanged' | 'removed'

/** runs.ItemJSON */
export interface ProposalItem {
  kind: 'task' | 'document'
  match_key: string
  action: ItemAction
  id: string
  from?: string
  to?: string
  title: string
  verification?: Verification
}

/** runs.Sample */
export interface DryRunSample {
  workspace: string
  task: string
  items: ProposalItem[]
}

/** The states a dry run reports (runs.Running, Succeeded, Failed). */
export type DryRunState = 'running' | 'succeeded' | 'failed'

/** runs.DryRunReport: GET /api/dry-runs/{id} */
export interface DryRunReport {
  state: DryRunState
  runs: number
  failed: number
  proposals: number
  workspaces: number
  samples: DryRunSample[] // at most 5
  error?: string // why the dry run itself failed
}

/** 202 body of POST /api/processors/{name}/dry-run */
export interface DryRunStarted {
  id: string
}
