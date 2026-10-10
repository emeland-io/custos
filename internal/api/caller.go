package api

import (
	"context"
	"net/http"

	"github.com/emeland-io/custos/internal/gitrepo"
)

// Caller is who a request is made by: the identity the authentication
// middleware (package auth) resolved from the request's session cookie or
// personal access token. Package api names it through this interface
// rather than auth.Principal because auth registers its endpoints on an
// *API and so imports this package.
type Caller interface {
	// Signature is the caller as the author of the commits their request
	// causes.
	Signature() gitrepo.Signature
	// Has reports whether the caller holds role ("admin", "task-author",
	// "processor-author" or "engineer"). workspaceID matters only for
	// "engineer"; an empty workspaceID asks whether the caller is engineer
	// of any workspace.
	Has(role, workspaceID string) bool
}

type callerKey struct{}

// WithCaller returns ctx carrying c. The authentication middleware calls
// it, and tests that serve the API without the middleware.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerOf returns the caller of r, or ok=false when the request carries
// no valid credential.
func CallerOf(r *http.Request) (Caller, bool) {
	c, ok := r.Context().Value(callerKey{}).(Caller)
	return c, ok && c != nil
}

// RequireRole wraps next so that it answers 401 when the request has no
// caller and 403 when the caller lacks role, before next runs. For
// "engineer", workspaceIDParam names the path value that holds the
// workspace id the role must cover (usually "id"); for the other roles it
// is empty. The answers carry no WWW-Authenticate header, so a browser
// never shows its own password dialog.
func RequireRole(role, workspaceIDParam string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := CallerOf(r)
		if !ok {
			WriteError(w, errNotSignedIn())
			return
		}
		ws := ""
		if workspaceIDParam != "" {
			ws = r.PathValue(workspaceIDParam)
		}
		if !c.Has(role, ws) {
			if ws != "" {
				WriteError(w, errorf(http.StatusForbidden, "this needs the %s role for workspace %s", role, ws))
				return
			}
			WriteError(w, errorf(http.StatusForbidden, "this needs the %s role", role))
			return
		}
		next(w, r)
	}
}

// errNotSignedIn is the 401 of a request without a caller.
func errNotSignedIn() error {
	return errorf(http.StatusUnauthorized,
		`not signed in: sign in at /auth/login, or send a personal access token as "Authorization: Bearer <token>"`)
}
