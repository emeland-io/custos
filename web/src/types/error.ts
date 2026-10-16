// Mirrors internal/api/response.go's problemJSON/errorJSON.

export interface Problem {
  path: string
  rule: string
  message: string
}

export interface ErrorBody {
  error: string
  problems?: Problem[]
}
