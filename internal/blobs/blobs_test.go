package blobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func open(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "blobs")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestPutAndOpen(t *testing.T) {
	s, dir := open(t)
	sha, size, err := s.Put(strings.NewReader("hello\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sha != hash("hello\n") || size != 6 {
		t.Fatalf("sha %s size %d", sha, size)
	}
	if !s.Has(sha) {
		t.Error("Has = false after Put")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "sha256", sha)); err != nil || string(data) != "hello\n" {
		t.Errorf("stored file: %q %v", data, err)
	}
	rc, n, err := s.Open(sha)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil || string(data) != "hello\n" || n != 6 {
		t.Errorf("Open: %q size %d %v", data, n, err)
	}
}

func TestPutEmpty(t *testing.T) {
	s, _ := open(t)
	sha, size, err := s.Put(strings.NewReader(""))
	if err != nil || sha != hash("") || size != 0 {
		t.Errorf("sha %s size %d err %v", sha, size, err)
	}
}

func TestPutIsIdempotent(t *testing.T) {
	s, dir := open(t)
	for range 2 {
		if _, _, err := s.Put(strings.NewReader("same")); err != nil {
			t.Fatal(err)
		}
	}
	if got := names(t, filepath.Join(dir, "sha256")); len(got) != 1 || got[0] != hash("same") {
		t.Errorf("sha256 holds %v", got)
	}
	if got := names(t, filepath.Join(dir, "tmp")); len(got) != 0 {
		t.Errorf("temporary files left: %v", got)
	}
}

func TestPutTooLarge(t *testing.T) {
	s, dir := open(t)
	s.limit = 4
	if _, _, err := s.Put(strings.NewReader("12345")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err %v, want ErrTooLarge", err)
	}
	if _, _, err := s.Put(strings.NewReader("1234")); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	if got := names(t, filepath.Join(dir, "sha256")); len(got) != 1 || got[0] != hash("1234") {
		t.Errorf("sha256 holds %v", got)
	}
	if got := names(t, filepath.Join(dir, "tmp")); len(got) != 0 {
		t.Errorf("temporary files left: %v", got)
	}
}

func TestPutReadError(t *testing.T) {
	s, dir := open(t)
	_, _, err := s.Put(iotest.ErrReader(errors.New("connection reset")))
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("err %v", err)
	}
	if got := names(t, filepath.Join(dir, "sha256")); len(got) != 0 {
		t.Errorf("sha256 holds %v", got)
	}
	if got := names(t, filepath.Join(dir, "tmp")); len(got) != 0 {
		t.Errorf("temporary files left: %v", got)
	}
}

func TestOpenUnknown(t *testing.T) {
	s, _ := open(t)
	for _, sha := range []string{hash("never stored"), "../../etc/passwd", strings.ToUpper(hash("x")), ""} {
		if _, _, err := s.Open(sha); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Open(%q): %v, want fs.ErrNotExist", sha, err)
		}
		if s.Has(sha) {
			t.Errorf("Has(%q) = true", sha)
		}
	}
}

func TestValidSHA(t *testing.T) {
	if !ValidSHA(hash("x")) {
		t.Error("a sha256 hex digest must be valid")
	}
	for _, s := range []string{"", "abc", strings.ToUpper(hash("x")), hash("x")[:63], hash("x") + "0"} {
		if ValidSHA(s) {
			t.Errorf("ValidSHA(%q) = true", s)
		}
	}
}
