// Package proposal keeps the output proposals of processor runs as
// branches of a workspace repository: custos/proposal/<task>/<digest-hex>
// for a run's output and custos/proposal/<task>/cascade for the removal of
// the tasks generated below a removed task. It writes, lists, accepts and
// rejects them through the store (ruling 2.3).
package proposal

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
)

// BranchPrefix starts the short name of every output proposal branch.
const BranchPrefix = "custos/proposal/"

const (
	mainRef     = "refs/heads/main"
	headsPrefix = "refs/heads/"
	refPrefix   = headsPrefix + BranchPrefix
	cascadeName = "cascade"
)

// ErrInvalid reports a request that cannot be carried out as given, such as
// an unknown selector (HTTP 400).
var ErrInvalid = errors.New("invalid request")

var hexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// maxConflictRetries bounds the attempts of a write that loses its
// compare-and-swap to a push.
const maxConflictRetries = 3

// Branch returns the proposal branch of a run of the image with digest
// "sha256:<hex>" on the answer to task taskID.
func Branch(taskID, digest string) string {
	return BranchPrefix + taskID + "/" + strings.TrimPrefix(digest, "sha256:")
}

func cascadeBranch(taskID string) string { return BranchPrefix + taskID + "/" + cascadeName }

// Proposal is one open output proposal.
type Proposal struct {
	Branch  string // short name, e.g. custos/proposal/<task>/<hex>
	Commit  string
	Task    string // answered task id (or removed task id for a cascade)
	Digest  string // "sha256:<hex>", "" for a cascade
	Cascade bool
	Items   []match.Item // recomputed from the branch against current main; Unchanged included
}

// parseBranch splits a short branch name below BranchPrefix. ok is false
// for names custos does not write (pushed by hand), which are ignored.
func parseBranch(branch string) (p Proposal, ok bool) {
	rest, ok := strings.CutPrefix(branch, BranchPrefix)
	if !ok {
		return Proposal{}, false
	}
	id, last, ok := strings.Cut(rest, "/")
	if !ok || !task.ValidID(id) {
		return Proposal{}, false
	}
	switch {
	case last == cascadeName:
		return Proposal{Branch: branch, Task: id, Cascade: true}, true
	case hexRE.MatchString(last):
		return Proposal{Branch: branch, Task: id, Digest: "sha256:" + last}, true
	}
	return Proposal{}, false
}

// open returns the open proposals of the workspace (of one task when taskID
// is not ""), sorted by branch, with Commit set and Items empty.
func open(repo *gitrepo.Repo, taskID string) ([]Proposal, error) {
	prefix := refPrefix
	if taskID != "" {
		prefix += taskID + "/"
	}
	refs, err := repo.Refs(prefix)
	if err != nil {
		return nil, err
	}
	var out []Proposal
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		p, ok := parseBranch(strings.TrimPrefix(ref, headsPrefix))
		if !ok || (taskID != "" && p.Task != taskID) {
			continue
		}
		p.Commit = refs[ref]
		out = append(out, p)
	}
	return out, nil
}

// List returns the open output proposals of workspace wsID, sorted by
// branch, with their items recomputed against the current main.
func List(st *store.Store, wsID string) ([]Proposal, error) {
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return nil, err
	}
	ps, err := open(repo, "")
	if err != nil {
		return nil, err
	}
	main, err := mainSide(repo)
	if err != nil {
		return nil, err
	}
	out := []Proposal{}
	for _, p := range ps {
		if err := fill(repo, main, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", p.Branch, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// Get returns the open proposal of taskID (store.ErrNotFound when none). If
// a hand push left several, the first by branch name is used.
func Get(st *store.Store, wsID, taskID string) (*Proposal, error) {
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return nil, err
	}
	p, err := first(repo, taskID)
	if err != nil {
		return nil, err
	}
	main, err := mainSide(repo)
	if err != nil {
		return nil, err
	}
	if err := fill(repo, main, p); err != nil {
		return nil, err
	}
	return p, nil
}

func first(repo *gitrepo.Repo, taskID string) (*Proposal, error) {
	if !task.ValidID(taskID) {
		return nil, fmt.Errorf("%w: no open output proposal for task %q", store.ErrNotFound, taskID)
	}
	ps, err := open(repo, taskID)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, fmt.Errorf("%w: no open output proposal for task %s", store.ErrNotFound, taskID)
	}
	return &ps[0], nil
}

