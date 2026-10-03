package gitrepo

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
)

// commitSuffix peels a ref or object id to the commit it points at; see
// ResolveRef.
const commitSuffix = "^{commit}"

// ResolveRef returns the object id ref points to; ok is false when there is
// no such ref. ref may be any revision git understands. A full object id
// resolves to itself even when the object does not exist; append "^{commit}"
// to check that a commit exists.
//
// A ref already written as refs/… (optionally followed by "^{commit}") is
// resolved as that exact ref, never through rev-parse's DWIM lookup: given a
// missing refs/heads/main, DWIM would also try refs/refs/heads/main,
// refs/tags/refs/heads/main and refs/heads/refs/heads/main, letting an
// unrelated ref such as a tag named refs/heads/main stand in for it.
func (r *Repo) ResolveRef(ref string) (oid string, ok bool, err error) {
	if err := checkRev(ref); err != nil {
		return "", false, err
	}
	if name, peel := strings.CutSuffix(ref, commitSuffix); strings.HasPrefix(name, "refs/") {
		return r.resolveExactRef(name, peel)
	}
	return r.revParseVerify(ref)
}

// resolveExactRef resolves name as the literal ref name, bypassing rev-parse's
// DWIM lookup, optionally peeling the result to the commit it points at.
func (r *Repo) resolveExactRef(name string, peelToCommit bool) (string, bool, error) {
	out, err := r.git("show-ref", "--verify", "--", name)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	oid, _, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	if !peelToCommit {
		return oid, true, nil
	}
	return r.revParseVerify(oid + commitSuffix)
}

// revParseVerify resolves rev with rev-parse's usual DWIM lookup; used for
// object ids and "<oid>^{commit}" expressions, which are unambiguous.
func (r *Repo) revParseVerify(rev string) (string, bool, error) {
	out, err := r.git("rev-parse", "--verify", "--quiet", "--end-of-options", rev)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

// ReadFile returns the content of the file at path in revision rev; ok is
// false when rev has no file at path. Like TreeFS it reads the blob as
// stored and rejects symlinks and submodules.
func (r *Repo) ReadFile(rev, path string) (data []byte, ok bool, err error) {
	if err := checkRev(rev); err != nil {
		return nil, false, err
	}
	if err := checkPath(path); err != nil {
		return nil, false, err
	}
	out, err := r.git("ls-tree", "-z", "--full-tree", rev, "--", path)
	if err != nil {
		return nil, false, err
	}
	for _, rec := range splitNUL(out) {
		meta, p, _ := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if p != path || len(f) != 3 || f[1] == "tree" {
			continue
		}
		files, err := r.entriesFS([]entry{{mode: f[0], oid: f[2], path: p}})
		if err != nil {
			return nil, false, err
		}
		return files[p].Data, true, nil
	}
	return nil, false, nil
}

// Refs returns the refs whose full name starts with prefix ("" for all),
// mapped to the object ids they point to.
func (r *Repo) Refs(prefix string) (map[string]string, error) {
	out, err := r.git("for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		oid, name, ok := strings.Cut(line, " ")
		if ok && strings.HasPrefix(name, prefix) {
			refs[name] = oid
		}
	}
	return refs, nil
}

// checkPath accepts slash-separated paths relative to the repository root
// that git can store: no empty, "." or ".." elements, no .git directory.
func checkPath(path string) error {
	if !fs.ValidPath(path) || path == "." || strings.ContainsAny(path, "\\\x00") {
		return fmt.Errorf("invalid path %q", path)
	}
	for _, elem := range strings.Split(path, "/") {
		if strings.EqualFold(elem, ".git") {
			return fmt.Errorf("invalid path %q: .git is reserved", path)
		}
	}
	return nil
}
