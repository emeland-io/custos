package merge

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"

	"go.yaml.in/yaml/v3"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

var pinRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// decodeConfig parses custos.yaml strictly, like workspace.Load. nil data
// (no file) is an error.
func decodeConfig(data []byte) (workspace.Config, error) {
	var c workspace.Config
	if data == nil {
		return c, errors.New("custos.yaml is missing")
	}
	if err := frontmatter.DecodeStrict(bytes.TrimPrefix(data, []byte("\ufeff")), &c); err != nil {
		return workspace.Config{}, fmt.Errorf("custos.yaml: %w", err)
	}
	return c, nil
}

// encodeConfig writes custos.yaml with two-space indentation.
func encodeConfig(c workspace.Config) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

type configOutcome int

const (
	configUnreadable  configOutcome = iota // a version cannot be parsed; git's merge applies
	configMerged                           // merged field by field
	configPinConflict                      // both sides moved the pin to unrelated commits
)

// config settles custos.yaml whenever main and the branch hold different
// versions of it, whether or not git found a textual conflict.
func (p *plan) config(st *store.Store, repo *gitrepo.Repo, base, ours, theirs string, mt *gitrepo.MergeTreeResult, res map[string]Resolution) error {
	var files [3][]byte
	for i, rev := range []string{base, ours, theirs} {
		data, _, err := repo.ReadFile(rev, configPath)
		if err != nil {
			return err
		}
		files[i] = data
	}
	if (files[1] == nil) == (files[2] == nil) && bytes.Equal(files[1], files[2]) {
		return nil
	}
	merged, outcome, err := mergeConfig(st.CatalogRepo(), files[0], files[1], files[2])
	if err != nil {
		return err
	}
	switch outcome {
	case configMerged:
		p.changes = append(p.changes, gitrepo.Change{Path: configPath, Data: merged})
		return nil
	case configPinConflict:
		return p.settle(configPath, files[1], files[2], res)
	}
	for _, c := range mt.Conflicts {
		if c.Path == configPath {
			return p.settle(c.Path, c.Ours, c.Theirs, res)
		}
	}
	return nil
}

// mergeConfig merges three versions of custos.yaml. The workspace id is
// always main's, because a branch may come from a fork. catalog.url and
// frozen take the side that changed them, main's when both did. The pin
// takes the side that moved it; when both moved it, the descendant of the
// two catalog commits (spec §4.4), or configPinConflict when neither
// descends from the other.
func mergeConfig(cat *gitrepo.Repo, base, ours, theirs []byte) ([]byte, configOutcome, error) {
	b, errB := decodeConfig(base)
	o, errO := decodeConfig(ours)
	t, errT := decodeConfig(theirs)
	if errB != nil || errO != nil || errT != nil {
		return nil, configUnreadable, nil
	}
	m := o
	m.Catalog.URL = pick(b.Catalog.URL, o.Catalog.URL, t.Catalog.URL)
	m.Frozen = pick(b.Frozen, o.Frozen, t.Frozen)
	pin, ok, err := mergePin(cat, b.Catalog.Commit, o.Catalog.Commit, t.Catalog.Commit)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, configPinConflict, nil
	}
	m.Catalog.Commit = pin
	data, err := encodeConfig(m)
	if err != nil {
		return nil, 0, err
	}
	return data, configMerged, nil
}

// pick is a three-way merge of one value: main's unless only the branch
// changed it.
func pick[T comparable](base, ours, theirs T) T {
	if ours == base {
		return theirs
	}
	return ours
}

// mergePin merges the pinned catalog commit. ok is false when both sides
// moved it and neither commit descends from the other, or when a pin is not
// a commit of the catalog.
func mergePin(cat *gitrepo.Repo, base, ours, theirs string) (pin string, ok bool, err error) {
	if ours == theirs || theirs == base {
		return ours, true, nil
	}
	if ours == base {
		return theirs, true, nil
	}
	for _, c := range []string{ours, theirs} {
		if !pinRE.MatchString(c) {
			return "", false, nil
		}
		if has, err := cat.HasCommit(c); err != nil || !has {
			return "", false, err
		}
	}
	up, err := cat.IsAncestor(ours, theirs)
	if err != nil {
		return "", false, err
	}
	if up {
		return theirs, true, nil
	}
	down, err := cat.IsAncestor(theirs, ours)
	if err != nil {
		return "", false, err
	}
	if down {
		return ours, true, nil
	}
	return "", false, nil
}
