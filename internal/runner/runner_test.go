package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/proctest"
)

const input = `{"contract":"custos.processor/v1","workspace":{"id":"5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9"}}`

func TestRunEcho(t *testing.T) {
	r := New(Config{})
	res, err := r.Run(context.Background(), Job{Image: proctest.Image(t, "echo"), Input: []byte(input)})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.TimedOut || res.Duration <= 0 {
		t.Fatalf("result %+v, log %s", res, res.Log)
	}
	out, err := contract.ParseOutput(res.Stdout)
	if err != nil {
		t.Fatalf("%v\nstdout: %s", err, res.Stdout)
	}
	if len(out.Tasks) != 0 || len(out.Documents) != 1 {
		t.Fatalf("output %+v", out)
	}
	if d := out.Documents[0]; d.MatchKey != "input" || d.Name != "input" || d.MediaType != "application/json" || string(d.Content) != input {
		t.Errorf("document %+v, content %s", d, d.Content)
	}
}

func TestRunFailKeepsLogAndExitCode(t *testing.T) {
	res, err := New(Config{}).Run(context.Background(), Job{Image: proctest.Image(t, "fail"), Input: []byte(input)})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.TimedOut || string(res.Log) != "boom\n" || len(res.Stdout) != 0 {
		t.Errorf("result %+v, log %q", res, res.Log)
	}
}

func TestRunTimeoutKillsAndRemovesContainer(t *testing.T) {
	img := proctest.Image(t, "loop")
	marker := filepath.Join(t.TempDir(), "marker") // tells this test's container from others
	writeFile(t, marker, "x")
	start := time.Now()
	res, err := New(Config{}).Run(context.Background(), Job{
		Image: img, Timeout: 2 * time.Second, Blobs: map[string]string{strings.Repeat("ef", 32): marker},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.ExitCode != -1 {
		t.Errorf("result %+v", res)
	}
	if d := time.Since(start); d < 2*time.Second || d > 20*time.Second {
		t.Errorf("run took %v, want about 2s", d)
	}
	if left := containersOf(t, img, marker); len(left) != 0 {
		t.Errorf("containers left behind: %v", left)
	}
}

func TestRunDefaultTimeout(t *testing.T) {
	res, err := New(Config{DefaultTimeout: time.Second}).Run(context.Background(), Job{Image: proctest.Image(t, "loop")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Errorf("result %+v, want a timeout from Config.DefaultTimeout", res)
	}
}

func TestRunCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(time.Second, cancel)
	_, err := New(Config{}).Run(ctx, Job{Image: proctest.Image(t, "loop"), Timeout: time.Minute})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
		t.Errorf("err %v, want context.Canceled", err)
	}
}

// TestRunCancelledBeforeResolve cancels ctx before Run even calls Resolve, so
// the container is never created. The resulting error must still wrap
// ctx.Err(), not ErrUnavailable, even though the docker calls inside Resolve
// fail because ctx is done.
func TestRunCancelledBeforeResolve(t *testing.T) {
	img := proctest.Image(t, "echo") // a pinned local image: resolves without a pull
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(Config{}).Run(ctx, Job{Image: img})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
		t.Errorf("err %v, want context.Canceled, not ErrUnavailable", err)
	}
}

// TestRunCancelledBeforePull cancels ctx before Run calls ensureImage for an
// image that is not present locally, so Run would otherwise attempt a
// docker pull. That pull fails because ctx is already done; the resulting
// error must still wrap ctx.Err(), not ErrUnavailable.
func TestRunCancelledBeforePull(t *testing.T) {
	remote := "registry.example.org/host-scanner@sha256:" + strings.Repeat("1", 64)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(Config{}).Run(ctx, Job{Image: remote})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
		t.Errorf("err %v, want context.Canceled, not ErrUnavailable", err)
	}
}

