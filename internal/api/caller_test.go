package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
)

// fakeCaller holds the roles named in has: "admin", or "engineer@<workspace>".
type fakeCaller struct {
	sig gitrepo.Signature
	has map[string]bool
}

func (c fakeCaller) Signature() gitrepo.Signature { return c.sig }
func (c fakeCaller) Has(role, workspaceID string) bool {
	return c.has[role] || c.has[role+"@"+workspaceID]
}

func TestCallerOf(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/workspaces", nil)
	if c, ok := CallerOf(req); ok {
		t.Errorf("caller %+v without WithCaller", c)
	}
	req = req.WithContext(WithCaller(req.Context(), nil))
	if _, ok := CallerOf(req); ok {
		t.Error("a nil caller counts as a caller")
	}
	req = req.WithContext(WithCaller(req.Context(), fakeCaller{sig: jane}))
	if c, ok := CallerOf(req); !ok || c.Signature() != jane {
		t.Errorf("caller %+v %v", c, ok)
	}
}

func TestRequireRole(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces", RequireRole("admin", "", ok))
	mux.HandleFunc("PUT /api/workspaces/{id}/answers/{task}", RequireRole("engineer", "id", ok))
	ws := fixture.WorkspaceID
	answer := "/api/workspaces/" + ws + "/answers/" + fixture.TaskA
	for _, tc := range []struct {
		name, method, path string
		caller             Caller // nil: not signed in
		want               int
		msg                string // part of the error message
	}{
		{"not signed in", "POST", "/api/workspaces", nil, http.StatusUnauthorized, "not signed in"},
		{"no role", "POST", "/api/workspaces", fakeCaller{sig: jane}, http.StatusForbidden, "needs the admin role"},
		{"admin", "POST", "/api/workspaces", fakeCaller{sig: jane, has: map[string]bool{"admin": true}}, http.StatusNoContent, ""},
		{"engineer of the workspace", "PUT", answer, fakeCaller{sig: jane, has: map[string]bool{"engineer@" + ws: true}}, http.StatusNoContent, ""},
		{"engineer of another workspace", "PUT", answer, fakeCaller{sig: jane, has: map[string]bool{"engineer@" + otherWorkspace: true}},
			http.StatusForbidden, "needs the engineer role for workspace " + ws},
		{"anonymous engineer request", "PUT", answer, nil, http.StatusUnauthorized, "not signed in"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.caller != nil {
			req = req.WithContext(WithCaller(req.Context(), tc.caller))
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if tc.want == http.StatusNoContent {
			if rec.Code != tc.want {
				t.Errorf("%s: status %d, body %s", tc.name, rec.Code, rec.Body)
			}
			continue
		}
		body := decode[errorJSON](t, rec, tc.want)
		if !strings.Contains(body.Error, tc.msg) {
			t.Errorf("%s: error %q, want it to contain %q", tc.name, body.Error, tc.msg)
		}
		if rec.Header().Get("WWW-Authenticate") != "" {
			t.Errorf("%s: WWW-Authenticate set; the API must not trigger a browser's password dialog", tc.name)
		}
	}
}
