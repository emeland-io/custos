// Hand-written mirrors (ruling 4.4) of the catalog and catalog-draft JSON
// used by the Task-Author views (plan 4d). Sources of truth:
// internal/api/catalog.go (catalogJSON, groupJSON, childJSON,
// taskVersionJSON, taskHistoryJSON), internal/api/response.go (problemJSON),
// internal/merge/http.go (conflictJSON, conflictBody, resolutionJSON) and
// internal/catalogdraft (plan 4b: Draft and the draft endpoints' bodies).
//
// These duplicate web/src/types/catalog.ts's Ref/TaskVersion/TaskHistory
// under different names (TaskRef/CatalogTaskVersion/TaskHistory) rather
// than importing them: plan 4c's types/catalog.ts was written for the
// Engineer persona's own views and this plan (4d) keeps its own
// hand-written mirrors beside it, per the plan's own file list ("added as
// new files beside plan 4c's in web/src/types/"). Verified against the
// real internal/api/catalog.go during this task; the shapes are identical.

/** task.Ref */
export interface TaskRef {
  id: string
  version: string
}

/** task.AnswerType */
export type AnswerType = 'markdown' | 'text' | 'timestamp' | 'path' | 'url' | 'choice'

/** The values of AnswerType, in the order of internal/task/task.go's answerTypes. */
export const ANSWER_TYPES: readonly AnswerType[] = ['markdown', 'text', 'timestamp', 'path', 'url', 'choice']

/** api.taskVersionJSON */
export interface CatalogTaskVersion {
  id: string
  version: string
  title: string
  answer_type: AnswerType
  choices?: string[]
  previous: TaskRef[]
  body: string
  processor?: string // bound processor name; only set in GET /api/catalog's task list
}

/** api.childJSON: exactly one of group or task is set. */
export interface CatalogChild {
  group?: CatalogGroup
  task?: string
}

/** api.groupJSON (sub-groups are nested, not referenced by slug) */
export interface CatalogGroup {
  slug: string
  title: string
  children: CatalogChild[]
}

/** api.catalogJSON: GET /api/catalog, and the "catalog" of a draft (plan 4b). */
export interface CatalogJSON {
  commit: string
  groups: CatalogGroup[] // top-level groups in groups/index.yaml order
  tasks: CatalogTaskVersion[] // the current version of every task that has one
}

/** api.taskHistoryJSON: GET /api/catalog/tasks/{id} */
export interface TaskHistory {
  id: string
  current: string // "" when superseded
  superseded: boolean
  processor?: string
  versions: CatalogTaskVersion[] // lowest first
}

/** api.problemJSON */
export interface CatalogProblem {
  path: string
  rule: string
  message: string
}

/** gitrepo.Signature as plan 4b encodes it. */
export interface Signature {
  name: string
  email: string
}

/** catalogdraft.Draft (plan 4b) */
export interface Draft {
  id: string
  branch: string
  title: string
  author: Signature
  base: string // catalog main's commit when the draft was created
  commit: string // current head of the draft branch
}

/** GET /api/catalog/drafts/{id} (plan 4b) */
export interface DraftDetail {
  draft: Draft
  catalog: CatalogJSON
  problems: CatalogProblem[]
}

/** semver.Step */
export type SemverStep = 'patch' | 'minor' | 'major'

/** Body of PUT /api/catalog/drafts/{id}/tasks/{task-id}; choices only for answer_type "choice". */
export interface NewVersionRequest {
  step: SemverStep
  title: string
  body: string
  answer_type: AnswerType
  choices?: string[]
}

/** One child in PUT …/groups/{slug}: exactly one of group (a slug) or task (a UUID). */
export interface GroupChildRef {
  group?: string
  task?: string
}

/** Body of PUT /api/catalog/drafts/{id}/groups/{slug} */
export interface GroupRequest {
  title: string
  children: GroupChildRef[]
}

/** Body of PUT /api/catalog/drafts/{id}/groups-index */
export interface GroupsIndexRequest {
  groups: string[]
}

/** merge.SideOurs/SideTheirs/SideContent */
export type ResolutionSide = 'ours' | 'theirs' | 'content'

/** merge.resolutionJSON: content is base64 and is sent only with side "content". */
export interface ResolutionJSON {
  side: ResolutionSide
  content?: string
}

/** merge.conflictJSON: ours = catalog main, theirs = the draft; base64, null when that side deleted the file. */
export interface ConflictJSON {
  path: string
  kind: string
  ours: string | null
  theirs: string | null
}

/** merge.conflictBody: the 409 body of a publish that needs resolutions. */
export interface ConflictBody {
  error: string
  conflicts: ConflictJSON[]
}

/** A ConflictJSON with ours/theirs decoded as UTF-8 text: MergeToolView's input. */
export interface DecodedConflict {
  path: string
  kind: string
  ours: string | null
  theirs: string | null
}

/** 200 body of POST /api/catalog/drafts/{id}/publish */
export interface PublishResponse {
  commit: string
}
