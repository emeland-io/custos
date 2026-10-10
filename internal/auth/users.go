package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/task"
)

// User is one person on file in <data-dir>/auth/users.json, keyed there by
// their email address in lower case.
type User struct {
	Name    string `json:"name"`              // from the last OIDC sign-in or custos token create; empty for someone an admin added before their first sign-in
	Subject string `json:"subject,omitempty"` // OIDC "sub" of the last sign-in
	Roles   Roles  `json:"roles"`
}

// ErrInvalid marks a malformed email address, name, label or role entry.
var ErrInvalid = errors.New("invalid")

// normalizeEmail returns s in lower case without surrounding space, or
// ErrInvalid when it cannot be an email address custos can sign commits
// with or put in a URL path.
func normalizeEmail(s string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(s))
	at := strings.LastIndex(e, "@")
	if at <= 0 || at == len(e)-1 || strings.ContainsAny(e, "<>\"\\/,; \t\r\n\x00") {
		return "", fmt.Errorf("%w: %q is not an email address", ErrInvalid, s)
	}
	return e, nil
}

// checkUser rejects names that cannot sign a commit and engineer entries
// that are neither a workspace UUID nor "*".
func checkUser(u User) error {
	if strings.ContainsAny(u.Name, "<>\r\n\x00") {
		return fmt.Errorf("%w: name %q contains <, > or a line break", ErrInvalid, u.Name)
	}
	for _, id := range u.Roles.Engineer {
		if id != AllWorkspaces && !task.ValidID(id) {
			return fmt.Errorf("%w: engineer entry %q is neither a workspace id (a lower-case UUID v4) nor %q", ErrInvalid, id, AllWorkspaces)
		}
	}
	return nil
}

func (m *Manager) usersFile() string { return filepath.Join(m.dir, "users.json") }

// loadUsers reads users.json; a missing file is an empty one.
func (m *Manager) loadUsers() (map[string]User, error) {
	data, err := os.ReadFile(m.usersFile())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]User{}, nil
	}
	if err != nil {
		return nil, err
	}
	users := map[string]User{}
	if err := json.Unmarshal(data, &users); err != nil {
		return nil, fmt.Errorf("%s: %w", m.usersFile(), err)
	}
	return users, nil
}

// saveUsers replaces users.json. Callers hold m.mu.
func (m *Manager) saveUsers(users map[string]User) error {
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(m.usersFile(), append(data, '\n'))
}

// UpdateUser applies update to the record of email — an empty record when
// the email is not on file yet — checks the result and saves it. Errors
// wrapping ErrInvalid name a malformed email, name or engineer entry; the
// file is then unchanged. Engineer lists are saved sorted and without
// duplicates.
func (m *Manager) UpdateUser(email string, update func(u *User)) (User, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	users, err := m.loadUsers()
	if err != nil {
		return User{}, err
	}
	u := users[email]
	update(&u)
	u.Roles = u.Roles.normalized()
	if err := checkUser(u); err != nil {
		return User{}, err
	}
	users[email] = u
	if err := m.saveUsers(users); err != nil {
		return User{}, err
	}
	return u, nil
}

// isAdminEmail reports whether email is in Config.AdminEmails.
func (m *Manager) isAdminEmail(email string) bool {
	return slices.Contains(m.adminEmails, strings.ToLower(email))
}

// principal is the Principal of the user on file as u under email:
// AdminEmails adds Admin, and a user without a name signs with the local
// part of their email address.
func (m *Manager) principal(email string, u User, subject string) Principal {
	roles := u.Roles.normalized()
	if m.isAdminEmail(email) {
		roles.Admin = true
	}
	name := u.Name
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	return Principal{Subject: subject, Name: name, Email: email, Roles: roles}
}

// writeAtomic writes data to a temporary file (mode 0600) next to path and
// renames it over path.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