// TestRunSandbox inspects a running container: the flags of §5.2 and the
// read-only mounts of secrets and attachments.
func TestRunSandbox(t *testing.T) {
	img := proctest.Image(t, "loop")
	secrets := t.TempDir()
	writeFile(t, filepath.Join(secrets, "api-token"), "s3cret")
	blob := filepath.Join(t.TempDir(), "blob")
	writeFile(t, blob, "attachment")
	sha := strings.Repeat("ab", 32)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := New(Config{SecretsDir: secrets, Memory: "256m"}).Run(ctx, Job{
			Image: img, Timeout: time.Minute, Secrets: []string{"api-token"}, Blobs: map[string]string{sha: blob},
		})
		done <- err
	}()
	var ids []string
	for deadline := time.Now().Add(30 * time.Second); len(ids) == 0 && time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		ids = containersOf(t, img, blob)
	}
	if len(ids) != 1 {
		t.Fatalf("running containers with the blob mounted: %v", ids)
	}
	var info []struct {
		HostConfig struct {
			ReadonlyRootfs bool
			NetworkMode    string
			CapDrop        []string
			SecurityOpt    []string
			Memory         int64
			MemorySwap     int64
			Tmpfs          map[string]string
		}
		Mounts []struct {
			Source, Destination string
			RW                  bool
		}
	}
	out, err := exec.Command("docker", "container", "inspect", ids[0]).Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out, &info); err != nil || len(info) != 1 {
		t.Fatalf("inspect: %v\n%s", err, out)
	}
	hc := info[0].HostConfig
	if !hc.ReadonlyRootfs || hc.NetworkMode != "none" || strings.Join(hc.CapDrop, ",") != "ALL" ||
		strings.Join(hc.SecurityOpt, ",") != "no-new-privileges" || hc.Memory != 256<<20 || hc.MemorySwap != 256<<20 {
		t.Errorf("host config %+v", hc)
	}
	if _, ok := hc.Tmpfs["/tmp"]; !ok {
		t.Errorf("no tmpfs at /tmp: %v", hc.Tmpfs)
	}
	mounts := map[string]bool{}
	for _, m := range info[0].Mounts {
		if m.RW {
			t.Errorf("mount %s is writable", m.Destination)
		}
		mounts[m.Destination] = true
	}
	if !mounts["/run/secrets/api-token"] || !mounts["/input/blobs/"+sha] {
		t.Errorf("mounts %+v", info[0].Mounts)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("err %v", err)
	}
}

func TestRunNetworkAllowed(t *testing.T) {
	img := proctest.Image(t, "loop")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	marker := filepath.Join(t.TempDir(), "marker")
	writeFile(t, marker, "x")
	done := make(chan error, 1)
	go func() {
		_, err := New(Config{}).Run(ctx, Job{Image: img, Timeout: time.Minute, Network: true, Blobs: map[string]string{strings.Repeat("cd", 32): marker}})
		done <- err
	}()
	var ids []string
	for deadline := time.Now().Add(30 * time.Second); len(ids) == 0 && time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		ids = containersOf(t, img, marker)
	}
	if len(ids) != 1 {
		t.Fatalf("containers %v", ids)
	}
	out, err := exec.Command("docker", "container", "inspect", "--format", "{{.HostConfig.NetworkMode}}", ids[0]).Output()
	if err != nil || strings.TrimSpace(string(out)) == "none" {
		t.Errorf("network mode %q, err %v", out, err)
	}
	cancel()
	<-done
}

func TestRunUnavailable(t *testing.T) {
	echo := proctest.Image(t, "echo")
	secrets := t.TempDir()
	for _, c := range []struct {
		name string
		cfg  Config
		job  Job
		want string
	}{
		{"missing runtime", Config{Runtime: "/nonexistent/docker"}, Job{Image: echo}, "/nonexistent/docker"},
		{"no secrets dir", Config{}, Job{Image: echo, Secrets: []string{"api-token"}}, "no secrets directory"},
		{"missing secret", Config{SecretsDir: secrets}, Job{Image: echo, Secrets: []string{"api-token"}}, "secret api-token"},
		{"bad secret name", Config{SecretsDir: secrets}, Job{Image: echo, Secrets: []string{"../etc/passwd"}}, "must match"},
		{"missing blob", Config{}, Job{Image: echo, Blobs: map[string]string{strings.Repeat("0", 64): filepath.Join(secrets, "nope")}}, "attachment"},
		{"bad blob hash", Config{}, Job{Image: echo, Blobs: map[string]string{"x": "/etc/hosts"}}, "64 lowercase hex"},
		{"unknown local image", Config{}, Job{Image: "custos.test/does-not-exist:latest"}, "does-not-exist"},
		{"unpullable image", Config{}, Job{Image: "custos.invalid/none@sha256:" + strings.Repeat("0", 64)}, "pulling custos.invalid/none"},
		{"empty image", Config{}, Job{Image: ""}, "invalid image reference"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := New(c.cfg).Run(context.Background(), c.job)
			if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("result %+v, err %v; want ErrUnavailable naming %q", res, err, c.want)
			}
		})
	}
}

