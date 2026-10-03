package gitrepo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrRefMoved reports that UpdateRef or DeleteRef found the ref at another
// value than the expected one, because someone else moved it meanwhile.
var ErrRefMoved = errors.New("ref moved")

var oidRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// Change sets or deletes one file in a commit.
type Change struct {
	Path   string // slash-separated, relative to the repository root
	Data   []byte
	Delete bool
}

// CommitRequest describes a commit for WriteCommit.
type CommitRequest struct {
	Base    string   // commit whose tree is the starting point; "" = empty tree
	Parents []string // usually []string{Base}; nil for a root commit
	Changes []Change
	Author  Signature
	Message string
}

// WriteCommit writes the tree of Base with Changes applied and a commit of
// it, and returns the commit id. It moves no ref. It builds the tree in a
// temporary index file, never in the repository's own index or one named by
// an inherited GIT_INDEX_FILE. Files are written as regular, non-executable
// files; a file replaces a directory of the same name and vice versa.
func (r *Repo) WriteCommit(req CommitRequest) (string, error) {
	tree, err := r.writeTree(req.Base, req.Changes)
	if err != nil {
		return "", err
	}
	return r.CommitTree(tree, req.Parents, req.Author, req.Message)
}

func (r *Repo) writeTree(base string, changes []Change) (string, error) {
	tmp, err := os.MkdirTemp("", "custos-index-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	zero := ""
	if base == "" {
		if _, err := r.run(env, nil, "read-tree", "--empty"); err != nil {
			return "", err
		}
	} else {
		oid, ok, err := r.ResolveRef(base + "^{commit}")
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("base %s is not a commit", base)
		}
		if _, err := r.run(env, nil, "read-tree", oid); err != nil {
			return "", err
		}
		zero = strings.Repeat("0", len(oid))
	}
	var info bytes.Buffer
	for _, c := range changes {
		if err := checkPath(c.Path); err != nil {
			return "", err
		}
		if c.Delete {
			if zero != "" { // nothing to delete in an empty tree
				fmt.Fprintf(&info, "0 %s\t%s\x00", zero, c.Path)
			}
			continue
		}
		out, err := r.run(nil, c.Data, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&info, "100644 %s\t%s\x00", strings.TrimSpace(string(out)), c.Path)
	}
	if info.Len() > 0 {
		if _, err := r.run(env, info.Bytes(), "update-index", "-z", "--index-info"); err != nil {
			return "", err
		}
	}
	out, err := r.run(env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CommitTree writes a commit of tree with the given parents, authored by
// author and committed by Bot, and returns its id. It moves no ref and
// never signs, whatever the git configuration says.
func (r *Repo) CommitTree(tree string, parents []string, author Signature, message string) (string, error) {
	if err := author.check(); err != nil {
		return "", err
	}
	if err := checkRev(tree); err != nil {
		return "", err
	}
	args := []string{"commit-tree", "--no-gpg-sign"}
	for _, p := range parents {
		if err := checkRev(p); err != nil {
			return "", err
		}
		args = append(args, "-p", p)
	}
	args = append(args, "-F", "-", tree)
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + author.Name, "GIT_AUTHOR_EMAIL=" + author.Email,
		"GIT_COMMITTER_NAME=" + Bot.Name, "GIT_COMMITTER_EMAIL=" + Bot.Email,
	}
	out, err := r.run(env, []byte(message), args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// UpdateRef points ref at newOID if it currently points at oldOID; oldOID
// "" means the ref must not exist yet. It returns an error wrapping
// ErrRefMoved when the ref has another value.
func (r *Repo) UpdateRef(ref, newOID, oldOID string) error {
	if err := checkRefName(ref); err != nil {
		return err
	}
	if !oidRE.MatchString(newOID) || (oldOID != "" && !oidRE.MatchString(oldOID)) {
		return fmt.Errorf("update %s: object ids must be full hashes, got %q and %q", ref, newOID, oldOID)
	}
	expect := oldOID
	if expect == "" {
		expect = strings.Repeat("0", len(newOID))
	}
	if _, err := r.git("update-ref", "--no-deref", ref, newOID, expect); err != nil {
		return r.moved(ref, oldOID, err)
	}
	return nil
}

// DeleteRef deletes ref if it currently points at oldOID. It returns an
// error wrapping ErrRefMoved when the ref has another value or is gone.
func (r *Repo) DeleteRef(ref, oldOID string) error {
	if err := checkRefName(ref); err != nil {
		return err
	}
	if !oidRE.MatchString(oldOID) {
		return fmt.Errorf("delete %s: expected value must be a full hash, got %q", ref, oldOID)
	}
	if _, err := r.git("update-ref", "--no-deref", "-d", ref, oldOID); err != nil {
		return r.moved(ref, oldOID, err)
	}
	return nil
}

// moved turns the failure of a compare-and-swap into ErrRefMoved when the ref
// is not at the expected value, without parsing git's localized messages.
func (r *Repo) moved(ref, want string, err error) error {
	cur, ok, rerr := r.ResolveRef(ref)
	if rerr != nil || cur == want {
		return err
	}
	if !ok {
		cur = "nothing"
	}
	if want == "" {
		want = "nothing"
	}
	return fmt.Errorf("%w: %s points to %s, expected %s", ErrRefMoved, ref, cur, want)
}

func checkRefName(ref string) error {
	if !strings.HasPrefix(ref, "refs/") {
		return fmt.Errorf("invalid ref %q: must start with refs/", ref)
	}
	return nil
}
