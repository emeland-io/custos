package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/emeland-io/custos/internal/api"
)

// maxAuthBody bounds the JSON bodies of these endpoints: a token's label
// and a role update are both short.
const maxAuthBody = 4 << 10

// Register adds /auth/* and the admin endpoints to a (full paths, not
// below /api/ for the /auth/* ones — see the architecture note and this
// plan's server wiring in Task 8). Call it before a.Handler(), even in
// token-only mode: only handleLogin/handleCallback 404 then, which they
// check themselves.
func Register(a *api.API, m *Manager) {
	a.Handle("GET /auth/login", m.handleLogin)
	a.Handle("GET /auth/callback", m.handleCallback)
	a.Handle("POST /auth/logout", m.handleLogout)
	a.Handle("GET /auth/config", m.handleConfig)
	a.Handle("GET /auth/me", requireSignedIn(m.handleMe))
	a.Handle("GET /auth/tokens", requireSignedIn(m.handleListTokens))
	a.Handle("POST /auth/tokens", requireSignedIn(m.handleCreateToken))
	a.Handle("DELETE /auth/tokens/{id}", requireSignedIn(m.handleRevokeToken))
	a.Handle("GET /api/admin/users", Require(RoleAdmin, m.handleListUsers))
	a.Handle("PUT /api/admin/users/{email}/roles", Require(RoleAdmin, m.handlePutRoles))
}

// requireSignedIn wraps next so it 401s before next runs when the request
// carries no caller. Unlike Require/RequireWorkspace it asks for no
// particular role: GET /auth/me and /auth/tokens are for anyone signed in.
func requireSignedIn(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromRequest(r); !ok {
			writeAuthError(w, http.StatusUnauthorized,
				`not signed in: sign in at /auth/login, or send a personal access token as "Authorization: Bearer <token>"`)
			return
		}
		next(w, r)
	}
}

// decodeAuthJSON decodes r's body into v, bounded to maxAuthBody and
// rejecting unknown fields, writing the matching error status itself on
// failure (so callers just return on a non-nil error).
func decodeAuthJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeAuthError(w, status, "request body: "+err.Error())
		return err
	}
	return nil
}

func (m *Manager) handleLogout(w http.ResponseWriter, r *http.Request) {
	m.clearCookie(w, sessionCookie)
	w.WriteHeader(http.StatusNoContent)
}

// handleConfig answers whether /auth/login is usable, unauthenticated and
// always 200 — the signal the frontend uses to tell "sign in please" (OIDC
// configured, a plain 401 from handleMe means not yet signed in) apart
// from "there is no sign-in here, paste a token" (token-only mode), rather
// than overloading /auth/me's status code for both meanings (cross-plan
// review of 4a/4c; architecture note ruling 18).
func (m *Manager) handleConfig(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, http.StatusOK, map[string]any{"oidc": m.oidc != nil})
}

func (m *Manager) handleMe(w http.ResponseWriter, r *http.Request) {
	p, _ := FromRequest(r) // requireSignedIn already guarantees ok
	api.WriteJSON(w, http.StatusOK, map[string]any{"name": p.Name, "email": p.Email, "roles": p.Roles})
}

func (m *Manager) handleListTokens(w http.ResponseWriter, r *http.Request) {
	p, _ := FromRequest(r)
	list, err := m.listTokens(p.Email)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, list)
}

func (m *Manager) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	p, _ := FromRequest(r)
	var body struct {
		Label string `json:"label"`
	}
	if decodeAuthJSON(w, r, &body) != nil {
		return
	}
	id, token, err := m.CreateToken(p.Email, body.Label)
	if err != nil {
		writeAuthError(w, httpStatusForAuthErr(err), err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	api.WriteJSON(w, http.StatusCreated, map[string]string{"id": id, "token": token})
}

func (m *Manager) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	p, _ := FromRequest(r)
	if err := m.revokeToken(p.Email, r.PathValue("id")); err != nil {
		if errors.Is(err, errNoToken) {
			writeAuthError(w, http.StatusNotFound, "no such token")
			return
		}
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Manager) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := m.loadUsers()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type userJSON struct {
		Email         string `json:"email"`
		Name          string `json:"name"`
		Roles         Roles  `json:"roles"`
		AdminByConfig bool   `json:"admin_by_config"`
	}
	out := make([]userJSON, 0, len(users))
	for email, u := range users {
		out = append(out, userJSON{Email: email, Name: u.Name, Roles: u.Roles.normalized(), AdminByConfig: m.isAdminEmail(email)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	api.WriteJSON(w, http.StatusOK, out)
}

// rolesUpdate is PUT /api/admin/users/{email}/roles's body: every field is
// optional, and only the ones present are merged into the user's roles
// (architecture note: "merges into that user's record").
type rolesUpdate struct {
	Admin           *bool     `json:"admin,omitempty"`
	TaskAuthor      *bool     `json:"task_author,omitempty"`
	ProcessorAuthor *bool     `json:"processor_author,omitempty"`
	Engineer        *[]string `json:"engineer,omitempty"`
}

func (m *Manager) handlePutRoles(w http.ResponseWriter, r *http.Request) {
	var body rolesUpdate
	if decodeAuthJSON(w, r, &body) != nil {
		return
	}
	u, err := m.UpdateUser(r.PathValue("email"), func(u *User) {
		if body.Admin != nil {
			u.Roles.Admin = *body.Admin
		}
		if body.TaskAuthor != nil {
			u.Roles.TaskAuthor = *body.TaskAuthor
		}
		if body.ProcessorAuthor != nil {
			u.Roles.ProcessorAuthor = *body.ProcessorAuthor
		}
		if body.Engineer != nil {
			u.Roles.Engineer = *body.Engineer
		}
	})
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, u.Roles)
}

// httpStatusForAuthErr maps CreateToken's ErrInvalid to 400; anything else
// (disk trouble) is a 500.
func httpStatusForAuthErr(err error) int {
	if errors.Is(err, ErrInvalid) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
