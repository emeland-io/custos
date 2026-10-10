package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/api"
)

// sessionFor signs in email as a cookie header value ready for
// httptest.NewRequest + req.AddCookie, or req.Header.Set("Cookie", ...).
func sessionFor(t *testing.T, m *Manager, email string) *http.Cookie {
	t.Helper()
	return &http.Cookie{Name: sessionCookie, Value: m.sign(claims{
		Kind: "session", Email: email, Subject: "token:test", Issuer: m.issuer, Exp: m.now().Add(time.Hour).Unix(),
	})}
}

func TestMiddlewareResolvesSessionAndToken(t *testing.T) {
	m := newManager(t)
	addUser(t, m, "jane@example.org", "Jane Doe", Roles{TaskAuthor: true})
	tok := addUser(t, m, "joe@example.org", "Joe", Roles{Admin: true})

	ok := func(w http.ResponseWriter, r *http.Request) {
		p, signedIn := FromRequest(r)
		if !signedIn {
			w.WriteHeader(http.StatusTeapot) // distinguishable "no caller" marker
			return
		}
		w.Header().Set("X-Email", p.Email)
	}
	h := m.Middleware()(http.HandlerFunc(ok))

	req := httptest.NewRequest("GET", "/api/catalog", nil)
	req.AddCookie(sessionFor(t, m, "jane@example.org"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Email") != "jane@example.org" {
		t.Errorf("session: X-Email %q", rec.Header().Get("X-Email"))
	}

	req = httptest.NewRequest("GET", "/api/catalog", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Email") != "joe@example.org" {
		t.Errorf("bearer: X-Email %q", rec.Header().Get("X-Email"))
	}

	// resolve() passes the whole Basic password straight to lookupToken,
	// which expects the full "<id>.<secret>" bearer value, not the secret
	// half alone — found while applying this plan: an earlier version of
	// this line used cutToken to split tok and sent only the secret half,
	// which lookupToken then (correctly) rejected as malformed.
	req = httptest.NewRequest("GET", "/api/catalog", nil)
	req.SetBasicAuth("x", tok) // git sends the token as the Basic password; the username is ignored
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Email") != "joe@example.org" {
		t.Errorf("basic: X-Email %q", rec.Header().Get("X-Email"))
	}

	req = httptest.NewRequest("GET", "/api/catalog", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Errorf("no credential: code %d, want the handler's own 418 (no caller, not a 401 from Middleware itself)", rec.Code)
	}
}

func TestMiddlewareExcludedPrefixes(t *testing.T) {
	m := newManager(t)
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if _, ok := FromRequest(r); ok {
			t.Error("excluded path resolved a caller from a cookie it was never asked to check")
		}
	})
	h := m.Middleware("/healthz", "/auth/login")(next)
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "garbage-that-would-otherwise-be-rejected-not-ignored"})
	httptest.NewRecorder()
	h.ServeHTTP(httptest.NewRecorder(), req)
	if calls != 1 {
		t.Fatalf("calls %d", calls)
	}
}

// TestMiddlewareCrossOrigin is Review Focus 2: an unsafe request whose only
// credential is the session cookie or Basic (both ambient: a browser
// attaches them on its own) must resolve to nobody when it is cross-origin;
// a Bearer token, which a cross-site page cannot attach by itself, is
// unaffected.
func TestMiddlewareCrossOrigin(t *testing.T) {
	m := newManager(t)
	tok := addUser(t, m, "jane@example.org", "Jane Doe", Roles{Admin: true})
	h := m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromRequest(r); ok {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))

	post := func(withToken bool, sameOrigin bool) int {
		req := httptest.NewRequest("POST", "/api/workspaces", nil)
		req.Host = "custos.example.org"
		if withToken {
			req.Header.Set("Authorization", "Bearer "+tok)
		} else {
			req.AddCookie(sessionFor(t, m, "jane@example.org"))
		}
		if !sameOrigin {
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Origin", "https://evil.example.org")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := post(false, true); got != http.StatusOK {
		t.Errorf("same-origin cookie write: %d", got)
	}
	if got := post(false, false); got != http.StatusUnauthorized {
		t.Errorf("cross-origin cookie write: %d, want 401 (resolves to nobody)", got)
	}
	if got := post(true, false); got != http.StatusOK {
		t.Errorf("cross-origin bearer-token write: %d, want 200 (a cross-site page cannot attach this header itself)", got)
	}
}

func TestRequireAndRequireWorkspace(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces", Require(RoleAdmin, ok))
	mux.HandleFunc("PUT /api/workspaces/{id}/answers/{task}", RequireWorkspace("id", ok))

	req := httptest.NewRequest("POST", "/api/workspaces", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no caller: %d", rec.Code)
	}

	req = httptest.NewRequest("POST", "/api/workspaces", nil)
	req = req.WithContext(api.WithCaller(req.Context(), Principal{Email: "jane@example.org", Roles: Roles{Admin: true}}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("admin: %d", rec.Code)
	}

	req = httptest.NewRequest("PUT", "/api/workspaces/"+ws1+"/answers/"+ws1, nil)
	req = req.WithContext(api.WithCaller(req.Context(), Principal{Email: "jane@example.org", Roles: Roles{Engineer: []string{ws1}}}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("engineer of %s: %d", ws1, rec.Code)
	}

	req = httptest.NewRequest("PUT", "/api/workspaces/"+ws2+"/answers/"+ws1, nil)
	req = req.WithContext(api.WithCaller(req.Context(), Principal{Email: "jane@example.org", Roles: Roles{Engineer: []string{ws1}}}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("engineer of another workspace: %d", rec.Code)
	}
}

// cutToken splits a bearer value into its id and secret, as the Basic
// password half of TestMiddlewareResolvesSessionAndToken needs.
func cutToken(token string) (id, secret string, ok bool) {
	return strings.Cut(token, ".")
}
