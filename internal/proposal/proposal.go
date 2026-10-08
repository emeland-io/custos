// Package proposal keeps the output proposals of processor runs as
// branches of a workspace repository: custos/proposal/<task>/<digest-hex>
// for a run's output and custos/proposal/<task>/cascade for the removal of
// the tasks generated below a removed task. It writes, lists, accepts and
// rejects them through the store (ruling 2.3).
package proposal

import (
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
func Write(st *store.Store, wsID, taskID, digest string, ch *match.Changes, message string) (branch string, err error) {
	var files []gitrepo.Change
	if ch != nil {
		files = ch.Files
	}
	return writeBranch(st, wsID, taskID, Branch(taskID, digest), files, message)
}

// writeBranch deletes every open proposal of taskID and then writes files
// as a new branch from main, retrying when a push wins a compare-and-swap.
func writeBranch(st *store.Store, wsID, taskID, branch string, files []gitrepo.Change, message string) (string, error) {
	if !task.ValidID(taskID) {
		return "", fmt.Errorf("%w: task id %q is not a lowercase UUID v4", ErrInvalid, taskID)
	}
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return "", err
	}
	var commit string
	for range maxConflictRetries {
		if err = deleteAll(st, wsID, repo, taskID); err != nil {
			break
		}
		if len(files) == 0 {
			return "", nil
		}
		commit, err = st.UpdateWorkspace(wsID, headsPrefix+branch, gitrepo.Bot, message, func(fs.FS) ([]gitrepo.Change, error) {
			return files, nil
		})
		if !errors.Is(err, store.ErrConflict) {
			break
		}
	}
	if err != nil || commit == "" {
		return "", err
	}
	return branch, nil
}

// deleteAll deletes the open proposal branches of taskID under the
// workspace's lock.
func deleteAll(st *store.Store, wsID string, repo *gitrepo.Repo, taskID string) error {
	unlock := st.Lock(wsID)
	defer unlock()
	refs, err := repo.Refs(refPrefix + taskID + "/")
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

// Reject deletes the open proposal of taskID; store.ErrNotFound when none.
func Reject(st *store.Store, wsID, taskID string) error {
	repo, err := st.WorkspaceRepo(wsID)
	if err != nil {
		return err
	}
	if _, err := first(repo, taskID); err != nil {
		return err
	}
	return deleteAll(st, wsID, repo, taskID)
}

// conflict reports a ref that moved under us as store.ErrConflict.
func conflict(err error) error {
	if errors.Is(err, gitrepo.ErrRefMoved) {
		return fmt.Errorf("%w: %w", store.ErrConflict, err)
	}
	return err
}
