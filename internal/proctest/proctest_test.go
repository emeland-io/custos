package proctest

import (
	"os/exec"
	"strings"
	"testing"
)

func TestImageBuildsOncePerProcess(t *testing.T) {
	ref := Image(t, "echo")
	name, id, ok := strings.Cut(ref, "@")
	if !ok || name != "custos.test/echo" || !idRE.MatchString(id) {
		t.Fatalf("ref %q, want custos.test/echo@sha256:<64 hex>", ref)
	}
	out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", id).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != id {
		t.Fatalf("docker image inspect %s: %v\n%s", id, err, out)
	}
	if again := Image(t, "echo"); again != ref {
		t.Errorf("second call returned %q, want the cached %q", again, ref)
	}
}

func TestImageUnknownName(t *testing.T) {
	if _, err := build("nope"); err == nil || !strings.Contains(err.Error(), "unknown test image") {
		t.Errorf("err %v", err)
	}
}
