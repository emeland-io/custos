package gitrepo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var mergeOIDRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// ConflictedFile is a path that git could not merge. Base, Ours and Theirs
// hold the file in the merge base and on each side; nil means the file does
// not exist there, for example because that side deleted it.
type ConflictedFile struct {
	Path               string
	Base, Ours, Theirs []byte
}

// MergeTreeResult is the outcome of MergeTree.
type MergeTreeResult struct {
	Tree      string           // merged tree; conflicted files hold conflict markers
	Conflicts []ConflictedFile // sorted by path; empty for a clean merge
}

// MergeTree merges commit theirs into commit ours without a work tree
// (git merge-tree --write-tree, git 2.38 or later) and writes the merged
// tree into the repository. Commits without common history are an error.
// Conflicts on symlinks or submodules are an error, because custos only
// stores regular files.
func (r *Repo) MergeTree(ours, theirs string) (*MergeTreeResult, error) {
	if err := checkMergeRevs(ours, theirs); err != nil {
		return nil, err
	}
	out, code, err := r.mergeGit(nil, nil, "merge-tree", "--write-tree", "-z", "--no-messages", ours, theirs)
	if err != nil && code != 1 {
		return nil, err
	}
	recs := splitNUL(out)
	if len(recs) == 0 || !mergeOIDRE.MatchString(recs[0]) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected git merge-tree output %q", out)
	}
	res := &MergeTreeResult{Tree: recs[0]}
	if code == 0 {
		return res, nil
	}
	// One record per conflicted stage: <mode> SP <oid> SP <stage> TAB <path>.
	stages := map[string]*[4]string{}
	var paths []string
	for _, rec := range recs[1:] {
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("unexpected git merge-tree output %q", rec)
		}
		if f[0] != "100644" && f[0] != "100755" {
			return nil, fmt.Errorf("%s: only regular files can be merged, not mode %s", path, f[0])
		}
		stage, serr := strconv.Atoi(f[2])
		if serr != nil || stage < 1 || stage > 3 {
			return nil, fmt.Errorf("unexpected stage in git merge-tree output %q", rec)
		}
		s := stages[path]
		if s == nil {
			s = new([4]string)
			stages[path] = s
			paths = append(paths, path)
		}
		s[stage] = f[1]
	}
	if len(paths) == 0 {
		return nil, errors.New("git merge-tree reported a conflict but no conflicted file")
	}
	slices.Sort(paths)
	var oids []string
	for _, p := range paths {
		for _, oid := range stages[p][1:] {
			if oid != "" {
				oids = append(oids, oid)
			}
		}
	}
	blobs, err := r.readBlobs(oids)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		c := ConflictedFile{Path: p}
		for stage, oid := range stages[p] {
			if oid == "" {
				continue
			}
			data := blobs[0]
			blobs = blobs[1:]
			switch stage {
			case 1:
				c.Base = data
			case 2:
				c.Ours = data
			case 3:
				c.Theirs = data
			}
		}
		res.Conflicts = append(res.Conflicts, c)
	}
	return res, nil
}

// MergeBase returns the best common ancestor of commits a and b. ok is
// false when they share no history.
func (r *Repo) MergeBase(a, b string) (oid string, ok bool, err error) {
	if err := checkMergeRevs(a, b); err != nil {
		return "", false, err
	}
	out, code, err := r.mergeGit(nil, nil, "merge-base", a, b)
	if code == 1 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

// HasCommit reports whether oid names a commit in the repository.
func (r *Repo) HasCommit(oid string) (bool, error) {
	if err := checkMergeRevs(oid); err != nil {
		return false, err
	}
	_, code, err := r.mergeGit(nil, nil, "rev-parse", "--verify", "--quiet", oid+"^{commit}")
	if code == 1 {
		return false, nil
	}
	return err == nil, err
}

// Fetch copies srcRef of the local repository src, with its history, into r
// as dstRef (replacing dstRef if it exists) and returns the copied commit.
// No other ref of r changes. Both names must be full ref names.
func (r *Repo) Fetch(src *Repo, srcRef, dstRef string) (string, error) {
	for _, ref := range []string{srcRef, dstRef} {
		if !strings.HasPrefix(ref, "refs/") || strings.ContainsAny(ref, ": \t\n") {
			return "", fmt.Errorf("%q is not a full ref name", ref)
		}
	}
	from, err := filepath.Abs(src.Dir)
	if err != nil {
		return "", err
	}
	if _, _, err := r.mergeGit(nil, nil, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", from, "+"+srcRef+":"+dstRef); err != nil {
		return "", err
	}
	out, _, err := r.mergeGit(nil, nil, "rev-parse", "--verify", dstRef)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// EditTree applies changes to tree and writes the resulting tree. tree must
// be a full object id. It uses a temporary index, never the repository's
// index or an inherited GIT_INDEX_FILE. Files are written with mode 100644.
func (r *Repo) EditTree(tree string, changes []Change) (string, error) {
	if !mergeOIDRE.MatchString(tree) {
		return "", fmt.Errorf("EditTree needs a full tree id, not %q", tree)
	}
	tmp, err := os.MkdirTemp("", "custos-tree-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	if _, _, err := r.mergeGit(env, nil, "read-tree", tree); err != nil {
		return "", err
	}
	zero := strings.Repeat("0", len(tree))
	var info bytes.Buffer
	for _, c := range changes {
		if c.Path == "" || strings.ContainsRune(c.Path, 0) {
			return "", fmt.Errorf("invalid path %q", c.Path)
		}
		if c.Delete {
			fmt.Fprintf(&info, "0 %s\t%s\x00", zero, c.Path)
			continue
		}
		out, _, err := r.mergeGit(nil, c.Data, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&info, "100644 %s\t%s\x00", strings.TrimSpace(string(out)), c.Path)
	}
	if info.Len() > 0 {
		if _, _, err := r.mergeGit(env, info.Bytes(), "update-index", "-z", "--index-info"); err != nil {
			return "", err
		}
	}
	out, _, err := r.mergeGit(env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// checkMergeRevs rejects revisions git would read as options.
func checkMergeRevs(revs ...string) error {
	for _, rev := range revs {
		if rev == "" || strings.HasPrefix(rev, "-") {
			return fmt.Errorf("invalid revision %q", rev)
		}
	}
	return nil
}

// mergeGit runs git in r with extra environment variables and standard
// input. It returns standard output and the exit code (0 on success, -1 when
// git did not run); the error is non-nil for any non-zero exit. It uses
// r.command (plan 2a), so variables such as GIT_DIR, GIT_OBJECT_DIRECTORY
// and GIT_INDEX_FILE are dropped unless r.InheritGitEnv is set.
func (r *Repo) mergeGit(env []string, stdin []byte, args ...string) ([]byte, int, error) {
	cmd := r.command(env, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), 0, nil
	}
	code := -1
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	}
	return stdout.Bytes(), code, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
}
