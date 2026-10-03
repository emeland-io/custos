package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/blobs"
)

func TestBlobUploadAndDownload(t *testing.T) {
	e := newEnv(t, nil)
	got := decode[blobJSON](t, e.do(t, "POST", "/api/blobs", janeHeader, "scan results\n"), http.StatusCreated)
	if got.SHA256 != hash("scan results\n") || got.Size != 13 {
		t.Fatalf("got %+v", got)
	}
	rec := e.do(t, "GET", "/api/blobs/"+got.SHA256, "", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "scan results\n" {
		t.Fatalf("GET: %d %q", rec.Code, rec.Body.String())
	}
	if ct, cl := rec.Header().Get("Content-Type"), rec.Header().Get("Content-Length"); ct != "application/octet-stream" || cl != "13" {
		t.Errorf("Content-Type %q Content-Length %q", ct, cl)
	}
	rec = e.do(t, "HEAD", "/api/blobs/"+got.SHA256, "", "")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "13" {
		t.Errorf("HEAD: %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Length"))
	}
}

func TestBlobUploadNeedsAuthor(t *testing.T) {
	e := newEnv(t, nil)
	decode[errorJSON](t, e.do(t, "POST", "/api/blobs", "", "secret"), http.StatusUnauthorized)
	if e.bl.Has(hash("secret")) {
		t.Error("blob stored without an author")
	}
}

func TestBlobUploadTooLarge(t *testing.T) {
	e := newEnv(t, nil)
	req := httptest.NewRequest("POST", "/api/blobs", strings.NewReader("x"))
	req.ContentLength = blobs.MaxSize + 1
	req.Header.Set("X-Custos-Author", janeHeader)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	decode[errorJSON](t, rec, http.StatusRequestEntityTooLarge)
	if e.bl.Has(hash("x")) {
		t.Error("oversized upload was read")
	}
}

func TestBlobUnknown(t *testing.T) {
	e := newEnv(t, nil)
	for _, sha := range []string{hash("missing"), "not-a-hash"} {
		body := decode[errorJSON](t, e.do(t, "GET", "/api/blobs/"+sha, "", ""), http.StatusNotFound)
		if !strings.Contains(body.Error, "unknown blob") {
			t.Errorf("%s: %q", sha, body.Error)
		}
	}
}