// mainSide loads the workspace's main.
func mainSide(repo *gitrepo.Repo) (side, error) {
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil {
		return side{}, err
	}
	if !ok {
		return side{}, fmt.Errorf("%w: the workspace has no main branch yet", store.ErrNotFound)
	}
	fsys, err := repo.TreeFS(main)
	if err != nil {
		return side{}, err
	}
	return loadSide(fsys), nil
}

// fill sets p.Items from the branch tip against main.
func fill(repo *gitrepo.Repo, main side, p *Proposal) error {
	fsys, err := repo.TreeFS(p.Commit)
	if err != nil {
		return err
	}
	es, err := diff(main, loadSide(fsys), p.Task, p.Cascade)
	if err != nil {
		return err
	}
	p.Items = []match.Item{}
	for _, e := range es {
		p.Items = append(p.Items, e.item)
	}
	return nil
}

// Write puts ch.Files on branch Branch(taskID, digest) as one commit by Bot
// based on the current main, replacing the branch if it exists, and deletes
// other open proposals of taskID. No files → deletes open proposals of
// taskID and returns "". Files that leave main as it is (because main moved
// on since the run) do not count, so a run whose output main already holds
// opens no proposal either.
//
// A task that is no longer live — neither a task of the catalog at main's
// pin nor a generated task on main, as after its removal was accepted — gets
// no proposal, and its branches (such as the cascade the removal opened) are
// left alone: Write returns store.ErrNotFound. A run that was in flight when
// the removal landed must not drop or replace that cascade.
//
// The liveness check and the whole delete-then-write sequence of one attempt
// run under a single held workspace lock, so two concurrent writes for the
// same task — even naming different digests, which would not otherwise
// collide on a compare-and-swap, because they write different branch names —
// can never both leave an open proposal, and no Accept can remove the task
// between the check and the write. A write that loses a compare-and-swap to
// a push is retried.
func Write(st *store.Store, wsID, taskID, digest string, ch *match.Changes, message string) (branch string, err error) {
	if !task.ValidID(taskID) {
		return "", fmt.Errorf("%w: task id %q is not a lowercase UUID v4", ErrInvalid, taskID)
	}
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return "", err
	}
	var files []gitrepo.Change
	if ch != nil {
		files = ch.Files
	}
	branch = Branch(taskID, digest)
	var commit string
	for range maxConflictRetries {
		commit, err = writeLocked(st, wsID, repo, taskID, branch, files, message)
		if !errors.Is(err, store.ErrConflict) {
			break
		}
	}
	if err != nil || commit == "" {
		return "", err
	}
	return branch, nil
}

// writeLocked does one attempt of Write under one held st.Lock(wsID) span:
// it reads main, requires taskID to be live there, and replaces the task's
// proposals (replaceProposals).
func writeLocked(st *store.Store, wsID string, repo *gitrepo.Repo, taskID, branch string, files []gitrepo.Change, message string) (string, error) {
	unlock := st.Lock(wsID)
	defer unlock()
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: the workspace has no main branch yet", store.ErrNotFound)
	}
	w, c, err := st.Load(wsID, main)
	if err != nil {
		return "", err
	}
	if !c.Tasks.Has(taskID) && !w.Graph.Has(taskID) {
		return "", fmt.Errorf("%w: task %s is neither a catalog task nor a generated task on main any more; its output is not proposed", store.ErrNotFound, taskID)
	}
	tree, err := repo.TreeFS(main)
	if err != nil {
		return "", err
	}
	return replaceProposals(repo, taskID, branch, main, tree, files, message)
}

