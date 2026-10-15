package store

import (
	"io/fs"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/rules"
)

// catalogLockKey is the Store.Lock key that serialises every write to the
// catalog repository this package or another package (internal/catalogdraft)
// makes through the store: see the doc comment on Lock.
const catalogLockKey = "catalog"

// UpdateCatalog applies edit to the tree at ref ("refs/heads/main" or
// another branch; the ref may not exist yet for a branch, whose base is
// then main) of the catalog repository, under the catalog's own lock
// (Store.Lock("catalog")). No changes → no commit, returns the current oid
// ("" for a branch that does not exist). A move of main is validated with
// rules.CheckCatalogUpdate, exactly what the pre-receive hook runs for a
// push (history, catalog.Check, catalog.CheckImmutable); a problem there
// comes back as *RejectedError. Any other ref (a catalogdraft branch) is
// not validated here at all: a draft may be transiently invalid
// (architecture note, ruling 4.13). The ref is moved with the expected old
// value (ErrConflict when it moved).
//
// allowImmutableChange, found necessary while applying this plan to a
// scratch copy (cross-plan review, Part 2): it names paths whose
// immutability ("was changed") problem is not grounds to reject this
// particular call. It exists only for catalogdraft.Publish's resolved-
// merge-conflict case (Task 5) — a git push (which never passes it) is
// checked exactly as before, with no exemption ever possible. Why this is
// safe: an ordinary immutable-content rewrite is a *silent* one (nobody
// asked about it, so rejecting it outright is correct); a path named here
// arrives only because gitrepo.MergeTree already reported a genuine
// conflict at it and a human explicitly chose a side through Publish's
// res parameter — the catalog's one rule a workspace merge never had to
// consider, now given the same "an explicit resolution overrides what
// main had" treatment a workspace merge's conflict resolution already
// gets. Every other immutability problem, and every other rule
// CheckCatalogUpdate checks, still rejects exactly as before. (Without
// this, TestPublishConflict's own "resolved with theirs" step below
// cannot succeed: once the first draft's new version file is on main,
// resolving the second draft's conflict at that exact path with "theirs"
// necessarily asks to replace that path's content, which CheckImmutable
// — correctly, for every *other* case — would otherwise reject.)
func (s *Store) UpdateCatalog(ref string, author gitrepo.Signature, message string,
	edit func(tree fs.FS) ([]gitrepo.Change, error), allowImmutableChange ...string) (string, error) {
	repo := s.CatalogRepo()
	if err := checkBranch(ref); err != nil {
		return "", err
	}
	unlock := s.Lock(catalogLockKey)
	defer unlock()

	old, _, err := repo.ResolveRef(ref)
	if err != nil {
		return "", err
	}
	base := old
	if base == "" {
		if base, _, err = repo.ResolveRef(mainRef); err != nil {
			return "", err
		}
	}
	var tree fs.FS = fstest.MapFS{}
	if base != "" {
		if tree, err = repo.TreeFS(base); err != nil {
			return "", err
		}
	}
	changes, err := edit(tree)
	if err != nil {
		return "", err
	}
	changes = effective(tree, changes)
	if len(changes) == 0 {
		return old, nil
	}
	req := gitrepo.CommitRequest{Base: base, Changes: changes, Author: author, Message: message}
	if base != "" {
		req.Parents = []string{base}
	}
	commit, err := repo.WriteCommit(req)
	if err != nil {
		return "", err
	}
	if ref == mainRef {
		if err := s.validateCatalogMain(repo, old, commit, allowImmutableChange); err != nil {
			return "", err
		}
	}
	if err := repo.UpdateRef(ref, commit, old); err != nil {
		return "", conflict(err)
	}
	return commit, nil
}

// validateCatalogMain applies the push rules to moving the catalog's main,
// except that a "was changed" immutability problem at a path listed in
// allowed is dropped rather than turned into a rejection (see
// UpdateCatalog's allowImmutableChange; empty for every push-equivalent
// caller, so this is a no-op there).
func (s *Store) validateCatalogMain(repo *gitrepo.Repo, oldOID, newOID string, allowed []string) error {
	ps, err := rules.CheckCatalogUpdate(repo, oldOID, newOID)
	if err != nil {
		return err
	}
	if len(allowed) > 0 {
		ps = slices.DeleteFunc(ps, func(p problem.Problem) bool {
			// Only a replaced path's problem is ever dropped, never a
			// removed one: resolving a conflict with a side that deletes
			// an already-published task version is not a case this
			// exemption is meant to cover.
			return p.Rule == problem.RuleImmutable && strings.Contains(p.Message, "was changed") && slices.Contains(allowed, p.Path)
		})
	}
	if len(ps) > 0 {
		return &RejectedError{Problems: ps}
	}
	return nil
}
