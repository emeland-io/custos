package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildHostlistImage builds the Go SDK's example processor
// (sdk/go/examples/hostlist, a module of its own) as a static Linux binary
// in a FROM scratch image tagged custos.test/hostlist:sdk, and returns the
// image ID without "sha256:".
func buildHostlistImage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, "hostlist"), "./examples/hostlist")
	build.Dir = filepath.Join("..", "..", "sdk", "go")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH, "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	dockerfile := "FROM scratch\nCOPY hostlist /hostlist\nUSER 65532:65532\nENTRYPOINT [\"/hostlist\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("docker", "build", "-q", "-t", "custos.test/hostlist:sdk", dir).Output()
	if err != nil {
		t.Fatalf("docker build: %v", err)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "sha256:")
}

// The SDK example runs through the real runner, addressed by a tag as a
// processor author would during development (Resolve inspects the image).
func TestProcessorTestGoSDKExample(t *testing.T) {
	id := buildHostlistImage(t)
	answer := writeAnswer(t, t.TempDir(), markdownAnswer("# production\n- web-01\n- db-01\n"))

	code, out, errs := runCmd(t, "processor", "test", "custos.test/hostlist:sdk", "--answer", answer)
	if code != 0 || errs != "" {
		t.Fatalf("exit code %d, stderr %q\nstdout:\n%s", code, errs, out)
	}
	golden(t, "hostlist", normalize(out, id))

	empty := writeAnswer(t, t.TempDir(), markdownAnswer("# nothing yet\n"))
	code, out, errs = runCmd(t, "processor", "test", "custos.test/hostlist:sdk", "--answer", empty)
	if code != 1 || errs != "" {
		t.Fatalf("exit code %d, stderr %q\nstdout:\n%s", code, errs, out)
	}
	golden(t, "hostlist-empty", normalize(out, id))
}
