package blobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPath(t *testing.T) {
	s, _ := open(t)
	sha, _, err := s.Put(strings.NewReader("scan"))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := s.Path(sha)
	if !ok || !filepath.IsAbs(p) {
		t.Fatalf("Path = %q, %v", p, ok)
	}
	if data, err := os.ReadFile(p); err != nil || string(data) != "scan" {
		t.Errorf("content %q, %v", data, err)
	}
	if _, ok := s.Path(hash("missing")); ok {
		t.Error("a missing blob has no path")
	}
	if _, ok := s.Path("../../etc/passwd"); ok {
		t.Error("a malformed hash has no path")
	}
}
