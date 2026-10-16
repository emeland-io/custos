// Mirrors internal/api/read.go's statusJSON family and internal/task.AnswerType.

// internal/task/task.go's AnswerType constants.
export type AnswerType = 'markdown' | 'text' | 'timestamp' | 'path' | 'url' | 'choice'

export interface Attachment {
  name: string
  sha256: string
  media_type: string
  available: boolean
}

export interface Answer {
  task: string
  task_version: string
  type: AnswerType
  value: string
  body: string
  attachments: Attachment[]
}

// internal/status.State's values.
export type TaskState = 'unanswered' | 'answered' | 'pending-update'

export interface TaskStatus {
  id: string
  version: string // current version
  title: string
  answer_type: AnswerType
  choices?: string[] // present when answer_type is 'choice'; plan 4b, Task 8
  generated: boolean
  state: TaskState
  answer: Answer | null
  merged: Answer[]
  in_effect: Answer[]
}

export interface BookGroup {
  slug: string
  title: string
}

export interface BookEntry {
  depth: number
  group?: BookGroup
  task?: string
}

export interface Status {
  workspace: string
  pin: string
  tasks: TaskStatus[] // book order
  book: BookEntry[]
}
