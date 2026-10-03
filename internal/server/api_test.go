package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emeland-io/custos/internal/store"
)

func TestWithAPI(t *testing.T) {
	st, err := store.Open(t.TempDir(), "/custos", "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "api "+r.URL.Path)
	})
	h, err := New(st).WithAPI(api).Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "api /api/workspaces" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Body.String() != "ok\n" {
		t.Errorf("healthz %q", rec.Body.String())
	}

	plain, err := New(st).Handler()
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	plain.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("without WithAPI: %d", rec.Code)
	}
}
