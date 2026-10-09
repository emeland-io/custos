// Package proctest builds the container images that tests of processor runs
// use (spec §8): echo, generate, sign, fail and loop. Each image is
// FROM scratch plus one static Linux binary built from
// internal/proctest/images/<name>, so building needs Docker but no network.
// Only test code imports this package.
package proctest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// SigningKeySecret is the secret name SigningKey writes and the sign image
// reads.
const SigningKeySecret = "test-signing-key"

// Names lists the test images Image can build.
var Names = []string{"echo", "generate", "sign", "fail", "loop"}

var idRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type built struct {
	ref string
	err error
}

var (
	mu     sync.Mutex
	images = map[string]*built{}
)

// Image builds the named test image once per process and returns
// "custos.test/<name>@sha256:<image-id-hex>". Names: echo, generate, sign,
// fail, loop. Fails the test when Docker is unavailable.
func Image(t testing.TB, name string) string {
	t.Helper()
	mu.Lock()
	b, ok := images[name]
	if !ok {
		b = &built{}
		b.ref, b.err = build(name)
		images[name] = b
	}
	mu.Unlock()
	if b.err != nil {
		t.Fatalf("test image %s: %v", name, b.err)
	}
	return b.ref
}

func build(name string) (string, error) {
	known := false
	for _, n := range Names {
		known = known || n == name
	}
	if !known {
		return "", fmt.Errorf("unknown test image, want one of %s", strings.Join(Names, ", "))
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate the sources of package proctest")
	}
	src := filepath.Join(filepath.Dir(file), "images", name)
	dir, err := os.MkdirTemp("", "custos-proctest-"+name+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	gobuild := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", filepath.Join(dir, "processor"), ".")
	gobuild.Dir = src
	gobuild.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := gobuild.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v\n%s", err, out)
	}
	dockerfile := "FROM scratch\nCOPY processor /processor\nENTRYPOINT [\"/processor\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	docker := exec.Command("docker", "build", "--quiet", "--tag", "custos.test/"+name+":latest", dir)
	docker.Stdout, docker.Stderr = &stdout, &stderr
	if err := docker.Run(); err != nil {
		return "", fmt.Errorf("docker build (is Docker running?): %v\n%s", err, stderr.Bytes())
	}
	id := strings.TrimSpace(stdout.String())
	if !idRE.MatchString(id) {
		return "", fmt.Errorf("docker build printed %q, want an image id sha256:<64 hex digits>", id)
	}
	return "custos.test/" + name + "@" + id, nil
}

// SigningKey writes a fresh ed25519 private key (PKCS#8 PEM) as secret
// "test-signing-key" into a new temp directory and returns that secrets
// directory and the matching public key (PKIX PEM).
func SigningKey(t testing.TB) (secretsDir string, publicKeyPEM []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	secretsDir = t.TempDir()
	// 0644: the container drops all capabilities, so even its root user
	// can read only files the permissions allow.
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	if err := os.WriteFile(filepath.Join(secretsDir, SigningKeySecret), keyPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	return secretsDir, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
}
