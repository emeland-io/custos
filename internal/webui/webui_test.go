package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexAtRoot(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "custos") {
		t.Errorf("body = %q, want it to mention custos", rr.Body.String())
	}
}

func TestHandlerFallsBackToIndexForAClientRoute(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/w/00000000-0000-4000-8000-000000000000", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "custos") {
		t.Errorf("body = %q, want the index.html shell", rr.Body.String())
	}
}

func TestHandler404sAnUnknownAsset(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/assets/does-not-exist.js", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
