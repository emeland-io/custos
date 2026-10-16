package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
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

// TestHandlerServesIndexHtmlDirectlyWithoutRedirect reproduces a review
// finding against an earlier version of this handler: a direct GET of
// /index.html (as a bookmark, a crawler, or a health check might send)
// resolved to a real file that fs.Stat found, so the handler fell through
// to http.FileServer — and FileServer's serveFile 301-redirects any
// request path ending in "/index.html" to its parent directory. That made
// a direct hit on the real file redirect instead of render, the same bug
// pattern the SPA-fallback fix was supposed to eliminate everywhere, just
// on a different branch. Asserts 200, no Location header, and the index
// content, not a redirect.
func TestHandlerServesIndexHtmlDirectlyWithoutRedirect(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (not a redirect)", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "" {
		t.Fatalf("got Location header %q, want none (no redirect)", loc)
	}
	if !strings.Contains(rr.Body.String(), "custos") {
		t.Errorf("body = %q, want it to mention custos", rr.Body.String())
	}
}

// TestHandlerServesRealAssetWithContentType exercises the http.FileServer
// branch against a real, non-index file so the extension-derived
// Content-Type is asserted by a test rather than only checked by hand.
// The committed dist/ (ruling 4.2) has no asset files to embed for this,
// so this builds a small in-memory fs.FS via handlerFor directly instead
// of going through Handler()/the real embedded dist/.
func TestHandlerServesRealAssetWithContentType(t *testing.T) {
	sub := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>custos</html>")},
		"style.css":  &fstest.MapFile{Data: []byte("body { color: red; }")},
	}
	h := handlerFor(sub)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/style.css", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Errorf("Content-Type = %q, want it to contain text/css", ct)
	}
	if rr.Body.String() != "body { color: red; }" {
		t.Errorf("body = %q, want the real asset's contents", rr.Body.String())
	}
}
