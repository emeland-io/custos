package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/auth"
)

func TestTokenCreateUsage(t *testing.T) {
	for _, args := range [][]string{
		{"token"},
		{"token", "create"},
		{"token", "create", "--data-dir", t.TempDir()},
		{"token", "create", "--email", "jane@example.org"},
	} {
		code, _, errs := runCmd(t, args...)
		if code != 2 || !strings.Contains(errs, "usage: custos token create") {
			t.Errorf("%v: code %d err %q", args, code, errs)
		}
	}
}

func TestTokenCreateBootstrapsAnAdmin(t *testing.T) {
	data := t.TempDir()
	code, out, errs := runCmd(t, "token", "create", "--data-dir", data, "--email", "Jane@Example.org",
		"--name", "Jane Doe", "--admin", "--label", "bootstrap")
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	token := strings.TrimSpace(out)
	if token == "" || strings.Contains(token, "\n") {
		t.Fatalf("stdout %q, want exactly the token", out)
	}

	m, err := auth.NewManager(auth.Config{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	u, err := m.UpdateUser("jane@example.org", func(*auth.User) {}) // a no-op update just reads the current record back
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Jane Doe" || !u.Roles.Admin {
		t.Fatalf("user %+v", u)
	}
}

// TestTokenCreateWhileServing is Review Focus 6: a user or role granted by
// custos token create, run against the same --data-dir a server already
// has open, is usable on that server's very next request — no restart.
// The "server" here is the real auth.Manager + auth.Register + api.API
// stack (what server.Server mounts in Task 8), driven directly through
// m.Middleware so this test needs nothing from the server package.
func TestTokenCreateWhileServing(t *testing.T) {
	data := t.TempDir()
	serverSide, err := auth.NewManager(auth.Config{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(nil, nil)
	auth.Register(a, serverSide)
	ts := httptest.NewServer(serverSide.Middleware()(a.Handler()))
	defer ts.Close()

	before, err := http.Get(ts.URL + "/api/admin/users")
	if err != nil {
		t.Fatal(err)
	}
	before.Body.Close()
	if before.StatusCode != http.StatusUnauthorized {
		t.Fatalf("before: %d, want 401 (no user exists yet)", before.StatusCode)
	}

	code, out, errs := runCmd(t, "token", "create", "--data-dir", data, "--email", "jane@example.org",
		"--name", "Jane Doe", "--admin", "--label", "bootstrap")
	if code != 0 {
		t.Fatalf("token create: code %d, stderr %s", code, errs)
	}
	token := strings.TrimSpace(out)

	req, _ := http.NewRequest("GET", ts.URL+"/api/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	after, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Body.Close()
	if after.StatusCode != http.StatusOK {
		t.Fatalf("after: %d, want 200 — serverSide (opened before the CLI ran) must see jane without a restart", after.StatusCode)
	}
}

func TestTokenCreateAddsToExistingRoles(t *testing.T) {
	data := t.TempDir()
	if code, _, errs := runCmd(t, "token", "create", "--data-dir", data, "--email", "jane@example.org", "--engineer", ws1); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	if code, _, errs := runCmd(t, "token", "create", "--data-dir", data, "--email", "jane@example.org", "--task-author"); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	m, err := auth.NewManager(auth.Config{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	u, err := m.UpdateUser("jane@example.org", func(*auth.User) {}) // a no-op update just to read the current record back
	if err != nil {
		t.Fatal(err)
	}
	if !u.Roles.TaskAuthor || len(u.Roles.Engineer) != 1 || u.Roles.Engineer[0] != ws1 {
		t.Errorf("roles %+v, want both grants kept", u.Roles)
	}
}
