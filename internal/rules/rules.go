// Package rules decides whether the main branch of a catalog or workspace
// repository may move to a new commit. The pre-receive hook applies them to
// pushes, the store to the commits it writes itself.
package rules

import (
	"slices"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/workspace"
)

const mainRef = "refs/heads/main"

// CheckCatalogUpdate validates moving the catalog's main from oldOID ("" or
// all zeros = no previous main) to newOID: history (no delete, no rewrite),
// catalog.Check and catalog.CheckImmutable. newOID all zeros = delete.
func CheckCatalogUpdate(cat *gitrepo.Repo, oldOID, newOID string) ([]problem.Problem, error) {
	if ps, err := checkHistory(cat, oldOID, newOID); err != nil || len(ps) > 0 {
		return ps, err
	}
	newFS, err := cat.TreeFS(newOID)
	if err != nil {
		return nil, err
	}
	ps := catalog.Check(newFS)
	if !none(oldOID) {
		oldFS, err := cat.TreeFS(oldOID)
		if err != nil {
			return nil, err
		}
		ps = append(ps, catalog.CheckImmutable(oldFS, newFS)...)
	}
	problem.Sort(ps)
	return ps, nil
}

// CheckWorkspaceUpdate validates moving a workspace's main: history,
// workspace.Check, the workspace id in custos.yaml equals id, the pin is a
// commit on the catalog's main, and workspace.CheckAgainstCatalog against the
// catalog tree at the pin. The id and pin are only checked when custos.yaml
// itself is valid, and the answers only when the pin is.
func CheckWorkspaceUpdate(ws, cat *gitrepo.Repo, id, oldOID, newOID string) ([]problem.Problem, error) {
	if ps, err := checkHistory(ws, oldOID, newOID); err != nil || len(ps) > 0 {
		return ps, err
	}
	fsys, err := ws.TreeFS(newOID)
	if err != nil {
		return nil, err
	}
	w, ps := workspace.Load(fsys)
	ps = append(ps, w.Graph.Check()...)
	configOK := !slices.ContainsFunc(ps, func(p problem.Problem) bool { return p.Path == workspace.ConfigPath })
	if configOK {
		if w.Config.Workspace != id {
			ps = append(ps, problem.Problem{Path: workspace.ConfigPath, Rule: problem.RuleWorkspaceID,
				Message: "workspace " + w.Config.Workspace + " does not match this repository, which belongs to workspace " + id})
		}
		pps, c, err := pinnedCatalog(cat, w.Config.Catalog.Commit)
		if err != nil {
			return nil, err
		}
		ps = append(ps, pps...)
		if c != nil {
			ps = append(ps, workspace.CheckAgainstCatalog(w, c)...)
		}
	}
	problem.Sort(ps)
	return ps, nil
}

// pinnedCatalog loads the catalog at pin, which must be a commit on the
// catalog's main (ruling 2.16 allows any of them, also older ones).
func pinnedCatalog(cat *gitrepo.Repo, pin string) ([]problem.Problem, *catalog.Catalog, error) {
	var ps problem.List
	main, ok, err := cat.ResolveRef(mainRef)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		ps.Add(workspace.ConfigPath, problem.RulePin, "the catalog has no main branch yet; push the catalog first")
		return ps, nil, nil
	}
	oid, ok, err := cat.ResolveRef(pin + "^{commit}")
	if err != nil {
		return nil, nil, err
	}
	if !ok || oid != pin {
		ps.Add(workspace.ConfigPath, problem.RulePin, "catalog commit %s does not exist in this server's catalog", pin)
		return ps, nil, nil
	}
	onMain, err := cat.IsAncestor(pin, main)
	if err != nil {
		return nil, nil, err
	}
	if !onMain {
		ps.Add(workspace.ConfigPath, problem.RulePin, "catalog commit %s is not on the catalog's main branch", pin)
		return ps, nil, nil
	}
	fsys, err := cat.TreeFS(pin)
	if err != nil {
		return nil, nil, err
	}
	c, _ := catalog.Load(fsys) // valid: every commit on main passed CheckCatalogUpdate
	return nil, c, nil
}

func checkHistory(repo *gitrepo.Repo, oldOID, newOID string) ([]problem.Problem, error) {
	var ps problem.List
	if none(newOID) {
		ps.Add("", problem.RuleHistory, "main cannot be deleted")
		return ps, nil
	}
	if !none(oldOID) {
		ok, err := repo.IsAncestor(oldOID, newOID)
		if err != nil {
			return nil, err
		}
		if !ok {
			ps.Add("", problem.RuleHistory, "main cannot be rewritten; add commits on top of it instead of force-pushing")
		}
	}
	return ps, nil
}

// none reports whether oid stands for a missing ref.
func none(oid string) bool { return oid == "" || gitrepo.IsZero(oid) }
