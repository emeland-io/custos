// Package repofile walks and reads the files of a catalog or workspace tree.
package repofile

import (
	"errors"
	"io/fs"
	"regexp"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/problem"
)

// Walk calls fn for every file below root whose path matches layout. Other
// files are reported as unexpected; want describes the expected layout.
// A missing root is not a problem.
func Walk(fsys fs.FS, root string, layout *regexp.Regexp, want string, fn func(path string, data []byte) []problem.Problem) []problem.Problem {
	if _, err := fs.Stat(fsys, root); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var ps problem.List
	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !layout.MatchString(path) {
			ps.Add(path, problem.RulePath, "unexpected file; expected %s", want)
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		ps = append(ps, fn(path, data)...)
		return nil
	})
	if err != nil {
		ps.Add(root, problem.RuleFormat, "%v", err)
	}
	return ps
}

// ReadYAML decodes the YAML file at path into v, rejecting unknown fields.
// found is false when the file does not exist.
func ReadYAML(fsys fs.FS, path string, v any) (found bool, ps []problem.Problem) {
	data, err := fs.ReadFile(fsys, path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	var l problem.List
	if err != nil {
		l.Add(path, problem.RuleFormat, "%v", err)
		return true, l
	}
	if err := frontmatter.DecodeStrict(data, v); err != nil {
		l.Add(path, problem.RuleFormat, "%v", err)
	}
	return true, l
}
