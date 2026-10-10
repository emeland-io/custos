package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/emeland-io/custos/internal/auth"
)

// defaultSessionTTL matches auth.Config's own default; named again here
// only because authFlagSet.config always sets SessionTTL explicitly
// (there is no --session-ttl flag yet — the architecture note does not
// ask for one, and nothing in this plan needs it configurable).
const defaultSessionTTL = 12 * time.Hour

// authFlagSet holds serve's auth flags (ruling 4.11: leaving
// --oidc-issuer-url empty runs custos in token-only mode, with no
// /auth/login).
type authFlagSet struct {
	issuerURL, clientID, clientSecret, redirectURL *string
	adminEmails, sessionSecret                     *string
}

// defineAuthFlags defines serve's auth flags on fl. Call it alongside
// publicURLFlag; its result's config method needs that flag's already-
// parsed value.
func defineAuthFlags(fl *flag.FlagSet) *authFlagSet {
	return &authFlagSet{
		issuerURL: fl.String("oidc-issuer-url", os.Getenv("CUSTOS_OIDC_ISSUER_URL"),
			"OIDC issuer URL; empty runs custos in token-only mode, with no /auth/login (env CUSTOS_OIDC_ISSUER_URL)"),
		clientID: fl.String("oidc-client-id", os.Getenv("CUSTOS_OIDC_CLIENT_ID"),
			"OIDC client id (env CUSTOS_OIDC_CLIENT_ID)"),
		clientSecret: fl.String("oidc-client-secret", os.Getenv("CUSTOS_OIDC_CLIENT_SECRET"),
			"OIDC client secret (env CUSTOS_OIDC_CLIENT_SECRET)"),
		redirectURL: fl.String("oidc-redirect-url", os.Getenv("CUSTOS_OIDC_REDIRECT_URL"),
			"URL the identity provider redirects back to; default <public-url>/auth/callback (env CUSTOS_OIDC_REDIRECT_URL)"),
		adminEmails: fl.String("admin-emails", os.Getenv("CUSTOS_ADMIN_EMAILS"),
			"comma-separated emails that always have the admin role, in addition to users.json (env CUSTOS_ADMIN_EMAILS)"),
		sessionSecret: fl.String("session-secret", os.Getenv("CUSTOS_SESSION_SECRET"),
			"hex-encoded HMAC key for session cookies, at least 32 bytes (64 hex digits); random and unique to this process if empty, so restarting forgets every session (env CUSTOS_SESSION_SECRET)"),
	}
}

// config builds an auth.Config from the parsed flags. publicURL is
// --public-url, already validated by checkPublicURL; it supplies
// --oidc-redirect-url's default.
func (f *authFlagSet) config(publicURL string) (auth.Config, error) {
	cfg := auth.Config{SessionTTL: defaultSessionTTL}
	if *f.sessionSecret != "" {
		secret, err := hex.DecodeString(*f.sessionSecret)
		if err != nil || len(secret) < 32 {
			return auth.Config{}, fmt.Errorf("--session-secret must be at least 32 bytes of hex (64 digits)")
		}
		cfg.SessionSecret = secret
	}
	for _, e := range strings.Split(*f.adminEmails, ",") {
		if e = strings.TrimSpace(e); e != "" {
			cfg.AdminEmails = append(cfg.AdminEmails, e)
		}
	}
	switch {
	case *f.issuerURL == "" && (*f.clientID != "" || *f.clientSecret != "" || *f.redirectURL != ""):
		return auth.Config{}, fmt.Errorf("--oidc-client-id, --oidc-client-secret and --oidc-redirect-url need --oidc-issuer-url")
	case *f.issuerURL == "":
		return cfg, nil // token-only mode
	case *f.clientID == "" || *f.clientSecret == "":
		return auth.Config{}, fmt.Errorf("--oidc-issuer-url needs --oidc-client-id and --oidc-client-secret")
	}
	redirect := *f.redirectURL
	if redirect == "" {
		redirect = strings.TrimRight(publicURL, "/") + "/auth/callback"
	}
	cfg.OIDC = &auth.OIDCConfig{IssuerURL: *f.issuerURL, ClientID: *f.clientID, ClientSecret: *f.clientSecret, RedirectURL: redirect}
	return cfg, nil
}
