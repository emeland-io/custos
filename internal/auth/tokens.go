package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/task"
)

// tokenRecord is the file <data-dir>/auth/tokens/<id>.json. It holds the
// SHA-256 of the whole bearer value, never the value itself (ruling 4.7).
type tokenRecord struct {
	ID         string     `json:"id"`
	OwnerEmail string     `json:"owner_email"`
	Label      string     `json:"label"`
	SHA256     string     `json:"sha256"`
	Created    time.Time  `json:"created"`
	LastUsed   *time.Time `json:"last_used"`
}

// tokenInfo is a token as GET /auth/tokens lists it.
type tokenInfo struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"last_used"`
}

// maxLabelLen bounds a token's label, in bytes.
const maxLabelLen = 100

// lastUsedEvery is how often at most a token's last_used is rewritten, so
// that a clone (several requests) does not rewrite the file each time.
const lastUsedEvery = time.Minute

// errNoToken: the token does not exist or is not the caller's.
var errNoToken = errors.New("no such token")

func (m *Manager) tokenFile(id string) string { return filepath.Join(m.dir, "tokens", id+".json") }

// CreateToken mints a personal access token for the user on file under
// email, labelled label (1-100 bytes, no control characters). It returns
// the token's id and its bearer value "<id>.<32 random bytes, base64url>",
// which is not stored and cannot be shown again. A user not on file, a
// malformed email or a bad label is ErrInvalid.
func (m *Manager) CreateToken(email, label string) (id, token string, err error) {
	if email, err = normalizeEmail(email); err != nil {
		return "", "", err
	}
	if label, err = checkLabel(label); err != nil {
		return "", "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	users, err := m.loadUsers()
	if err != nil {
		return "", "", err
	}
	if _, ok := users[email]; !ok {
		return "", "", fmt.Errorf("%w: %s is not on file; add the user first", ErrInvalid, email)
	}
	secret := make([]byte, 32)
	rand.Read(secret) // never fails (crypto/rand since Go 1.24)
	id = uuid.NewString()
	token = id + "." + base64.RawURLEncoding.EncodeToString(secret)
	sum := sha256.Sum256([]byte(token))
	rec := tokenRecord{ID: id, OwnerEmail: email, Label: label, SHA256: hex.EncodeToString(sum[:]), Created: m.now().UTC()}
	if err := m.writeToken(rec); err != nil {
		return "", "", err
	}
	return id, token, nil
}

// checkLabel trims s and checks it as a token label.
func checkLabel(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", fmt.Errorf("%w: a token needs a label, such as the machine or script that uses it", ErrInvalid)
	case len(s) > maxLabelLen:
		return "", fmt.Errorf("%w: a token label has at most %d bytes", ErrInvalid, maxLabelLen)
	case strings.ContainsFunc(s, unicode.IsControl):
		return "", fmt.Errorf("%w: a token label must not contain control characters", ErrInvalid)
	}
	return s, nil
}

func (m *Manager) writeToken(rec tokenRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(m.tokenFile(rec.ID), append(data, '\n'))
}

// readToken reads the record of id; id must have passed task.ValidID. A
// file whose id field is not its name is an error, so a copied file does
// not work under another id.
func (m *Manager) readToken(id string) (tokenRecord, error) {
	data, err := os.ReadFile(m.tokenFile(id))
	if err != nil {
		return tokenRecord{}, err
	}
	var rec tokenRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return tokenRecord{}, fmt.Errorf("token %s: %w", id, err)
	}
	if rec.ID != id {
		return tokenRecord{}, fmt.Errorf("token %s: file holds token %q", id, rec.ID)
	}
	return rec, nil
}

// lookupToken returns the principal a bearer value belongs to, or
// ok=false when it names no live token: malformed, unknown id, revoked,
// wrong secret, or an owner who is no longer on file. The id is read off
// the value; the whole value is hashed and compared in constant time.
func (m *Manager) lookupToken(token string) (Principal, bool) {
	id, _, ok := strings.Cut(token, ".")
	if !ok || !task.ValidID(id) {
		return Principal{}, false
	}
	rec, err := m.readToken(id)
	if err != nil {
		return Principal{}, false
	}
	want, err := hex.DecodeString(rec.SHA256)
	sum := sha256.Sum256([]byte(token))
	if err != nil || subtle.ConstantTimeCompare(sum[:], want) != 1 {
		return Principal{}, false
	}
	users, err := m.loadUsers()
	if err != nil {
		return Principal{}, false
	}
	u, ok := users[rec.OwnerEmail]
	if !ok {
		return Principal{}, false
	}
	subject := u.Subject
	if subject == "" {
		subject = "token:" + id
	}
	if rec.LastUsed == nil || m.now().Sub(*rec.LastUsed) >= lastUsedEvery {
		m.touchToken(id)
	}
	return m.principal(rec.OwnerEmail, u, subject), true
}

// touchToken sets last_used to now, at most once per lastUsedEvery. It is
// best effort: a failure must not fail the request.
func (m *Manager) touchToken(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, err := m.readToken(id)
	if err != nil {
		return // revoked meanwhile
	}
	now := m.now().UTC()
	if rec.LastUsed != nil && now.Sub(*rec.LastUsed) < lastUsedEvery {
		return
	}
	rec.LastUsed = &now
	m.writeToken(rec)
}

// listTokens returns the tokens of email, oldest first, never nil.
// Damaged files are skipped, like damaged run records (ruling 3.52).
func (m *Manager) listTokens(email string) ([]tokenInfo, error) {
	entries, err := os.ReadDir(filepath.Join(m.dir, "tokens"))
	if err != nil {
		return nil, err
	}
	out := []tokenInfo{}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !task.ValidID(id) {
			continue // a temporary file of writeAtomic
		}
		rec, err := m.readToken(id)
		if err != nil || rec.OwnerEmail != email {
			continue
		}
		out = append(out, tokenInfo{ID: rec.ID, Label: rec.Label, Created: rec.Created, LastUsed: rec.LastUsed})
	}
	slices.SortFunc(out, func(a, b tokenInfo) int {
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// revokeToken deletes token id of email. errNoToken when there is no such
// token or it belongs to someone else (the two are not told apart).
func (m *Manager) revokeToken(email, id string) error {
	if !task.ValidID(id) {
		return errNoToken
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, err := m.readToken(id)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && rec.OwnerEmail != email) {
		return errNoToken
	}
	if err != nil {
		return err
	}
	return os.Remove(m.tokenFile(id))
}
