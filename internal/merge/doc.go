// Package merge forks workspaces and merges branches into a workspace's
// main branch with custos-aware conflict handling (spec §4.4).
package merge

import "errors"

// ErrInvalid reports a malformed fork or merge request: a bad workspace id,
// branch name or resolution. The REST API answers it with 400.
var ErrInvalid = errors.New("invalid request")

const (
	mainRef    = "refs/heads/main"
	configPath = "custos.yaml"
)
