package auth

import (
	"net/http"
	"strings"

	"github.com/emeland-io/custos/internal/api"
)

// FromRequest returns the caller Middleware resolved for r. It is the
// architecture note's Principal(r), renamed because package auth cannot
// declare both a type and a function named Principal (Global Constraints).
// It does not read a context key of its own: it asks api.CallerOf — the
// only thing that ever puts a caller there is Middleware, via
// api.WithCaller — and type-asserts the result back to Principal.
func FromRequest(r *http.Request) (Principal, bool) {
	c, ok := api.CallerOf(r)
	if !ok {
		return Principal{}, false
	}
	p, ok := c.(Principal)
	return p, ok
}

// Require wraps next so it 401s (no caller) or 403s (caller present, role
// missing) before next runs. It is auth.Require(role, next) == api.
// RequireRole(role, "", next); the architecture note's name for roles that
// are not scoped to a workspace.
func Require(role string, next http.HandlerFunc) http.HandlerFunc {
	return api.RequireRole(role, "", next)
}

// RequireWorkspace is Require for the engineer role scoped to the
// workspace named by r.PathValue(workspaceIDParam) (usually "id").
func RequireWorkspace(workspaceIDParam string, next http.HandlerFunc) http.HandlerFunc {
	return api.RequireRole(RoleEngineer, workspaceIDParam, next)
}

// Middleware resolves the caller of every request that does not match one
// of excludedPrefixes (an exact path, or a prefix ending in "/") and, when
// it finds one, stores it for FromRequest and api.CallerOf/api.Author to
// read. It never itself answers a request — an unresolved caller is simply
// absent from the context, and Require/RequireWorkspace/api.Author give
// the 401 — except that an unsafe (non-GET/HEAD/OPTIONS) request whose only
// credential came from the cookie or Basic auth (both ambient: a browser
// attaches them without the page asking) and that http.
// CrossOriginProtection judges cross-origin is treated as if it had no
// credential at all (Review Focus 2). A credential read from an
// Authorization: Bearer header is not ambient — a cross-site page cannot
// attach an arbitrary header to a simple request — so it is exempt from
// that check.
func (m *Manager) Middleware(excludedPrefixes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range excludedPrefixes {
				// p == "/" is deliberately an *exact* match only (the
				// first clause), never a prefix match: every request
				// path starts with "/", so letting a bare "/" act as a
				// prefix (it ends in "/", so the second clause would
				// otherwise accept it) bypassed credential resolution for
				// every single request to every path — found empirically
				// while applying this plan to a scratch copy and running
				// internal/server's real end-to-end tests
				// (TestAPINeedsSignIn, TestPushNeedsRole) against a real
				// HTTP server: both failed because no caller was ever
				// resolved, for any request, once "/" was in
				// excludedPrefixes — exactly the value cmd/custos/serve.go
				// passes in production. This was a critical bug: as
				// originally written, authentication was a no-op for
				// every request to a real custos serve. Only a longer
				// directory-style prefix (none in current use besides
				// "/") is still eligible for prefix matching.
				if r.URL.Path == p || (p != "/" && strings.HasSuffix(p, "/") && strings.HasPrefix(r.URL.Path, p)) {
					next.ServeHTTP(w, r)
					return
				}
			}
			if p, ambient, ok := m.resolve(r); ok {
				unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
				if !ambient || !unsafe || m.cop.Check(r) == nil {
					r = r.WithContext(api.WithCaller(r.Context(), p))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// resolve extracts a credential from r, in order: Authorization: Bearer
// (explicit, not ambient), Authorization: Basic with the token as the
// password (what git sends; ambient — a browser that cached it resends it
// to the same origin on its own), then the session cookie (ambient).
func (m *Manager) resolve(r *http.Request) (p Principal, ambient bool, ok bool) {
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
		p, ok = m.lookupToken(strings.TrimPrefix(v, "Bearer "))
		return p, false, ok
	}
	if _, pass, basicOK := r.BasicAuth(); basicOK {
		p, ok = m.lookupToken(pass)
		return p, true, ok
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return Principal{}, false, false
	}
	claims, err := m.verify(c.Value, "session")
	if err != nil || claims.Issuer != m.issuer {
		return Principal{}, false, false
	}
	users, err := m.loadUsers()
	if err != nil {
		return Principal{}, false, false
	}
	u, ok := users[claims.Email]
	if !ok {
		return Principal{}, false, false
	}
	return m.principal(claims.Email, u, claims.Subject), true, true
}
