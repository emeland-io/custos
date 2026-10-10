package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Cookie names. custos_login never reaches a route handler: Middleware
// reads it only inside handleLogin/handleCallback.
const (
	sessionCookie = "custos_session"
	loginCookie   = "custos_login"
)

// claims is the signed payload of both cookies. Kind tells them apart so
// that one cookie's bytes, presented under the other cookie's name, verify
// as neither (Review Focus 1). The login fields are empty on a session and
// vice versa.
type claims struct {
	Kind     string `json:"kind"` // "session" or "login"
	Email    string `json:"email,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Issuer   string `json:"issuer,omitempty"`   // the Manager.issuer a session or login was started against
	State    string `json:"state,omitempty"`    // login only: the OIDC "state"
	Nonce    string `json:"nonce,omitempty"`    // login only: the OIDC nonce
	Verifier string `json:"verifier,omitempty"` // login only: the PKCE code verifier
	Return   string `json:"return,omitempty"`   // login only: where to send the browser after the callback
	Exp      int64  `json:"exp"`                // unix seconds
}

// errBadCookie covers every way a cookie can fail to verify — malformed,
// wrongly signed, expired, wrong kind — without telling the caller which,
// so a 400 cannot be used to probe the difference.
var errBadCookie = errors.New("auth: invalid, expired or tampered cookie")

// sign returns c as a cookie value: base64url(json) + "." +
// base64url(HMAC-SHA256(secret, json)).
func (m *Manager) sign(c claims) string {
	data, _ := json.Marshal(c) // claims never fails to marshal
	mac := hmac.New(sha256.New, m.secret)
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verify checks value's signature, expiry and kind (one of sessionCookie's
// "session" or loginCookie's "login", passed as want). Everything that can
// go wrong collapses to errBadCookie.
func (m *Manager) verify(value, want string) (claims, error) {
	payload, sig, ok := strings.Cut(value, ".")
	if !ok || payload == "" || sig == "" {
		return claims{}, errBadCookie
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return claims{}, errBadCookie
	}
	wantMAC, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return claims{}, errBadCookie
	}
	mac := hmac.New(sha256.New, m.secret)
	mac.Write(data)
	if subtle.ConstantTimeCompare(mac.Sum(nil), wantMAC) != 1 {
		return claims{}, errBadCookie
	}
	var c claims
	if err := json.Unmarshal(data, &c); err != nil {
		return claims{}, errBadCookie
	}
	if c.Kind != want || c.Exp == 0 || !m.now().Before(time.Unix(c.Exp, 0)) {
		return claims{}, errBadCookie
	}
	return c, nil
}

// setCookie sets name to value, valid for ttl, HttpOnly, SameSite=Lax (the
// OIDC redirect back from the IdP is a top-level GET navigation, which Lax
// still sends the cookie on) and Secure when m.secure.
func (m *Manager) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true, Secure: m.secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()),
	})
}

// clearCookie deletes name from the browser.
func (m *Manager) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", HttpOnly: true, Secure: m.secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}
