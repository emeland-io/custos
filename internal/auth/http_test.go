package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/api"
)

// handler builds the mux Register adds to, with one extra route
// (GET /whoami) that reports the caller api.CallerOf finds — the simplest
// way to drive requireSignedIn-gated and Require-gated handlers through a
// real http.Handler without a full server.Server.
func testHandler(t *testing.T, m *Manager) http.Handler {
	t.Helper()
	a := api.New(nil, nil) //lint:ignore -- these endpoints never touch a.st/a.bl
	Register(a, m)
	return a.Handler()
}

// signedRequest returns a request carrying ctx's caller already injected
// via api.WithCaller — the same shortcut internal/api's own tests use —
// standing in for a full session cookie or token, since these tests are
// about Register's own endpoints, not about Middleware (Task 3 already
// covers resolving a cookie or token into a caller).
func signedRequest(method, path, body string, p Principal) *http.Request {
	var r *strings.Reader
	if body != "" {
		r = strings.NewReader(body)
	} else {
		r = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req.WithContext(api.WithCaller(req.Context(), p))
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder, want int) T {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d; body %s", rec.Code, want, rec.Body)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return v
}

// TestHandleConfig: unauthenticated, always 200, and the oidc field tracks
// whether the Manager was built with Config.OIDC set — this is the signal
// the frontend uses to tell token-only mode apart from a plain 401
// (cross-plan review of 4a/4c; architecture note ruling 18), so /auth/me
// itself never needs to vary its status code for that purpose.
func TestHandleConfig(t *testing.T) {
	m := newManager(t) // token-only: Config.OIDC is nil
	h := testHandler(t, m)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/auth/config", nil))
	got := decodeBody[struct {
		OIDC bool `json:"oidc"`
	}](t, rec, http.StatusOK)
	if got.OIDC {
		t.Errorf("token-only manager: /auth/config oidc = true")
	}
}

func TestHandleMe(t *testing.T) {
	m := newManager(t)
	h := testHandler(t, m)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/auth/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", rec.Code)
	}

	p := Principal{Email: "jane@example.org", Name: "Jane Doe", Roles: Roles{TaskAuthor: true}}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("GET", "/auth/me", "", p))
	got := decodeBody[struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Roles Roles  `json:"roles"`
	}](t, rec, http.StatusOK)
	if got.Name != "Jane Doe" || got.Email != "jane@example.org" || !got.Roles.TaskAuthor {
		t.Errorf("me %+v", got)
	}
}

func TestHandleLogout(t *testing.T) {
	m := newManager(t)
	h := testHandler(t, m)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/auth/logout", nil)) // anyone, even signed out
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code %d", rec.Code)
	}
	cleared := rec.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != sessionCookie || cleared[0].MaxAge >= 0 {
		t.Errorf("cookies %+v", cleared)
	}
}

func TestTokenEndpoints(t *testing.T) {
	m := newManager(t)
	addUser(t, m, "jane@example.org", "Jane Doe", Roles{})
	h := testHandler(t, m)
	jane := Principal{Email: "jane@example.org"}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("POST", "/auth/tokens", `{"label":"laptop"}`, jane))
	created := decodeBody[struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}](t, rec, http.StatusCreated)
	if created.ID == "" || created.Token == "" {
		t.Fatalf("created %+v", created)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("missing Cache-Control: no-store on a response that carries a secret")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("GET", "/auth/tokens", "", jane))
	list := decodeBody[[]struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}](t, rec, http.StatusOK)
	// addUser (users_test.go/tokens_test.go, Task 2) already mints one
	// token labelled "test" as its own side effect, so the list also
	// carries that entry alongside "laptop" — found while applying this
	// plan: an earlier version of this assertion expected exactly one
	// entry, which only held if addUser minted none.
	found := false
	for _, tok := range list {
		if tok.ID == created.ID && tok.Label == "laptop" {
			found = true
		}
	}
	if len(list) != 2 || !found {
		t.Fatalf("list %+v, want 2 entries including {%s laptop}", list, created.ID)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("DELETE", "/auth/tokens/"+created.ID, "", jane))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("DELETE", "/auth/tokens/"+created.ID, "", jane))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}

	// A token cannot be created for anyone but the caller: the label-only
	// body has nowhere to name another user, and the handler always uses
	// the caller's own email, so this is enforced by construction, not by
	// an explicit check — documented here so a later refactor notices if
	// it adds an "email" field to the request body without re-deriving it
	// from the caller.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("POST", "/auth/tokens", `{"label":""}`, jane))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty label: %d", rec.Code)
	}
}

// TestPutRoles is Review Focus 6: an admin's PUT merges into a user's
// roles — including a user who has never signed in — and takes effect for
// the very next request (nothing here is cached; proven by reading
// loadUsers directly afterwards, with no server restart in between).
func TestPutRoles(t *testing.T) {
	m := newManager(t)
	h := testHandler(t, m)
	admin := Principal{Email: "admin@example.org", Roles: Roles{Admin: true}}
	notAdmin := Principal{Email: "jane@example.org", Roles: Roles{TaskAuthor: true}}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("PUT", "/api/admin/users/new@example.org/roles", `{"task_author":true,"engineer":["`+ws1+`"]}`, notAdmin))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("PUT", "/api/admin/users/new@example.org/roles", `{"task_author":true,"engineer":["`+ws1+`"]}`, admin))
	got := decodeBody[Roles](t, rec, http.StatusOK)
	if !got.TaskAuthor || !slices.Equal(got.Engineer, []string{ws1}) {
		t.Fatalf("roles %+v", got)
	}

	// A second PUT that sets only admin leaves task_author and engineer
	// alone: this is a merge, not a replace.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("PUT", "/api/admin/users/new@example.org/roles", `{"admin":true}`, admin))
	got = decodeBody[Roles](t, rec, http.StatusOK)
	if !got.Admin || !got.TaskAuthor || len(got.Engineer) != 1 {
		t.Fatalf("merged roles %+v", got)
	}

	users, _ := m.loadUsers()
	if !users["new@example.org"].Roles.Admin {
		t.Error("the change was not on disk straight after the request")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("PUT", "/api/admin/users/not-an-email/roles", `{}`, admin))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad email: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("PUT", "/api/admin/users/jane@example.org/roles", `{"engineer":["not-a-uuid"]}`, admin))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad engineer entry: %d", rec.Code)
	}
}

func TestHandleListUsers(t *testing.T) {
	m, err := NewManager(Config{DataDir: t.TempDir(), AdminEmails: []string{"root@example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	addUser(t, m, "jane@example.org", "Jane Doe", Roles{TaskAuthor: true})
	addUser(t, m, "root@example.org", "Root", Roles{})
	h := testHandler(t, m)
	admin := Principal{Email: "root@example.org", Roles: Roles{Admin: true}}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest("GET", "/api/admin/users", "", admin))
	list := decodeBody[[]struct {
		Email         string `json:"email"`
		AdminByConfig bool   `json:"admin_by_config"`
	}](t, rec, http.StatusOK)
	if len(list) != 2 || list[0].Email != "jane@example.org" || list[1].Email != "root@example.org" || !list[1].AdminByConfig {
		t.Fatalf("users %+v", list)
	}
}