// replaceProposals deletes every open proposal of taskID (every branch below
// custos/proposal/<taskID>/) and then, if files changes anything in tree,
// writes them as a new commit by Bot on base (whose tree is tree) and
// creates branch there. It returns the new commit, or "" when it wrote
// none.
//
// It does not take the workspace's lock: the caller must hold st.Lock for
// the whole call — writeLocked, or Accept's then callback, which runs inside
// store.UpdateWorkspaceAndThen's lock span — so that nothing else that takes
// the lock can interleave between the deletion and the write. Pushes still
// bypass the lock (ruling 2.3), so ref updates are compare-and-swaps,
// reported as store.ErrConflict.
func replaceProposals(repo *gitrepo.Repo, taskID, branch, base string, tree fs.FS, files []gitrepo.Change, message string) (string, error) {
	if err := deleteRefs(repo, refPrefix+taskID+"/"); err != nil {
		return "", err
	}
	changes := effectiveChanges(tree, files)
	if len(changes) == 0 {
		return "", nil
	}
	req := gitrepo.CommitRequest{Changes: changes, Author: gitrepo.Bot, Message: message}
	if base != "" {
		req.Base, req.Parents = base, []string{base}
	}
	commit, err := repo.WriteCommit(req)
	if err != nil {
		return "", err
	}
	if err := repo.UpdateRef(headsPrefix+branch, commit, ""); err != nil {
		return "", conflict(err)
	}
	return commit, nil
}

// deleteRefs deletes every ref below prefix. Callers that must not let
// another writer interleave between this and a following write hold
// st.Lock(wsID) across both (replaceProposals); Reject, which only
// deletes, takes the lock itself.
func deleteRefs(repo *gitrepo.Repo, prefix string) error {
	refs, err := repo.Refs(prefix)
	if err != nil {
		return err
	}
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		if err := repo.DeleteRef(ref, refs[ref]); err != nil {
			return conflict(err)
		}
	}
	return nil
}

// effectiveChanges drops changes that would leave tree exactly as it already
// is, mirroring store.UpdateWorkspace's own no-op filtering. replaceProposals
// writes commits directly through gitrepo rather than through
// store.UpdateWorkspace (to keep the delete-then-write sequence under one
// lock instead of two), so it must replicate this filtering itself; without
// it, a run whose output main already holds would open a vacuous proposal
// instead of none (Review Focus item 3).
func effectiveChanges(tree fs.FS, changes []gitrepo.Change) []gitrepo.Change {
	changes = lastPerPath(changes)
	var out []gitrepo.Change
	for _, c := range changes {
		if c.Delete {
			if info, err := fs.Stat(tree, c.Path); err == nil && info.IsDir() {
				continue // nothing to delete: the path is a directory, not a file
			}
		}
		cur, err := fs.ReadFile(tree, c.Path)
		missing := errors.Is(err, fs.ErrNotExist)
		if c.Delete && missing || !c.Delete && err == nil && bytes.Equal(cur, c.Data) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// lastPerPath keeps only the last change to each path, in the order its
// first occurrence appeared.
func lastPerPath(changes []gitrepo.Change) []gitrepo.Change {
	latest := make(map[string]gitrepo.Change, len(changes))
	for _, c := range changes {
		latest[c.Path] = c
	}
	seen := make(map[string]bool, len(changes))
	var out []gitrepo.Change
	for _, c := range changes {
		if seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		out = append(out, latest[c.Path])
	}
	return out
}

// Reject deletes the open proposal of taskID; store.ErrNotFound when none,
// including a proposal an Accept running concurrently already closed: the
// existence check and the delete run under one held st.Lock(wsID) span
// (rather than checking before taking the lock, as this used to), so Reject
// and Accept of the same proposal — which also now does its own check and
// delete under this same lock, around the main-branch commit (see
// accept.go's Accept) — can never both report success for one proposal.
func Reject(st *store.Store, wsID, taskID string) error {
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return err
	}
	unlock := st.Lock(wsID)
	defer unlock()
	if _, err := first(repo, taskID); err != nil {
		return err
	}
	return deleteRefs(repo, refPrefix+taskID+"/")
}

// conflict reports a ref that moved under us as store.ErrConflict.
func conflict(err error) error {
	if errors.Is(err, gitrepo.ErrRefMoved) {
		return fmt.Errorf("%w: %w", store.ErrConflict, err)
	}
	return err
}
