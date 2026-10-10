package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/task"
)

// addUser puts a user on file and returns a new token for them.
func addUser(t *testing.T, m *Manager, email, name string, roles Roles) string {
	t.Helper()
	if _, err := m.UpdateUser(email, func(u *User) { u.Name, u.Roles = name, roles }); err != nil {
		t.Fatal(err)
	}
	_, token, err := m.CreateToken(email, "test")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// flip changes the first character of a base64url string to another
// valid one.
func flip(s string) string {
	if s == "" {
		return "A"
	}
	if s[0] == 'A' {
		return "B" + s[1:]
	}
	return "A" + s[1:]
}

func TestTokenLookup(t *testing.T) {
	m := newManager(t)
	token := addUser(t, m, "Jane@Example.org", "Jane Doe", Roles{TaskAuthor: true})
	id, _, _ := strings.Cut(token, ".")
	p, ok := m.lookupToken(token)
	if !ok || p.Email != "jane@example.org" || p.Name != "Jane Doe" || !p.Roles.TaskAuthor || p.Subject != "token:"+id {
		t.Fatalf("principal %+v %v", p, ok)
	}
	// After an OIDC sign-in the subject on file is used.
	m.UpdateUser("jane@example.org", func(u *User) { u.Subject = "oidc-sub" })
	if p, _ := m.lookupToken(token); p.Subject != "oidc-sub" {
		t.Errorf("subject %q, want the OIDC subject on file", p.Subject)
	}
	// A user on file without a name signs commits with the email's local part.
	tok := addUser(t, m, "joe@example.org", "", Roles{})
	if p, _ := m.lookupToken(tok); p.Name != "joe" {
		t.Errorf("name %q, want joe", p.Name)
	}
}

func TestTokenFormat(t *testing.T) {
	m := newManager(t)
	token := addUser(t, m, "jane@example.org", "Jane Doe", Roles{})
	id, secret, ok := strings.Cut(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(secret)
	if !ok || !task.ValidID(id) || err != nil || len(raw) != 32 {
		t.Fatalf("token %q: want <uuid v4>.<base64url of 32 bytes>", token)
	}
	path := filepath.Join(m.dir, "tokens", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatal("the token file contains the secret")
	}
	var rec tokenRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(token))
	if rec.ID != id || rec.OwnerEmail != "jane@example.org" || rec.Label != "test" ||
		rec.SHA256 != hex.EncodeToString(sum[:]) || rec.LastUsed != nil || rec.Created.IsZero() {
		t.Errorf("record %+v", rec)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
}

func TestTokenLookupFails(t *testing.T) {
	m := newManager(t)
	token := addUser(t, m, "jane@example.org", "Jane Doe", Roles{Admin: true})
	id, secret, _ := strings.Cut(token, ".")

	_, revoked, err := m.CreateToken("jane@example.org", "revoked")
	if err != nil {
		t.Fatal(err)
	}
	revokedID, _, _ := strings.Cut(revoked, ".")
	if err := m.revokeToken("jane@example.org", revokedID); err != nil {
		t.Fatal(err)
	}

	orphan := addUser(t, m, "gone@example.org", "Gone", Roles{})
	users, _ := m.loadUsers()
	delete(users, "gone@example.org")
	if err := m.saveUsers(users); err != nil {
		t.Fatal(err)
	}

	copied := uuid.NewString()
	data, _ := os.ReadFile(filepath.Join(m.dir, "tokens", id+".json"))
	os.WriteFile(filepath.Join(m.dir, "tokens", copied+".json"), data, 0o600)

	for name, tok := range map[string]string{
		"empty":                     "",
		"id only":                   id,
		"secret only":               secret,
		"unknown id":                uuid.NewString() + "." + secret,
		"revoked":                   revoked,
		"wrong secret":              id + "." + flip(secret),
		"secret with extra data":    token + "x",
		"path in id":                "../users." + secret,
		"upper-case id":             strings.ToUpper(id) + "." + secret,
		"file copied to another id": copied + "." + secret,
		"owner not on file":         orphan,
	} {
		if p, ok := m.lookupToken(tok); ok {
			t.Errorf("%s: resolved to %+v", name, p)
		}
	}
	if _, ok := m.lookupToken(token); !ok {
		t.Error("the valid token stopped working")
	}
}

func TestTokenLastUsed(t *testing.T) {
	m := newManager(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	token := addUser(t, m, "jane@example.org", "Jane Doe", Roles{})
	id, _, _ := strings.Cut(token, ".")
	lastUsed := func() *time.Time {
		rec, err := m.readToken(id)
		if err != nil {
			t.Fatal(err)
		}
		return rec.LastUsed
	}
	m.lookupToken(token)
	if lu := lastUsed(); lu == nil || !lu.Equal(now) {
		t.Fatalf("last_used %v after the first use", lu)
	}
	first := now
	now = now.Add(30 * time.Second)
	m.lookupToken(token)
	if lu := lastUsed(); !lu.Equal(first) {
		t.Errorf("last_used rewritten %v after 30s; it is updated at most once a minute", lu)
	}
	now = now.Add(time.Minute)
	m.lookupToken(token)
	if lu := lastUsed(); !lu.Equal(now) {
		t.Errorf("last_used %v, want %v", lu, now)
	}
}

func TestListAndRevokeTokens(t *testing.T) {
	m := newManager(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	addUser(t, m, "jane@example.org", "Jane Doe", Roles{}) // token "test"
	now = now.Add(time.Second)
	id2, _, err := m.CreateToken("jane@example.org", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	joe := addUser(t, m, "joe@example.org", "Joe", Roles{})
	joeID, _, _ := strings.Cut(joe, ".")

	list, err := m.listTokens("jane@example.org")
	if err != nil || len(list) != 2 || list[0].Label != "test" || list[1].ID != id2 || list[1].Label != "laptop" {
		t.Fatalf("list %+v %v", list, err)
	}
	if empty, err := m.listTokens("nobody@example.org"); err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("list of a user without tokens: %#v %v, want an empty non-nil slice", empty, err)
	}
	for name, id := range map[string]string{"another user's token": joeID, "unknown": uuid.NewString(), "not an id": "../users"} {
		if err := m.revokeToken("jane@example.org", id); !errors.Is(err, errNoToken) {
			t.Errorf("%s: err %v, want errNoToken", name, err)
		}
	}
	if _, ok := m.lookupToken(joe); !ok {
		t.Error("jane revoked joe's token")
	}
	if err := m.revokeToken("jane@example.org", id2); err != nil {
		t.Fatal(err)
	}
	if list, _ := m.listTokens("jane@example.org"); len(list) != 1 {
		t.Errorf("list after revoke %+v", list)
	}
}

func TestCreateTokenRejects(t *testing.T) {
	m := newManager(t)
	addUser(t, m, "jane@example.org", "Jane Doe", Roles{})
	for name, tc := range map[string]struct{ email, label string }{
		"user not on file":  {"nobody@example.org", "x"},
		"not an email":      {"jane", "x"},
		"empty label":       {"jane@example.org", "  "},
		"label too long":    {"jane@example.org", strings.Repeat("x", 101)},
		"control character": {"jane@example.org", "a\nb"},
	} {
		if _, _, err := m.CreateToken(tc.email, tc.label); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err %v, want ErrInvalid", name, err)
		}
	}
}
