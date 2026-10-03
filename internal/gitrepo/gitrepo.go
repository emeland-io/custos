// Package gitrepo runs the git commands custos needs on a repository.
package gitrepo

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"
)

// Repo is a git repository, bare or not, at Dir.
type Repo struct {
	Dir string
	// InheritGitEnv keeps the environment variables that point git at a
	// repository and its objects (GIT_DIR, GIT_OBJECT_DIRECTORY, ...). Only
	// the repository a pre-receive hook runs in sets it, because git passes
	// the quarantined objects of the push that way. Every other Repo drops
	// them, so that a hook can also read a second repository.
	InheritGitEnv bool
}

// repoEnv lists the variables that make git use another repository, object
// store or index than the one at Dir.
var repoEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_QUARANTINE_PATH", "GIT_COMMON_DIR",
	"GIT_NAMESPACE", "GIT_SHALLOW_FILE", "GIT_GRAFT_FILE", "GIT_PREFIX",
}

// cleanEnv returns the process environment without the variables in repoEnv.
func cleanEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(repoEnv, name)
	})
}

// command prepares git with args in r.Dir, with extra environment variables.
func (r *Repo) command(env []string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	base := cleanEnv()
	if r.InheritGitEnv {
		base = os.Environ()
	}
	cmd.Env = append(base, env...)
	return cmd
}

// IsZero reports whether oid is the all-zero object ID git uses for a ref
// that does not exist yet or is being deleted.
func IsZero(oid string) bool {
	return oid != "" && strings.Trim(oid, "0") == ""
}

// InitBare creates a bare repository with main as its initial branch that
// accepts pushes over HTTP.
func InitBare(dir string) (*Repo, error) {
	cmd := exec.Command("git", "init", "--quiet", "--bare", "--initial-branch=main", dir)
	cmd.Env = cleanEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
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
// The blobs are read as stored, so .gitattributes (export-ignore, filters,
// eol conversion) cannot change or hide what is validated. Symlinks and
// submodules are rejected, because custos cannot validate what they point to.
func (r *Repo) TreeFS(rev string) (fs.FS, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	out, err := r.git("ls-tree", "-r", "-z", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	var entries []entry
	for _, rec := range splitNUL(out) {
		// <mode> SP <type> SP <oid> TAB <path>
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("unexpected git ls-tree output %q", rec)
		}
		if f[1] == "commit" {
			return nil, fmt.Errorf("%s: submodules are not supported", path)
		}
		entries = append(entries, entry{mode: f[0], oid: f[2], path: path})
	}
	return r.entriesFS(entries)
}

// IndexFS returns the files staged in the index of a work tree, with the
// same rules as TreeFS. Unmerged paths are an error.
func (r *Repo) IndexFS() (fs.FS, error) {
	out, err := r.git("ls-files", "-s", "-z")
	if err != nil {
		return nil, err
	}
	var entries []entry
	for _, rec := range splitNUL(out) {
		// <mode> SP <oid> SP <stage> TAB <path>
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("unexpected git ls-files output %q", rec)
		}
		if f[2] != "0" {
			return nil, fmt.Errorf("%s: unmerged; resolve the conflict and stage the file first", path)
		}
		entries = append(entries, entry{mode: f[0], oid: f[1], path: path})
	}
	return r.entriesFS(entries)
}

// entry is a file in a tree or in the index.
type entry struct {
	mode, oid, path string
}

func splitNUL(out []byte) []string {
	s := strings.TrimSuffix(string(out), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// entriesFS reads the blobs of entries into an in-memory file system.
func (r *Repo) entriesFS(entries []entry) (fstest.MapFS, error) {
	oids := make([]string, 0, len(entries))
	for _, e := range entries {
		switch e.mode {
		case "100644", "100755":
			oids = append(oids, e.oid)
		case "120000":
			return nil, fmt.Errorf("%s: only regular files are supported, not symlinks", e.path)
		case "160000":
			return nil, fmt.Errorf("%s: submodules are not supported", e.path)
		default:
			return nil, fmt.Errorf("%s: unsupported file mode %s", e.path, e.mode)
		}
	}
	files := fstest.MapFS{}
	if len(oids) == 0 {
		return files, nil
	}
	blobs, err := r.readBlobs(oids)
	if err != nil {
		return nil, err
	}
	for i, e := range entries {
		files[e.path] = &fstest.MapFile{Data: blobs[i], Mode: 0o644}
	}
	return files, nil
}

// readBlobs reads the contents of blobs with a single git cat-file process.
func (r *Repo) readBlobs(oids []string) ([][]byte, error) {
	cmd := r.command(nil, "cat-file", "--batch")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Write the requests concurrently: git answers while it reads, so a full
	// pipe in either direction would otherwise block both processes.
	go func() {
		w := bufio.NewWriter(stdin)
		for _, oid := range oids {
			w.WriteString(oid + "\n")
		}
		w.Flush()
		stdin.Close()
	}()
	blobs, readErr := parseBatch(bufio.NewReader(stdout), oids)
	io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git cat-file --batch: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return blobs, readErr
}

// parseBatch parses one "<oid> blob <size>\n<content>\n" reply per object.
func parseBatch(br *bufio.Reader, oids []string) ([][]byte, error) {
	blobs := make([][]byte, 0, len(oids))
	for _, oid := range oids {
		header, err := br.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file --batch: %w", err)
		}
		f := strings.Fields(header)
		if len(f) != 3 || f[0] != oid || f[1] != "blob" {
			return nil, fmt.Errorf("git cat-file --batch: unexpected reply %q for %s", strings.TrimSpace(header), oid)
		}
		size, err := strconv.Atoi(f[2])
		if err != nil || size < 0 {
			return nil, fmt.Errorf("git cat-file --batch: bad size in %q", strings.TrimSpace(header))
		}
		data := make([]byte, size+1)
		if _, err := io.ReadFull(br, data); err != nil {
			return nil, fmt.Errorf("git cat-file --batch: %w", err)
		}
		if data[size] != '\n' {
			return nil, fmt.Errorf("git cat-file --batch: missing newline after %s", oid)
		}
		blobs = append(blobs, data[:size])
	}
	return blobs, nil
}

func (r *Repo) git(args ...string) ([]byte, error) {
	return r.run(nil, nil, args...)
}

// run runs git with extra environment variables and stdin (nil for none).
func (r *Repo) run(env []string, stdin []byte, args ...string) ([]byte, error) {
	cmd := r.command(env, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// checkRev rejects revisions git would read as an option.
func checkRev(rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return fmt.Errorf("invalid revision %q", rev)
	}
	return nil
}
