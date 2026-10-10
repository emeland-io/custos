package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/auth"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
)

// allCaller is a signed-in caller holding every role, for tests of
// openAPI/server wiring that are not themselves about authentication —
// internal/server's own tests (TestAPINeedsSignIn and siblings) cover the
// sign-in gate itself.
type allCaller struct{}

func (allCaller) Signature() gitrepo.Signature      { return gitrepo.Signature{} }
func (allCaller) Has(role, workspaceID string) bool { return true }

func TestOpenAPI(t *testing.T) {
	st, err := store.Open(t.TempDir(), "/custos", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	a, err := openAPI(st)
	if err != nil {
		t.Fatal(err)
	}
	h, err := server.New(st).WithAPI(a.Handler()).Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces", nil)
	req = req.WithContext(api.WithCaller(req.Context(), allCaller{}))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(st.DataDir(), "blobs", "sha256")); err != nil {
		t.Errorf("blob store not created: %v", err)
	}
}

// TestOpenServerServesAPI checks the wiring serve uses.
func TestOpenServerServesAPI(t *testing.T) {
	data := t.TempDir()
	srv, err := openServer(t.Context(), data, "http://127.0.0.1:8080", defaultProcessorOptions(), auth.Config{DataDir: data}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	token := bootstrapAdmin(t, data)
	req := httptest.NewRequest("GET", "/api/workspaces", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
}
