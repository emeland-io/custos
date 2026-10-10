// Package auth authenticates the people who use custos (spec §6.4): OIDC
// sign-in in the browser with a signed session cookie, and personal access
// tokens for Git over HTTP, the CLI and scripts. It keeps who has which
// role in files under <data-dir>/auth (ruling 4.8), resolves the caller of
// every request (Manager.Middleware, FromRequest), guards endpoints by role
// (Require, RequireWorkspace) and serves /auth/* and the admin endpoints
// (Register).
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/emeland-io/custos/internal/gitrepo"
)

// The roles of §6.4.
const (
	RoleAdmin           = "admin"
	RoleTaskAuthor      = "task-author"
	RoleProcessorAuthor = "processor-author"
	RoleEngineer        = "engineer"
)

// AllWorkspaces as an entry of Roles.Engineer grants the engineer role for
// every workspace (ruling 4.9).
const AllWorkspaces = "*"

// Principal is the authenticated identity of a request: either a human who
// signed in through OIDC, or the owner of a personal access token. Name and
// Email make a gitrepo.Signature for commits the person's actions cause.
type Principal struct {
	Subject string // OIDC "sub", or "token:<token id>" for a PAT-only identity with no OIDC login on record
	Name    string
	Email   string
	Roles   Roles
}

// Signature is the principal as the author of commits.
func (p Principal) Signature() gitrepo.Signature {
	return gitrepo.Signature{Name: p.Name, Email: p.Email}
}

// Has reports whether p holds role (see Roles.Has). It makes Principal an
// api.Caller.
func (p Principal) Has(role, workspaceID string) bool { return p.Roles.Has(role, workspaceID) }

// Roles. Engineer is scoped per workspace; the others are global. Admin
// implies every other role everywhere (§6.4: "admin: create and fork
// workspaces, manage roles").
type Roles struct {
	Admin           bool     `json:"admin"`
	TaskAuthor      bool     `json:"task_author"`
	ProcessorAuthor bool     `json:"processor_author"`
	Engineer        []string `json:"engineer"` // workspace ids; "*" grants every workspace (ruling 4.9)
}

// Has reports whether r holds role, one of RoleAdmin, RoleTaskAuthor,
// RoleProcessorAuthor and RoleEngineer. workspaceID is ignored except for
// RoleEngineer, where an empty workspaceID asks for "engineer of any
// workspace". Admin always has every role; an unknown role nobody else has.
func (r Roles) Has(role string, workspaceID string) bool {
	if r.Admin {
		return true
	}
	switch role {
	case RoleTaskAuthor:
		return r.TaskAuthor
	case RoleProcessorAuthor:
		return r.ProcessorAuthor
	case RoleEngineer:
		for _, id := range r.Engineer {
			if workspaceID == "" || id == AllWorkspaces || id == workspaceID {
				return true
			}
		}
	}
	return false
}

// normalized returns r with Engineer sorted, without duplicates, and an
// empty list instead of nil, the way users.json and every JSON answer of
// this package list it.
func (r Roles) normalized() Roles {
	eng := slices.Clone(r.Engineer)
	slices.Sort(eng)
	eng = slices.Compact(eng)
	if eng == nil {
		eng = []string{}
	}
	r.Engineer = eng
	return r
}

// Config configures a Manager. One per process, created by cmd/custos.
type Config struct {
	DataDir       string
	SessionSecret []byte        // HMAC-SHA256 key for session cookies; random if empty (ruling 4.5: sessions do not survive a restart unless the operator sets one)
	SessionTTL    time.Duration // default 12h
	AdminEmails   []string      // users whose email is in this list get Admin on every login/token use, in addition to whatever is on file (ruling 4.6)
	OIDC          *OIDCConfig   // nil disables OIDC login entirely (token-only mode, e.g. CI/tests)
}

// OIDCConfig names the identity provider and how custos is registered there.
type OIDCConfig struct {
	IssuerURL, ClientID, ClientSecret, RedirectURL string
}

// defaultSessionTTL is the lifetime of a session when Config.SessionTTL is 0.
const defaultSessionTTL = 12 * time.Hour

// Manager owns the identity store under <data-dir>/auth and issues and
// verifies sessions and tokens. It caches nothing from the files: every
// request reads users.json and its token's file, so changes made by another
// process (custos token create) apply at once.
type Manager struct {
	dir         string        // <data-dir>/auth
	secret      []byte        // HMAC-SHA256 key of the session and sign-in cookies
	ttl         time.Duration // lifetime of a session
	adminEmails []string      // lower case
	issuer      string        // OIDC issuer URL; "" in token-only mode, which accepts no session
	secure      bool          // cookies carry the Secure attribute (the redirect URL is https)
	cop         *http.CrossOriginProtection
	now         func() time.Time // tests replace it
	oidc        *oidcClient      // nil in token-only mode (Config.OIDC == nil)

	mu sync.Mutex // serialises read-modify-write of users.json and of token files within this process
}

// NewManager loads or creates <data-dir>/auth/users.json and its token
// directory. With cfg.OIDC set it also discovers the identity provider and
// fails if the issuer is unreachable.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("auth: no data directory")
	}
	m := &Manager{
		dir:    filepath.Join(cfg.DataDir, "auth"),
		secret: bytes.Clone(cfg.SessionSecret),
		ttl:    cfg.SessionTTL,
		cop:    http.NewCrossOriginProtection(),
		now:    time.Now,
	}
	if err := os.MkdirAll(filepath.Join(m.dir, "tokens"), 0o700); err != nil {
		return nil, err
	}
	if len(m.secret) == 0 {
		m.secret = make([]byte, 32)
		rand.Read(m.secret) // never fails (crypto/rand since Go 1.24)
	}
	if m.ttl <= 0 {
		m.ttl = defaultSessionTTL
	}
	for _, e := range cfg.AdminEmails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			m.adminEmails = append(m.adminEmails, e)
		}
	}
	if _, err := m.loadUsers(); err != nil { // a damaged users.json stops start-up
		return nil, err
	}
	if cfg.OIDC != nil {
		client, err := newOIDCClient(context.Background(), *cfg.OIDC)
		if err != nil {
			return nil, err
		}
		m.oidc = client
		m.issuer = cfg.OIDC.IssuerURL
		m.secure = strings.HasPrefix(cfg.OIDC.RedirectURL, "https://")
	}
	return m, nil
}
