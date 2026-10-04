package distribute

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

// rejectedRefPrefix + catalog commit marks the proposal for that commit as
// rejected, so that reconcile does not open it again. It points at the last
// commit of the rejected branch. It is not a branch, so clones do not fetch
// it. Reconcile deletes it once the catalog's main moves on.
const rejectedRefPrefix = "refs/custos/rejected-pin/"

var commitRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// PinProposal is one open branch custos/pin/<catalog commit>.
type PinProposal struct {
	Branch  string `json:"branch"`  // custos/pin/<catalog commit>
	Commit  string `json:"commit"`  // last commit of the branch
	From    string `json:"from"`    // pin on the workspace's main
	To      string `json:"to"`      // pin on the branch
	Changes Diff   `json:"changes"` // CatalogDiff(From, To)
}

// Proposals lists the open pin proposals of a workspace, sorted by branch.
func Proposals(st *store.Store, id string) ([]PinProposal, error) {
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return nil, err
	}
	out := []PinProposal{}
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil || !ok {
		return out, err
	}
	cfg, err := readConfig(repo, main)
	if err != nil {
		return nil, err
	}
	refs, err := repo.Refs(pinRefPrefix)
	if err != nil {
		return nil, err
	}
	for _, ref := range slices.Sorted(maps.Keys(refs)) {
		branch := strings.TrimPrefix(ref, "refs/heads/")
		pc, err := readConfig(repo, refs[ref])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", branch, err)
		}
		d, err := CatalogDiff(st.CatalogRepo(), cfg.Catalog.Commit, pc.Catalog.Commit)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", branch, err)
		}
		out = append(out, PinProposal{Branch: branch, Commit: refs[ref], From: cfg.Catalog.Commit, To: pc.Catalog.Commit, Changes: d})
	}
	return out, nil
}

// proposalRef returns the ref of a pin proposal branch and the catalog
// commit in its name. Other names are reported as not found.
func proposalRef(branch string) (ref, commit string, err error) {
	commit, ok := strings.CutPrefix(branch, pinBranch)
	if !ok || !commitRE.MatchString(commit) {
		return "", "", fmt.Errorf("%q is not a pin proposal (custos/pin/<catalog commit>): %w", branch, store.ErrNotFound)
	}
	return pinRefPrefix + commit, commit, nil
}

// Accept merges a pin proposal into the workspace's main: a fast-forward
// when main has not moved since the proposal was opened, otherwise a merge
// commit by author whose tree is main's tree with the proposal's pin. The
// new main is validated like any other update. The branch is deleted.
func Accept(st *store.Store, id, branch string, author gitrepo.Signature) error {
	mu.Lock()
	defer mu.Unlock()
	ref, _, err := proposalRef(branch)
	if err != nil {
		return err
	}
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return err
	}
	tip, ok, err := repo.ResolveRef(ref)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("pin proposal %s: %w", branch, store.ErrNotFound)
	}
	main, ok, err := repo.ResolveRef(mainRef)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("workspace %s has no main branch: %w", id, store.ErrNotFound)
	}
	next := tip
	ff, err := repo.IsAncestor(main, tip)
	if err != nil {
		return err
	}
	if !ff {
		if next, err = mergeProposal(repo, main, tip, branch, author); err != nil {
			return err
		}
	}
	if err := st.SetRef(id, mainRef, next, main); err != nil {
		return err
	}
	unlock := st.Lock(id)
	defer unlock()
	return conflict(repo.DeleteRef(ref, tip))
}

// mergeProposal writes the merge of proposal tip into main. Proposal
// branches are written by custos and change nothing but the pin, so main's
// tree with the proposal's pin is what a three-way merge would produce.
func mergeProposal(repo *gitrepo.Repo, main, tip, branch string, author gitrepo.Signature) (string, error) {
	pc, err := readConfig(repo, tip)
	if err != nil {
		return "", err
	}
	data, ok, err := repo.ReadFile(main, configPath)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%s is missing on main", configPath)
	}
	out, changed, err := setConfig(data, func(c *workspace.Config) { c.Catalog.Commit = pc.Catalog.Commit })
	if err != nil {
		return "", err
	}
	var changes []gitrepo.Change
	if changed {
		changes = []gitrepo.Change{{Path: configPath, Data: out}}
	}
	return repo.WriteCommit(gitrepo.CommitRequest{
		Base:    main,
		Parents: []string{main, tip},
		Changes: changes,
		Author:  author,
		Message: "Accept pin proposal " + branch,
	})
}

// Reject deletes a pin proposal and marks its catalog commit as rejected
// for this workspace, so that reconcile does not open it again.
func Reject(st *store.Store, id, branch string) error {
	mu.Lock()
	defer mu.Unlock()
	ref, commit, err := proposalRef(branch)
	if err != nil {
		return err
	}
	repo, err := st.WorkspaceRepo(id)
	if err != nil {
		return err
	}
	unlock := st.Lock(id)
	defer unlock()
	tip, ok, err := repo.ResolveRef(ref)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("pin proposal %s: %w", branch, store.ErrNotFound)
	}
	mark := rejectedRefPrefix + commit
	_, marked, err := repo.ResolveRef(mark)
	if err != nil {
		return err
	}
	if !marked {
		if err := repo.UpdateRef(mark, tip, ""); err != nil {
			return conflict(err)
		}
	}
	return conflict(repo.DeleteRef(ref, tip))
}
