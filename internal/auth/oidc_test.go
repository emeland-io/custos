package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newOIDCManager returns a Manager configured against idp.
func newOIDCManager(t *testing.T, idp *testIDP) *Manager {
	t.Helper()
	m, err := NewManager(Config{DataDir: t.TempDir(), OIDC: &OIDCConfig{
		IssuerURL: idp.issuer(), ClientID: "custos", ClientSecret: "s3cr3t",
		RedirectURL: "https://custos.example.org/auth/callback",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestHandleLoginRedirectsWithPKCEAndSetsLoginCookie(t *testing.T) {
	idp := newTestIDP(t)
	m := newOIDCManager(t, idp)
	req := httptest.NewRequest("GET", "/auth/login", nil)
	rec := httptest.NewRecorder()
	m.handleLogin(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("code %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || !strings.HasPrefix(loc.String(), idp.issuer()+"/authorize") {
		t.Fatalf("Location %q", rec.Header().Get("Location"))
	}
	if loc.Query().Get("code_challenge") == "" || loc.Query().Get("code_challenge_method") != "S256" {
		t.Errorf("no PKCE challenge: %v", loc.Query())
	}
	if loc.Query().Get("state") == "" {
		t.Error("no state")
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != loginCookie {
		t.Fatalf("cookies %+v", cookies)
	}
}

func TestHandleLoginTokenOnlyMode(t *testing.T) {
	m := newManager(t) // Config.OIDC is nil
	rec := httptest.NewRecorder()
	m.handleLogin(rec, httptest.NewRequest("GET", "/auth/login", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("code %d", rec.Code)
	}
}

// runLogin drives handleLogin and returns the state/nonce/verifier it
// picked, by parsing its own redirect, so TestHandleCallback* below do not
// need to re-derive PKCE by hand.
func runLogin(t *testing.T, m *Manager) (loginCookieHeader string, state string) {
	t.Helper()
	rec := httptest.NewRecorder()
	m.handleLogin(rec, httptest.NewRequest("GET", "/auth/login", nil))
	loc, _ := url.Parse(rec.Header().Get("Location"))
	return rec.Result().Cookies()[0].String(), loc.Query().Get("state")
}

func callback(t *testing.T, m *Manager, loginCookieHeader, state, code string) *httptest.ResponseRecorder {
	t.Helper()
	// Built with url.Values, not raw string concatenation: TestOIDCCallbackRejects
	// below arms codes like "code-no email" (the map key, which contains a
	// space), and a literal space in a hand-built target string makes
	// httptest.NewRequest panic trying to parse it as a raw HTTP request
	// line — found while applying this plan.
	q := url.Values{"state": {state}, "code": {code}}
	req := httptest.NewRequest("GET", "/auth/callback?"+q.Encode(), nil)
	req.Header.Set("Cookie", loginCookieHeader)
	rec := httptest.NewRecorder()
	m.handleCallback(rec, req)
	return rec
}

func TestHandleCallbackSignsIn(t *testing.T) {
	idp := newTestIDP(t)
	m := newOIDCManager(t, idp)
	// Named loginCookieHeader, not loginCookie, so the comparison below
	// against the real cookie-name constant (package-level loginCookie,
	// session.go) isn't shadowed by this function's own local variable —
	// found while applying this plan: as "loginCookie, state :=
	// runLogin(...)" the local shadowed the constant, so
	// "c.Name == loginCookie" compared a short cookie name against a
	// whole Set-Cookie header string and could never be true, silently
	// defeating the "was cleared" check below.
	loginCookieHeader, state := runLogin(t, m)
	idp.setCode("the-code", map[string]any{
		"sub": "idp-sub-1", "email": "jane@example.org", "email_verified": true, "name": "Jane Doe",
		"nonce": nonceFromState(t, m, loginCookieHeader),
	})
	rec := callback(t, m, loginCookieHeader, state, "the-code")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("code %d location %q body %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			session = c
		}
		if c.Name == loginCookie && c.MaxAge >= 0 {
			t.Error("custos_login was not cleared")
		}
	}
	if session == nil {
		t.Fatal("no session cookie")
	}
	claims, err := m.verify(session.Value, "session")
	if err != nil || claims.Email != "jane@example.org" || claims.Subject != "idp-sub-1" {
		t.Fatalf("session claims %+v err %v", claims, err)
	}
	u, _ := m.loadUsers()
	if u["jane@example.org"].Name != "Jane Doe" || u["jane@example.org"].Subject != "idp-sub-1" {
		t.Errorf("user %+v", u["jane@example.org"])
	}
}

// TestOIDCCallbackRejects and TestOIDCCallbackState are Review Focus 1: an
// ID token signed with another key, for another audience, from another
// issuer, expired, or with another nonce must not create a session; nor
// must a replayed code, a changed state, or an IdP error parameter.
func TestOIDCCallbackRejects(t *testing.T) {
	idp := newTestIDP(t)
	for name, claims := range map[string]map[string]any{
		"unverified email": {"sub": "s", "email": "jane@example.org", "email_verified": false},
		"no email":         {"sub": "s", "email_verified": true},
		"wrong audience":   {"sub": "s", "email": "jane@example.org", "email_verified": true, "aud": "someone-else"},
		"wrong issuer":     {"sub": "s", "email": "jane@example.org", "email_verified": true, "iss": "https://not-the-real-issuer.example.org"},
		"expired":          {"sub": "s", "email": "jane@example.org", "email_verified": true, "exp": time.Now().Add(-time.Hour).Unix()},
		"wrong nonce":      {"sub": "s", "email": "jane@example.org", "email_verified": true, "nonce": "not-the-real-nonce"},
	} {
		m := newOIDCManager(t, idp)
		loginCookie, state := runLogin(t, m)
		if _, ok := claims["nonce"]; !ok {
			claims["nonce"] = nonceFromState(t, m, loginCookie)
		}
		idp.setCode("code-"+name, claims)
		rec := callback(t, m, loginCookie, state, "code-"+name)
		if rec.Code == http.StatusFound {
			t.Errorf("%s: callback succeeded", name)
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == sessionCookie && c.Value != "" {
				t.Errorf("%s: a session cookie was set", name)
			}
		}
	}

	// An ID token signed with a key other than the one published in the
	// IdP's JWKS must not verify, even though every other claim is valid.
	{
		m := newOIDCManager(t, idp)
		loginCookie, state := runLogin(t, m)
		idp.armWrongKey(t)
		idp.setCode("code-wrong-key", map[string]any{
			"sub": "s", "email": "jane@example.org", "email_verified": true,
			"nonce": nonceFromState(t, m, loginCookie),
		})
		rec := callback(t, m, loginCookie, state, "code-wrong-key")
		if rec.Code == http.StatusFound {
			t.Error("wrong key: callback succeeded")
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == sessionCookie && c.Value != "" {
				t.Error("wrong key: a session cookie was set")
			}
		}
	}

	// A replayed authorization code: the second exchange must fail because
	// testIDP's /token consumes the code once, which stands in for
	// go-oidc/oauth2's own single-use exchange against the IdP.
	m := newOIDCManager(t, idp)
	loginCookie, state := runLogin(t, m)
	idp.setCode("reuse-me", map[string]any{"sub": "s", "email": "jane@example.org", "email_verified": true, "nonce": nonceFromState(t, m, loginCookie)})
	if rec := callback(t, m, loginCookie, state, "reuse-me"); rec.Code != http.StatusFound {
		t.Fatalf("first use: %d", rec.Code)
	}
	loginCookie2, state2 := runLogin(t, m)
	idp.claims["nonce"] = nonceFromState(t, m, loginCookie2) // same armed code, new login
	if rec := callback(t, m, loginCookie2, state2, "reuse-me"); rec.Code == http.StatusFound {
		t.Error("replayed code succeeded")
	}
}

func TestOIDCCallbackState(t *testing.T) {
	idp := newTestIDP(t)
	m := newOIDCManager(t, idp)
	loginCookie, _ := runLogin(t, m)
	idp.setCode("code", map[string]any{"sub": "s", "email": "jane@example.org", "email_verified": true, "nonce": nonceFromState(t, m, loginCookie)})
	if rec := callback(t, m, loginCookie, "not-the-real-state", "code"); rec.Code == http.StatusFound {
		t.Error("wrong state succeeded")
	}
	if rec := callback(t, m, "custos_login=garbage", "x", "code"); rec.Code == http.StatusFound {
		t.Error("tampered login cookie succeeded")
	}
	req := httptest.NewRequest("GET", "/auth/callback?error=access_denied", nil) // no custos_login cookie at all
	rec := httptest.NewRecorder()
	m.handleCallback(rec, req)
	if rec.Code == http.StatusFound {
		t.Error("no login cookie at all succeeded")
	}
}

// nonceFromState decodes the nonce the manager put in its own login
// cookie, so the test's armed claims can match it without the test
// reimplementing handleLogin's random generation. loginCookieHeader is the
// Set-Cookie value runLogin returned ("custos_login=<value>; Path=/; ...").
func nonceFromState(t *testing.T, m *Manager, loginCookieHeader string) string {
	t.Helper()
	_, value, _ := strings.Cut(loginCookieHeader, loginCookie+"=")
	value, _, _ = strings.Cut(value, ";")
	c, err := m.verify(value, "login")
	if err != nil {
		t.Fatal(err)
	}
	return c.Nonce
}
