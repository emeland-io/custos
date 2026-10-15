package catalog

import (
	"fmt"
	"io/fs"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

// NewVersionChanges returns the single gitrepo.Change that adds the next
// version file of task id in the catalog tree fsys: version bumped by step
// from the current version, previous set to that version, and title, body,
// answer_type and choices exactly as given. It computes the same
// next-version and previous values as the disk-based NewVersion (used by
// the CLI), over an fs.FS instead of a directory, for use inside a
// catalogdraft.Update edit closure; problems elsewhere in the catalog do
// not block it.
func NewVersionChanges(fsys fs.FS, id string, step semver.Step, title, body string,
	answerType task.AnswerType, choices []string) ([]gitrepo.Change, error) {
	c, _ := Load(fsys)
	if !c.Tasks.Has(id) {
		return nil, fmt.Errorf("task %s not found in this catalog", id)
	}
	cur, ok := c.Tasks.Current(id)
	if !ok {
		if c.Tasks.Superseded(id) {
			return nil, fmt.Errorf("task %s was merged into another task and has no current version", id)
		}
		return nil, fmt.Errorf("task %s has several current versions; publish a version that merges them first", id)
	}
	next, err := semver.Bump(cur.Version, step)
	if err != nil {
		return nil, err
	}
	if _, exists := c.Tasks.Lookup(task.Ref{ID: id, Version: next}); exists {
		return nil, fmt.Errorf("version %s of task %s already exists", next, id)
	}
	m := task.Meta{ID: id, Version: next, Title: title, AnswerType: answerType, Choices: choices, Previous: []task.Ref{cur.Ref()}}
	data, err := frontmatter.Encode(m, body)
	if err != nil {
		return nil, err
	}
	return []gitrepo.Change{{Path: "tasks/" + id + "/" + next + ".md", Data: data}}, nil
}
