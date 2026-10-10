package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// testIDP is a minimal OIDC provider: a discovery document, one RSA
// signing key published as a JWKS, and a token endpoint that issues a real
// RS256-signed ID token for whatever claims the test arms it with. It
// exists so oidc.go runs against coreos/go-oidc's real verification
// (signature over the JWKS key, issuer, audience, expiry, nonce) instead
// of a mocked oidc.Provider (Tech Stack; Review Focus 1).
type testIDP struct {
	srv *httptest.Server
	key *rsa.PrivateKey
	kid string

	code     string          // armed by setCode; "" refuses every exchange
	claims   map[string]any  // merged over iss/aud/iat/exp for the next /token response
	signWith *rsa.PrivateKey // armed by armWrongKey; overrides key for the next /token response only
}

func newTestIDP(t *testing.T) *testIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &testIDP{key: key, kid: "test-key-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", idp.discovery)
	mux.HandleFunc("GET /jwks", idp.jwks)
	mux.HandleFunc("POST /token", idp.token)
	// /authorize is never hit: these tests drive handleCallback directly
	// with a code they armed, the way a browser redirect cannot be driven
	// from a test without a real browser.
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (idp *testIDP) issuer() string { return idp.srv.URL }

func (idp *testIDP) discovery(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                idp.issuer(),
		"authorization_endpoint":                idp.issuer() + "/authorize",
		"token_endpoint":                        idp.issuer() + "/token",
		"jwks_uri":                              idp.issuer() + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"claims_supported":                      []string{"sub", "email", "email_verified", "name"},
	})
}

func (idp *testIDP) jwks(w http.ResponseWriter, r *http.Request) {
	pub := idp.key.PublicKey
	json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA", "kid": idp.kid, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}

// setCode arms the token endpoint: the next POST /token presenting this
// code succeeds, returning an ID token whose claims are iss/aud/iat/exp
// (filled in here) merged with extra.
func (idp *testIDP) setCode(code string, extra map[string]any) { idp.code, idp.claims = code, extra }

// armWrongKey makes the next /token response's ID token be signed with a
// key other than the one published in the JWKS (the kid header still names
// idp.kid, so go-oidc looks up the real public key by that kid and the
// signature check fails) — Review Focus 1: "an ID token signed with
// another key". It is consumed by the next token response, same as a code.
func (idp *testIDP) armWrongKey(t *testing.T) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp.signWith = key
}

func (idp *testIDP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || idp.code == "" || r.Form.Get("code") != idp.code {
		// The real OIDC/OAuth2 token endpoint always answers JSON with an
		// explicit Content-Type, on an error response too — found while
		// applying this plan: omitting it (so Go's server falls back to
		// sniffing, which does not recognize JSON and yields "text/plain")
		// makes golang.org/x/oauth2's AuthStyle auto-probe treat the
		// *first*, actually-successful exchange attempt as unparseable
		// and silently retry with the other auth style; the retry then
		// re-presents the same, by-then-already-consumed code and gets a
		// legitimate invalid_grant, which is the error that actually
		// surfaces to the caller instead of the real token.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	idp.code = "" // a code is used once
	// client_id arrives either in the form body (AuthStyleInParams) or as
	// the Basic auth username (AuthStyleInHeader) — golang.org/x/oauth2's
	// auth-style auto-probe tries the header style first, which succeeds
	// against this fixture (nothing here checks the client secret), so
	// the aud claim must be read from wherever it actually landed or
	// every real sign-in gets an empty audience and fails go-oidc's own
	// audience check; found while applying this plan.
	clientID := r.Form.Get("client_id")
	if clientID == "" {
		if u, _, ok := r.BasicAuth(); ok {
			clientID = u
		}
	}
	now := time.Now()
	claims := map[string]any{
		"iss": idp.issuer(),
		"aud": clientID,
		"iat": now.Unix(),
		"exp": now.Add(time.Minute).Unix(),
	}
	for k, v := range idp.claims {
		claims[k] = v
	}
	key := idp.key
	if idp.signWith != nil {
		key = idp.signWith
		idp.signWith = nil // consumed, same as code
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": "test-access-token",
		"token_type":   "Bearer",
		"id_token":     idp.signJWT(key, claims),
		"expires_in":   60,
	})
}

// signJWT returns claims as a compact RS256 JWS, signed with key (normally
// idp.key, but armWrongKey substitutes another one), built by hand so the
// test needs no JOSE library: base64url(header) + "." + base64url(payload)
// + "." + base64url(signature). The kid header always names idp.kid, the
// key id published in the JWKS, regardless of which private key actually
// signs — that mismatch is what models "signed with another key".
func (idp *testIDP) signJWT(key *rsa.PrivateKey, claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": idp.kid, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err) // a 2048-bit key never fails to sign a SHA-256 digest
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}
