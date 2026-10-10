package main

import (
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/auth"
)

func TestAuthConfigTokenOnlyMode(t *testing.T) {
	_, err := parseAuthFlagsConfig(t)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthConfigOIDC(t *testing.T) {
	cfg, err := parseAuthFlagsConfig(t,
		"--oidc-issuer-url", "https://idp.example.org",
		"--oidc-client-id", "custos",
		"--oidc-client-secret", "s3cr3t",
		"--admin-emails", " Jane@Example.org , ,bob@example.org",
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDC == nil || cfg.OIDC.IssuerURL != "https://idp.example.org" || cfg.OIDC.RedirectURL != "http://127.0.0.1:8080/auth/callback" {
		t.Fatalf("OIDC %+v", cfg.OIDC)
	}
	if len(cfg.AdminEmails) != 2 || cfg.AdminEmails[0] != "Jane@Example.org" || cfg.AdminEmails[1] != "bob@example.org" {
		t.Fatalf("AdminEmails %+v", cfg.AdminEmails)
	}
}

func TestAuthConfigRedirectURLOverride(t *testing.T) {
	cfg, err := parseAuthFlagsConfig(t,
		"--oidc-issuer-url", "https://idp.example.org", "--oidc-client-id", "c", "--oidc-client-secret", "s",
		"--oidc-redirect-url", "https://custos.example.org/auth/callback",
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDC.RedirectURL != "https://custos.example.org/auth/callback" {
		t.Errorf("RedirectURL %q", cfg.OIDC.RedirectURL)
	}
}

func TestAuthConfigRejects(t *testing.T) {
	for name, args := range map[string][]string{
		"issuer without client id":     {"--oidc-issuer-url", "https://idp.example.org", "--oidc-client-secret", "s"},
		"issuer without client secret": {"--oidc-issuer-url", "https://idp.example.org", "--oidc-client-id", "c"},
		"client id without issuer":     {"--oidc-client-id", "c"},
		"redirect url without issuer":  {"--oidc-redirect-url", "https://x/callback"},
		"session secret too short":     {"--session-secret", "ab"},
		"session secret not hex":       {"--session-secret", strings.Repeat("zz", 32)},
	} {
		if _, err := parseAuthFlagsConfig(t, args...); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestAuthConfigSessionSecret(t *testing.T) {
	cfg, err := parseAuthFlagsConfig(t, "--session-secret", strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SessionSecret) != 32 {
		t.Errorf("SessionSecret %d bytes", len(cfg.SessionSecret))
	}
}

// parseAuthFlagsConfig builds an authFlagSet the way runServe will in
// Task 8, without needing the rest of serve's flags, and returns its
// built Config.
func parseAuthFlagsConfig(t *testing.T, args ...string) (auth.Config, error) {
	t.Helper()
	fl := flagSetForTest(t)
	af := defineAuthFlags(fl)
	if err := fl.Parse(args); err != nil {
		return auth.Config{}, err
	}
	return af.config("http://127.0.0.1:8080")
}

func flagSetForTest(t *testing.T) *flag.FlagSet {
	t.Helper()
	fl := flag.NewFlagSet("test", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	return fl
}
