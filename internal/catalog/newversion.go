package catalog

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
)

// NewVersion writes the next version of task id into the catalog checkout at
// dir. The new version copies the current one, increases the version by
// step and lists the current version as previous. It returns the new file's
// path relative to dir. Problems elsewhere in the catalog do not block it.
func NewVersion(dir, id string, step semver.Step) (string, error) {
	c, _ := Load(os.DirFS(dir))
	if !c.Tasks.Has(id) {
		return "", fmt.Errorf("task %s not found in %s", id, dir)
	}
	cur, ok := c.Tasks.Current(id)
	if !ok {
		if c.Tasks.Superseded(id) {
			return "", fmt.Errorf("task %s was merged into another task and has no current version", id)
		}
		return "", fmt.Errorf("task %s has several current versions; publish a version that merges them first", id)
	}
	next, err := semver.Bump(cur.Version, step)
	if err != nil {
		return "", err
	}
	if _, exists := c.Tasks.Lookup(task.Ref{ID: id, Version: next}); exists {
		return "", fmt.Errorf("version %s of task %s already exists", next, id)
	}
	m := cur.Meta
	m.Version = next
	m.Previous = []task.Ref{cur.Ref()}
	data, err := frontmatter.Encode(m, cur.Body)
	if err != nil {
		return "", err
	}
	rel := "tasks/" + id + "/" + next + ".md"
	f, err := os.OpenFile(filepath.Join(dir, filepath.FromSlash(rel)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	return rel, f.Close()
}
