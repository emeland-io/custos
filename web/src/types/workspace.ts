// Mirrors internal/api/workspaces.go's workspaceJSON.

export interface Workspace {
  id: string
  pin: string
  frozen: boolean
}
