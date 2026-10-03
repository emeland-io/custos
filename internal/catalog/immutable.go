package catalog

import (
	"bytes"
	"errors"
	"io/fs"

	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/repofile"
)

// CheckImmutable applies rule 1: every task version file in oldFS must exist
// unchanged in newFS.
func CheckImmutable(oldFS, newFS fs.FS) []problem.Problem {
	var ps problem.List
	repofile.Walk(oldFS, "tasks", taskLayout, "tasks/<uuid>/<semver>.md", func(path string, old []byte) []problem.Problem {
		cur, err := fs.ReadFile(newFS, path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			ps.Add(path, problem.RuleImmutable, "was removed; published task versions never change, create a new version instead")
		case err != nil:
			ps.Add(path, problem.RuleFormat, "%v", err)
		case !bytes.Equal(old, cur):
			ps.Add(path, problem.RuleImmutable, "was changed; published task versions never change, create a new version instead")
		}
		return nil
	})
	problem.Sort(ps)
	return ps
}
