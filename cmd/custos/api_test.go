package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/server"
	"github.com/emeland-io/custos/internal/store"
)

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
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(st.DataDir(), "blobs", "sha256")); err != nil {
		t.Errorf("blob store not created: %v", err)
	}
}

// TestOpenServerServesAPI checks the wiring serve uses.
func TestOpenServerServesAPI(t *testing.T) {
	srv, err := openServer(t.Context(), t.TempDir(), "http://127.0.0.1:8080", defaultProcessorOptions(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	h, err := srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
}
