package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	m := newManager(t)
	m.issuer = "https://idp.example.org"
	value := m.sign(claims{Kind: "session", Email: "jane@example.org", Subject: "sub-1", Issuer: m.issuer, Exp: m.now().Add(time.Hour).Unix()})
	c, err := m.verify(value, "session")
	if err != nil || c.Email != "jane@example.org" || c.Subject != "sub-1" {
		t.Fatalf("claims %+v, err %v", c, err)
	}
}

// TestSessionTampering is Review Focus 1: none of these variants of a
// signed cookie must verify.
func TestSessionTampering(t *testing.T) {
	m := newManager(t)
	m.issuer = "https://idp.example.org"
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	good := claims{Kind: "session", Email: "jane@example.org", Subject: "sub-1", Issuer: m.issuer, Exp: now.Add(time.Hour).Unix()}
	value := m.sign(good)
	payload, sig, _ := strings.Cut(value, ".")

	other := newManager(t) // a different random secret
	expired := m.sign(claims{Kind: "session", Email: "jane@example.org", Issuer: m.issuer, Exp: now.Add(-time.Second).Unix()})
	loginShaped := m.sign(claims{Kind: "login", Email: "jane@example.org", Issuer: m.issuer, Exp: now.Add(time.Hour).Unix()})
	// wrongIssuer is signed with m's own secret, not a copy of m with a
	// different issuer field: verify() does not itself check the issuer
	// (resolve(), one layer up in middleware.go, does that) — so the only
	// thing that actually needs to differ here is claims.Issuer, which
	// the test's own "want" computation checks below. Found while applying
	// this plan: `otherIssuer := *m; otherIssuer.issuer = "..."` both
	// copies a Manager containing a sync.Mutex (a go vet failure) and
	// signs `good`, whose Issuer field is already m.issuer and never
	// changes — so that case produced a token byte-identical to the
	// "good (control)" one, silently testing nothing.
	wrongIssuer := m.sign(claims{Kind: "session", Email: "jane@example.org", Subject: "sub-1", Issuer: "https://other-idp.example.org", Exp: now.Add(time.Hour).Unix()})

	for name, value := range map[string]string{
		"good (control)":             value,
		"no dot":                     payload + sig,
		"payload changed":            flip(payload) + "." + sig,
		"signed with another secret": other.sign(good),
		"wrong issuer in claims":     wrongIssuer,
		"expired":                    expired,
		"really the login cookie":    loginShaped,
		"empty":                      "",
		"garbage":                    "not-a-cookie",
	} {
		c, err := m.verify(value, "session")
		ok := err == nil && c.Issuer == m.issuer
		want := name == "good (control)"
		if ok != want {
			t.Errorf("%s: verify ok=%v err=%v, want ok=%v", name, ok, err, want)
		}
	}
}

func TestSetAndClearCookie(t *testing.T) {
	m := newManager(t)
	rec := httptest.NewRecorder()
	m.setCookie(rec, sessionCookie, "value", time.Hour)
	resp := rec.Result()
	if len(resp.Cookies()) != 1 {
		t.Fatalf("cookies %+v", resp.Cookies())
	}
	c := resp.Cookies()[0]
	if c.Name != sessionCookie || c.Value != "value" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie %+v", c)
	}
	rec = httptest.NewRecorder()
	m.clearCookie(rec, sessionCookie)
	c = rec.Result().Cookies()[0]
	if c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("clearCookie: %+v", c)
	}
}
