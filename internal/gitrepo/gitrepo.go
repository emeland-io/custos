// Package gitrepo runs the git commands custos needs on a repository.
package gitrepo

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing/fstest"
)

// Repo is a git repository, bare or not, at Dir.
type Repo struct {
	Dir string
}

// IsZero reports whether oid is the all-zero object ID git uses for a ref
// that does not exist yet or is being deleted.
func IsZero(oid string) bool {
	return oid != "" && strings.Trim(oid, "0") == ""
}

// InitBare creates a bare repository with main as its initial branch that
// accepts pushes over HTTP.
func InitBare(dir string) (*Repo, error) {
	if out, err := exec.Command("git", "init", "--quiet", "--bare", "--initial-branch=main", dir).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git init %s: %w: %s", dir, err, bytes.TrimSpace(out))
	}
	r := &Repo{Dir: dir}
	if _, err := r.git("config", "http.receivepack", "true"); err != nil {
		return nil, err
	}
	return r, nil
}

// InstallHook writes an executable hook script, replacing an existing one.
func (r *Repo) InstallHook(name, script string) error {
	path := filepath.Join(r.Dir, "hooks", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return err
	}
	return os.Chmod(path, 0o755)
}

// IsAncestor reports whether commit a is an ancestor of commit b.
func (r *Repo) IsAncestor(a, b string) (bool, error) {
	_, err := r.git("merge-base", "--is-ancestor", a, b)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return err == nil, err
}

// TreeFS returns the files of revision rev. Trees that custos reads hold a
// few thousand small files at most, so the whole tree is kept in memory.
// Symlinks are rejected, because custos cannot validate what they point to.
func (r *Repo) TreeFS(rev string) (fs.FS, error) {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return nil, fmt.Errorf("invalid revision %q", rev)
	}
	out, err := r.git("archive", "--format=tar", rev)
	if err != nil {
		return nil, err
	}
	files := fstest.MapFS{}
	tr := tar.NewReader(bytes.NewReader(out))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read tree of %s: %w", rev, err)
		}
		switch h.Typeflag {
		case tar.TypeDir, tar.TypeXGlobalHeader:
		case tar.TypeReg:
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			files[h.Name] = &fstest.MapFile{Data: data, Mode: 0o644}
		default:
			return nil, fmt.Errorf("%s: only regular files are supported, not symlinks", h.Name)
		}
	}
}

func (r *Repo) git(args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
