package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/carabiner-dev/signer"
	"github.com/carabiner-dev/signer/key"
	"github.com/carabiner-dev/signer/options"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/attest/carabiner"
	"github.com/emeland-io/custos/internal/model"
	"github.com/emeland-io/custos/internal/seed"
	"github.com/emeland-io/custos/internal/store"
)

type client struct {
	t   *testing.T
	srv *httptest.Server
}

func (c client) do(method, path string, body any, wantStatus int, out any) {
	c.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		r = bytes.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			c.t.Fatal(err)
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.srv.URL+path, r)
	if err != nil {
		c.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, wantStatus, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			c.t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
}

func newServer(t *testing.T, v *carabiner.Verifier) client {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(t.TempDir(), t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Store:    st,
		Verifier: v,
		Keys: func() []attest.Key {
			var out []attest.Key
			for _, k := range v.Keys() {
				out = append(out, k.Key)
			}
			return out
		},
		UI: fstest.MapFS{
			"index.html":    {Data: []byte("<html>spa</html>")},
			"assets/app.js": {Data: []byte("console.log(1)")},
		},
		Log: log,
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return client{t: t, srv: srv}
}

func signedStatement(t *testing.T, k *key.Private, predicateType, subject string) []byte {
	t.Helper()
	stmt := fmt.Sprintf(`{"_type":"https://in-toto.io/Statement/v1","predicateType":%q,"subject":[{"name":%q,"digest":{"sha256":"%s"}}],"predicate":{}}`,
		predicateType, subject, strings.Repeat("0", 64))
	s := signer.NewSigner()
	env, err := s.SignStatementToDSSE([]byte(stmt), options.WithKey(k))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := s.WriteDSSEEnvelope(env, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWorkflow(t *testing.T) {
	trustedKey, err := key.NewGenerator().GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	unknownKey, err := key.NewGenerator().GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	tk, err := carabiner.NewTrustedKey("trusted.pub", trustedKey)
	if err != nil {
		t.Fatal(err)
	}
	v := carabiner.New(carabiner.WithKeys(tk))
	c := newServer(t, v)
	const pt = "https://example.com/review/v1"

	var entry, child model.Node
	c.do("POST", "/api/nodes", map[string]any{
		"displayName":         "Release",
		"requiredAttestation": map[string]any{"predicateType": pt, "requiredIdentities": []string{tk.Identity}},
		"missingReason":       "Review pending",
	}, http.StatusCreated, &entry)
	c.do("POST", "/api/nodes", map[string]any{"displayName": "Build", "parentId": entry.ID}, http.StatusCreated, &child)
	var leaf model.Leaf
	c.do("POST", "/api/leaves", map[string]any{"parentId": child.ID, "description": "# Build it"}, http.StatusCreated, &leaf)
	var root model.Root
	c.do("POST", "/api/roots", map[string]any{"displayName": "Product", "entryNodeId": entry.ID}, http.StatusCreated, &root)
	var sd model.Seed
	c.do("POST", "/api/seeds", map[string]any{"displayName": "v1.0", "rootId": root.ID}, http.StatusCreated, &sd)

	var tree store.TreeNode
	c.do("GET", "/api/roots/"+root.ID.String()+"/tree", nil, http.StatusOK, &tree)
	if len(tree.Children) != 1 || len(tree.Children[0].Leaves) != 1 {
		t.Fatalf("tree: %+v", tree)
	}
	var path []model.Node
	c.do("GET", "/api/leaves/"+leaf.ID.String()+"/path", nil, http.StatusOK, &path)
	if len(path) != 2 || path[1].ID != child.ID {
		t.Errorf("path: %+v", path)
	}

	var st seed.Status
	c.do("GET", "/api/seeds/"+sd.ID.String()+"/status", nil, http.StatusOK, &st)
	if len(st.MissingShoots) != 1 || len(st.MissingAttestations) != 2 {
		t.Fatalf("status: %+v", st)
	}

	c.do("POST", "/api/shoots", map[string]any{"seedId": sd.ID, "leafId": leaf.ID, "content": "built"}, http.StatusCreated, nil)

	// An attestation signed with an unknown key is stored but not met.
	var att model.Attestation
	c.do("POST", fmt.Sprintf("/api/seeds/%s/nodes/%s/attestations", sd.ID, entry.ID),
		signedStatement(t, unknownKey, pt, "app"), http.StatusCreated, &att)
	if att.Verification.Status != model.StatusFailed || att.Verification.RequirementMet {
		t.Errorf("unknown key: %+v", att.Verification)
	}
	// Once the key is trusted and required, verifying again meets it.
	uk, err := carabiner.NewTrustedKey("unknown.pub", unknownKey)
	if err != nil {
		t.Fatal(err)
	}
	v.SetKeys([]carabiner.TrustedKey{tk, uk})
	var keys []attest.Key
	c.do("GET", "/api/keys", nil, http.StatusOK, &keys)
	if len(keys) != 2 {
		t.Errorf("keys: %+v", keys)
	}
	c.do("POST", "/api/attestations/"+att.ID.String()+"/verify", nil, http.StatusOK, &att)
	if att.Verification.Status != model.StatusVerified || att.Verification.RequirementMet {
		t.Errorf("re-verified, but the node requires the other key: %+v", att.Verification)
	}

	c.do("POST", fmt.Sprintf("/api/seeds/%s/nodes/%s/attestations", sd.ID, entry.ID),
		signedStatement(t, trustedKey, pt, "app"), http.StatusCreated, &att)
	if !att.Verification.RequirementMet || att.Format != attest.FormatDSSE || att.PredicateType != pt {
		t.Errorf("trusted key: %+v", att)
	}

	c.do("GET", "/api/seeds/"+sd.ID.String()+"/status", nil, http.StatusOK, &st)
	if st.AnsweredLeaves != 1 || st.AttestedNodes != 1 || len(st.MissingAttestations) != 1 || st.MissingAttestations[0].Node.ID != child.ID {
		t.Errorf("status after work: %+v", st)
	}
	var atts []model.Attestation
	c.do("GET", "/api/seeds/"+sd.ID.String()+"/attestations", nil, http.StatusOK, &atts)
	if len(atts) != 2 || atts[0].Raw != nil {
		t.Errorf("seed attestations should omit raw: %d", len(atts))
	}

	// New leaf version: the shoot becomes stale.
	var leaf2 model.Leaf
	c.do("POST", "/api/leaves/"+leaf.ID.String()+"/versions",
		map[string]any{"parentId": child.ID, "description": "# Build it twice", "version": "2"}, http.StatusCreated, &leaf2)
	c.do("GET", "/api/seeds/"+sd.ID.String()+"/status", nil, http.StatusOK, &st)
	if st.AnsweredLeaves != 0 || len(st.MissingShoots) != 1 || len(st.MissingShoots[0].Stale) != 1 {
		t.Errorf("stale shoot: %+v", st.MissingShoots)
	}
	var hist []model.Leaf
	c.do("GET", "/api/leaves/"+leaf2.ID.String()+"/history", nil, http.StatusOK, &hist)
	if len(hist) != 2 {
		t.Errorf("history: %+v", hist)
	}
	c.do("PUT", "/api/leaves/"+leaf.ID.String(), map[string]any{"parentId": child.ID}, http.StatusConflict, nil)
}

func TestErrors(t *testing.T) {
	c := newServer(t, carabiner.New())
	var n model.Node
	c.do("POST", "/api/nodes", map[string]any{"displayName": "n"}, http.StatusCreated, &n)
	var root model.Root
	c.do("POST", "/api/roots", map[string]any{"displayName": "r", "entryNodeId": n.ID}, http.StatusCreated, &root)
	var sd model.Seed
	c.do("POST", "/api/seeds", map[string]any{"displayName": "s", "rootId": root.ID}, http.StatusCreated, &sd)
	c.do("POST", "/api/leaves", map[string]any{"parentId": n.ID}, http.StatusCreated, nil)

	c.do("GET", "/api/nodes/not-a-uuid", nil, http.StatusBadRequest, nil)
	c.do("GET", "/api/nodes/"+sd.ID.String(), nil, http.StatusNotFound, nil)
	c.do("POST", "/api/nodes", []byte(`{`), http.StatusBadRequest, nil)
	c.do("POST", "/api/seeds", map[string]any{"displayName": "s", "rootId": n.ID}, http.StatusBadRequest, nil)
	c.do("DELETE", "/api/nodes/"+n.ID.String(), nil, http.StatusConflict, nil)
	c.do("PATCH", "/api/nodes", nil, http.StatusMethodNotAllowed, nil)
	c.do("GET", "/api/nothing", nil, http.StatusNotFound, nil)

	upload := fmt.Sprintf("/api/seeds/%s/nodes/%s/attestations", sd.ID, n.ID)
	c.do("POST", upload, []byte(`not json`), http.StatusBadRequest, nil)
	c.do("POST", upload, []byte(`{"hello":"world"}`), http.StatusBadRequest, nil)
	c.do("POST", upload, bytes.Repeat([]byte(" "), MaxBodySize+1), http.StatusRequestEntityTooLarge, nil)

	var att model.Attestation
	c.do("POST", upload, []byte(`{"_type":"https://in-toto.io/Statement/v1","predicateType":"x","subject":[],"predicate":{}}`), http.StatusCreated, &att)
	if att.Verification.Status != model.StatusUnsigned || att.Format != attest.FormatBare {
		t.Errorf("bare upload: %+v", att)
	}
}

func TestSPA(t *testing.T) {
	c := newServer(t, carabiner.New())
	for path, want := range map[string]string{
		"/":              "<html>spa</html>",
		"/seeds/123":     "<html>spa</html>",
		"/assets/app.js": "console.log(1)",
	} {
		resp, err := http.Get(c.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != want {
			t.Errorf("%s: %d %q", path, resp.StatusCode, body)
		}
	}
}
