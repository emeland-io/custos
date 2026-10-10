package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/auth"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gittest"
	"github.com/emeland-io/custos/internal/store"
)

// startAuthed is start, plus a real auth.Manager (token-only mode)
// wired through WithAuth/WithMiddleware, and api.New mounted at /api/
// with auth.Register added. It returns the bare URL (no credentials
// embedded) — callers add their own token per request, since these tests
// are about who may do what, not about one bootstrap identity.
func startAuthed(t *testing.T) (*store.Store, *auth.Manager, string) {
	t.Helper()
	st, err := store.Open(t.TempDir(), custosBin, "http://custos.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InstallHooks(); err != nil {
		t.Fatal(err)
	}
	m, err := auth.NewManager(auth.Config{DataDir: st.DataDir()})
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(st, nil)
	auth.Register(a, m)
	srv := New(st).WithAPI(a.Handler()).WithAuth(a.Handler()).
		WithMiddleware(m.Middleware("/healthz", "/auth/login", "/auth/callback", "/"))
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return st, m, ts.URL
}

// bootstrapToken creates (or updates) email with roles and returns a
// fresh token for them.
func bootstrapToken(t *testing.T, m *auth.Manager, email string, roles auth.Roles) string {
	t.Helper()
	if _, err := m.UpdateUser(email, func(u *auth.User) { u.Roles = roles }); err != nil {
		t.Fatal(err)
	}
	_, token, err := m.CreateToken(email, "test")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// authedURL returns rawURL with token as the Basic password (what git
// sends, and what Middleware's resolve reads back).
func authedURL(t *testing.T, rawURL, token string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("token", token)
	return u.String()
}

// TestAPINeedsSignIn is Review Focus 5: /api/ answers 401 to an anonymous
// request, with no WWW-Authenticate (so the UI never sees a browser's own
// password dialog), and 200 once a caller is presented.
func TestAPINeedsSignIn(t *testing.T) {
	_, m, baseURL := startAuthed(t)
	resp, err := http.Get(baseURL + "/api/catalog")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") != "" {
		t.Error("WWW-Authenticate set on /api/; the UI must not trigger a browser password dialog")
	}

	token := bootstrapToken(t, m, "jane@example.org", auth.Roles{})
	req, _ := http.NewRequest("GET", baseURL+"/api/catalog", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signed in, no role needed for a read: %d", resp.StatusCode)
	}
}

// TestGitNeedsSignIn is Review Focus 5 for /git/: a bare HTTP request
// (not through the git CLI, so there is no risk of it blocking on a
// credential prompt) gets 401 with the Basic challenge git needs before
// it will send a URL's or a credential helper's credentials.
func TestGitNeedsSignIn(t *testing.T) {
	_, _, baseURL := startAuthed(t)
	resp, err := http.Get(baseURL + "/git/catalog.git/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("code %d", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != `Basic realm="custos"` {
		t.Errorf("WWW-Authenticate %q", got)
	}
}

// TestPushNeedsRole is Review Focus 4: a push to the catalog needs
// task-author; a push to a workspace needs engineer on that workspace;
// a caller who is signed in but lacks the role gets 403 (not a retry
// prompt — git never re-asks for different credentials on 403, so these
// pushes, unlike an anonymous one, are safe to run through the real git
// CLI without GIT_TERMINAL_PROMPT=0).
func TestPushNeedsRole(t *testing.T) {
	st, m, baseURL := startAuthed(t)
	readOnly := bootstrapToken(t, m, "ro@example.org", auth.Roles{})
	taskAuthor := bootstrapToken(t, m, "ta@example.org", auth.Roles{TaskAuthor: true})
	engineer := bootstrapToken(t, m, "eng@example.org", auth.Roles{Engineer: []string{fixture.WorkspaceID}})

	work := gittest.Init(t)
	gittest.Commit(t, work, fixture.Catalog())

	if out, err := gittest.Try(work, "push", authedURL(t, baseURL+"/git/catalog.git", readOnly), "main"); err == nil {
		t.Fatalf("read-only caller pushed the catalog:\n%s", out)
	}
	gittest.Run(t, work, "push", authedURL(t, baseURL+"/git/catalog.git", taskAuthor), "main")

	if err := st.CreateWorkspace(fixture.WorkspaceID, jane); err != nil {
		t.Fatal(err)
	}
	wsDir := filepath.Join(t.TempDir(), "ws")
	gittest.Run(t, t.TempDir(), "clone", authedURL(t, baseURL+"/git/workspaces/"+fixture.WorkspaceID+".git", engineer), wsDir)
	gittest.Commit(t, wsDir, map[string]string{"answers/" + fixture.TaskA + ".md": "---\ntask: " + fixture.TaskA + "\ntask_version: 1.0.0\ntype: text\nvalue: x\n---\n"})
	gittest.Run(t, wsDir, "push", "origin", "main") // origin already carries eng's token, from clone

	// A further local commit, so the push below actually has something
	// new to send: otherwise git decides the remote is already up to
	// date and never issues the request at all, which would make this
	// check pass even if the role gate were missing entirely — found
	// while applying this plan.
	gittest.Commit(t, wsDir, map[string]string{"answers/" + fixture.TaskA + ".md": "---\ntask: " + fixture.TaskA + "\ntask_version: 1.0.0\ntype: text\nvalue: y\n---\n"})
	if out, err := gittest.Try(wsDir, "push", authedURL(t, baseURL+"/git/workspaces/"+fixture.WorkspaceID+".git", taskAuthor), "main"); err == nil {
		t.Fatalf("task-author (not engineer here) pushed the workspace:\n%s", out)
	}
}

// TestMountingAndMiddlewareOrder checks that /auth/ bypasses the sign-in
// gate (handleLogin itself decides whether it is available) while /api/
// does not, and that excludedPrefixes reaches Middleware even once
// WithAuth/WithMiddleware are both set.
func TestMountingAndMiddlewareOrder(t *testing.T) {
	_, _, baseURL := startAuthed(t)
	resp, err := http.Get(baseURL + "/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/auth/me without a caller: %d, want 401 from the handler itself, not the gate", resp.StatusCode)
	}
	resp, err = http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz: %d", resp.StatusCode)
	}
}
