package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// loginTTL bounds how long a sign-in may take before custos_login expires.
const loginTTL = 10 * time.Minute

// oidcClient is nil on a Manager built without Config.OIDC (token-only
// mode); handleLogin and handleCallback check for that themselves.
type oidcClient struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth2   oauth2.Config
}

// newOIDCClient discovers cfg.IssuerURL and builds the oauth2/go-oidc
// client. NewManager calls it once, at start-up, so a bad issuer URL or an
// unreachable IdP fails custos serve immediately rather than at the first
// /auth/login.
func newOIDCClient(ctx context.Context, cfg OIDCConfig) (*oidcClient, error) {
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("auth: OIDC discovery at %s: %w", cfg.IssuerURL, err)
	}
	return &oidcClient{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth2: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
	}, nil
}

// randString returns n random bytes, base64url-encoded, for state, nonce
// and (through oauth2.GenerateVerifier) the PKCE verifier.
func randString(n int) string {
	b := make([]byte, n)
	rand.Read(b) // crypto/rand never fails since Go 1.24
	return base64.RawURLEncoding.EncodeToString(b)
}

// handleLogin starts the authorization-code flow with PKCE: state, nonce
// and the PKCE verifier are generated, signed into custos_login, and the
// browser is sent to the IdP. 404 in token-only mode (Endpoints table).
func (m *Manager) handleLogin(w http.ResponseWriter, r *http.Request) {
	if m.oidc == nil {
		http.Error(w, "OIDC sign-in is not configured on this server", http.StatusNotFound)
		return
	}
	state, nonce, verifier := randString(24), randString(24), oauth2.GenerateVerifier()
	returnTo := "/"
	if v := r.URL.Query().Get("return_to"); strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") && !strings.Contains(v, "\\") {
		// "//host/.." is an open redirect a browser's URL parser resolves to
		// a different origin; so is a leading "/\" — browsers treat "\" the
		// same as "/" when resolving a URL, so "/\evil.com" against an
		// https: base also resolves off-origin even though it does not
		// start with "//" (found in code review). Rejecting any backslash
		// anywhere in the value, not just a leading one, is deliberately
		// broader than strictly necessary but keeps this check simple.
		// Everything else under "/" is the SPA's own routes.
		returnTo = v
	}
	m.setCookie(w, loginCookie, m.sign(claims{
		Kind: "login", State: state, Nonce: nonce, Verifier: verifier, Issuer: m.issuer,
		Return: returnTo, Exp: m.now().Add(loginTTL).Unix(),
	}), loginTTL)
	url := m.oidc.oauth2.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, url, http.StatusFound)
}

// handleCallback exchanges the authorization code, verifies the ID token
// through go-oidc's real verification path (issuer, audience, signature
// against the IdP's published JWKS, expiry — Review Focus 1), checks state
// and nonce against custos_login, upserts the user's name and OIDC subject
// into users.json (roles untouched — the architecture note), starts a
// session and clears custos_login.
func (m *Manager) handleCallback(w http.ResponseWriter, r *http.Request) {
	if m.oidc == nil {
		http.Error(w, "OIDC sign-in is not configured on this server", http.StatusNotFound)
		return
	}
	cookie, err := r.Cookie(loginCookie)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "no sign-in in progress; start at /auth/login")
		return
	}
	lc, err := m.verify(cookie.Value, "login")
	if err != nil || lc.Issuer != m.issuer {
		writeAuthError(w, http.StatusBadRequest, "sign-in state is missing, expired, or does not match this server")
		return
	}
	m.clearCookie(w, loginCookie)
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(lc.State)) != 1 {
		writeAuthError(w, http.StatusBadRequest, "state parameter does not match the sign-in that was started")
		return
	}
	if msg := r.URL.Query().Get("error"); msg != "" {
		writeAuthError(w, http.StatusUnauthorized, "identity provider refused sign-in: "+msg)
		return
	}
	token, err := m.oidc.oauth2.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(lc.Verifier))
	if err != nil {
		writeAuthError(w, http.StatusBadGateway, fmt.Sprintf("exchanging the authorization code: %v", err))
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		writeAuthError(w, http.StatusBadGateway, "identity provider did not return an id_token")
		return
	}
	idToken, err := m.oidc.verifier.Verify(r.Context(), raw)
	if err != nil {
		writeAuthError(w, http.StatusUnauthorized, fmt.Sprintf("invalid ID token: %v", err))
		return
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(lc.Nonce)) != 1 {
		writeAuthError(w, http.StatusUnauthorized, "ID token nonce does not match the sign-in that was started")
		return
	}
	var oc struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idToken.Claims(&oc); err != nil {
		writeAuthError(w, http.StatusUnauthorized, fmt.Sprintf("reading ID token claims: %v", err))
		return
	}
	if oc.Email == "" || !oc.EmailVerified {
		writeAuthError(w, http.StatusForbidden, "the identity provider did not report a verified email address")
		return
	}
	email, err := normalizeEmail(oc.Email)
	if err != nil {
		writeAuthError(w, http.StatusForbidden, err.Error())
		return
	}
	if _, err := m.UpdateUser(email, func(u *User) {
		if oc.Name != "" {
			u.Name = oc.Name
		}
		u.Subject = idToken.Subject
	}); err != nil {
		writeAuthError(w, http.StatusForbidden, fmt.Sprintf("%s cannot be used as a commit author: %v", email, err))
		return
	}
	m.setCookie(w, sessionCookie, m.sign(claims{
		Kind: "session", Email: email, Subject: idToken.Subject, Issuer: m.issuer,
		Exp: m.now().Add(m.ttl).Unix(),
	}), m.ttl)
	http.Redirect(w, r, lc.Return, http.StatusFound)
}

// writeAuthError writes a JSON error body in the same shape as
// api.WriteError, without importing api's unexported httpError (auth
// already imports api for Register and the Caller seam, so this is just
// avoiding one more unexported dependency, not a cycle concern).
func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}
