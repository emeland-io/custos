package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// newManager returns a token-only manager over a new data directory.
func newManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewManagerCreatesTheAuthDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewManager(Config{DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"auth", "auth/tokens"} {
		fi, err := os.Stat(filepath.Join(dir, d))
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi, err)
		}
	}
	if _, err := NewManager(Config{}); err == nil {
		t.Error("NewManager without a data directory succeeded")
	}
}

func TestNewManagerRejectsDamagedUsersFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "auth"), 0o700)
	os.WriteFile(filepath.Join(dir, "auth", "users.json"), []byte("{"), 0o600)
	if _, err := NewManager(Config{DataDir: dir}); err == nil || !strings.Contains(err.Error(), "users.json") {
		t.Errorf("err %v, want one naming users.json", err)
	}
}

func TestUpdateUser(t *testing.T) {
	m := newManager(t)
	u, err := m.UpdateUser(" Jane@Example.ORG ", func(u *User) {
		u.Name = "Jane Doe"
		u.Roles.Engineer = []string{ws2, ws1, ws2}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Roles{Engineer: []string{ws1, ws2}}); !reflect.DeepEqual(u.Roles, want) {
		t.Errorf("roles %+v, want %+v (sorted, without duplicates)", u.Roles, want)
	}
	// A second update sees the first one.
	if _, err := m.UpdateUser("jane@example.org", func(u *User) { u.Roles.TaskAuthor = true }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.dir, "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]map[string]any
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	jane, ok := file["jane@example.org"]
	if !ok || len(file) != 1 || jane["name"] != "Jane Doe" {
		t.Fatalf("users.json:\n%s", data)
	}
	if _, ok := jane["subject"]; ok {
		t.Errorf("subject written before any sign-in:\n%s", data)
	}
	roles := jane["roles"].(map[string]any)
	if roles["task_author"] != true || roles["admin"] != false || roles["processor_author"] != false || len(roles["engineer"].([]any)) != 2 {
		t.Errorf("roles in users.json: %v", roles)
	}
	if fi, _ := os.Stat(filepath.Join(m.dir, "users.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("users.json mode %v", fi.Mode().Perm())
	}
}

func TestUpdateUserRejects(t *testing.T) {
	m := newManager(t)
	if _, err := m.UpdateUser("jane@example.org", func(u *User) { u.Name = "Jane Doe" }); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(m.dir, "users.json"))
	for name, tc := range map[string]struct {
		email  string
		update func(*User)
	}{
		"not an email":       {"jane", func(*User) {}},
		"empty email":        {"", func(*User) {}},
		"email with <":       {"<jane@example.org>", func(*User) {}},
		"email with space":   {"jane doe@example.org", func(*User) {}},
		"name with <":        {"jane@example.org", func(u *User) { u.Name = "Jane <x>" }},
		"name with newline":  {"jane@example.org", func(u *User) { u.Name = "Jane\nDoe" }},
		"engineer not uuid":  {"jane@example.org", func(u *User) { u.Roles.Engineer = []string{"ws-1"} }},
		"engineer uppercase": {"jane@example.org", func(u *User) { u.Roles.Engineer = []string{strings.ToUpper(ws1)} }},
	} {
		if _, err := m.UpdateUser(tc.email, tc.update); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err %v, want ErrInvalid", name, err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(m.dir, "users.json"))
	if string(before) != string(after) {
		t.Errorf("a rejected update changed users.json:\n%s", after)
	}
}

func TestAdminEmails(t *testing.T) {
	m, err := NewManager(Config{DataDir: t.TempDir(), AdminEmails: []string{" JANE@example.org ", ""}})
	if err != nil {
		t.Fatal(err)
	}
	token := addUser(t, m, "jane@example.org", "Jane Doe", Roles{})
	p, ok := m.lookupToken(token)
	if !ok || !p.Roles.Admin {
		t.Errorf("principal %+v %v, want admin from AdminEmails", p, ok)
	}
	users, _ := m.loadUsers()
	if users["jane@example.org"].Roles.Admin {
		t.Error("AdminEmails was written into users.json; it must be applied at every check instead (ruling 4.6)")
	}
}
