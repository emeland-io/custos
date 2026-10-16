// Mirrors internal/api/catalog.go's taskVersionJSON/taskHistoryJSON.

import type { AnswerType } from './status'

// internal/task/task.go's Ref.
export interface Ref {
  id: string
  version: string
}

export interface TaskVersion {
  id: string
  version: string
  title: string
  answer_type: AnswerType
  choices?: string[]
  previous: Ref[]
  body: string
  processor?: string
}

export interface TaskHistory {
  id: string
  current: string // "" when superseded
  superseded: boolean
  processor?: string
  versions: TaskVersion[] // lowest first
}