func TestRunOutputIsCapped(t *testing.T) {
	big := `{"pad":"` + strings.Repeat("x", contract.MaxOutputSize) + `"}`
	res, err := New(Config{}).Run(context.Background(), Job{Image: proctest.Image(t, "echo"), Input: []byte(big)})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || len(res.Stdout) != contract.MaxOutputSize+1 {
		t.Fatalf("exit %d, stdout %d bytes, want %d", res.ExitCode, len(res.Stdout), contract.MaxOutputSize+1)
	}
	if _, err := contract.ParseOutput(res.Stdout); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err %v", err)
	}
}

func TestCapped(t *testing.T) {
	c := &capped{limit: 5, note: "[cut]"}
	for _, p := range []string{"abc", "def", "ghi"} {
		if n, err := c.Write([]byte(p)); n != 3 || err != nil {
			t.Fatalf("Write %q: %d, %v", p, n, err)
		}
	}
	if got := string(c.bytes()); got != "abcde[cut]" {
		t.Errorf("got %q", got)
	}
	c = &capped{limit: 5, note: "[cut]"}
	c.Write([]byte("abcde"))
	if got := string(c.bytes()); got != "abcde" {
		t.Errorf("exactly at the limit: got %q", got)
	}
}

func TestResolve(t *testing.T) {
	r := New(Config{})
	ctx := context.Background()
	pinned := proctest.Image(t, "echo")
	id := pinned[strings.Index(pinned, "@")+1:]

	ref, digest, err := r.Resolve(ctx, pinned)
	if err != nil || ref != id || digest != id {
		t.Errorf("pinned local image: ref %q, digest %q, err %v; want %q twice", ref, digest, err, id)
	}
	// Confirm independently (not via the cached id from proctest.Image) that
	// the tag currently points at the id Resolve is expected to return, so
	// this assertion does not just trust proctest's cache.
	freshID, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", "custos.test/echo:latest").Output()
	if err != nil {
		t.Fatalf("docker image inspect custos.test/echo:latest: %v", err)
	}
	wantDigest := strings.TrimSpace(string(freshID))
	ref, digest, err = r.Resolve(ctx, "custos.test/echo:latest")
	if err != nil || ref != "custos.test/echo:latest" || digest != wantDigest {
		t.Errorf("local tag: ref %q, digest %q, err %v; want digest %q (fresh docker image inspect)", ref, digest, err, wantDigest)
	}
	remote := "registry.example.org/host-scanner@sha256:" + strings.Repeat("1", 64)
	ref, digest, err = r.Resolve(ctx, remote)
	if err != nil || ref != remote || digest != "sha256:"+strings.Repeat("1", 64) {
		t.Errorf("remote: ref %q, digest %q, err %v", ref, digest, err)
	}
	if _, _, err := r.Resolve(ctx, "custos.test/does-not-exist:latest"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("unknown local image: err %v", err)
	}
}

// containersOf lists the IDs of containers of image img (running or not)
// that mount the host file source.
func containersOf(t *testing.T, img, source string) []string {
	t.Helper()
	id := img[strings.Index(img, "@")+1:]
	out, err := exec.Command("docker", "ps", "--all", "--quiet", "--no-trunc", "--filter", "ancestor="+id).Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	var ids []string
	for _, c := range strings.Fields(string(out)) {
		m, err := exec.Command("docker", "container", "inspect", "--format", "{{json .Mounts}}", c).Output()
		if err != nil || !bytes.Contains(m, []byte(source)) {
			continue
		}
		ids = append(ids, c)
	}
	return ids
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
