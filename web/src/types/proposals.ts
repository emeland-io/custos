// Mirrors internal/runs/json.go's ProposalJSON/ItemJSON (processor output
// proposals) and internal/distribute/proposals.go+diff.go's
// PinProposal/Diff (catalog pin-update proposals). These are two genuinely
// different shapes from two different endpoint families.

import type { Ref } from './catalog'

// internal/workspace/document.go's Verification.
export interface Verification {
  status: 'verified' | 'failed' | 'unsigned'
  signers?: string[]
}

// internal/match/match.go's Action constants.
export type ItemAction = 'added' | 'new-version' | 'unchanged' | 'removed'

// internal/runs/json.go's ItemJSON.
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

// ProcessorProposal is a processor output proposal (phase 3d), on branch
// custos/proposal/<task-uuid>/<digest>. Mirrors internal/runs/json.go's
// ProposalJSON.
export interface ProcessorProposal {
  branch: string
  commit: string
  task: string
  digest?: string
  cascade: boolean
  items: ProposalItem[]
}

// internal/distribute/diff.go's Diff.
export interface Diff {
  new_versions: Ref[]
  added: Ref[]
  superseded: Ref[]
  groups_changed: boolean
  bindings_changed: boolean
}

// PinProposal is a catalog pin-update proposal (phase 2c), on branch
// custos/pin/<catalog-commit>. Mirrors internal/distribute/proposals.go's
// PinProposal.
export interface PinProposal {
  branch: string
  commit: string
  from: string
  to: string
  changes: Diff
}
