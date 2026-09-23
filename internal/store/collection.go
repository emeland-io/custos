package store

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/model"
)

// element is the constraint for persisted element types.
type element[E any] interface {
	*E
	GetMeta() *model.Meta
}

// collection keeps the elements of one type in memory and mirrors each
// one as <dir>/<uuid>.json on disk. It is not safe for concurrent use; the
// Store serializes access.
type collection[E any, P element[E]] struct {
	dir   string
	items map[uuid.UUID]P
}

func newCollection[E any, P element[E]](dir string) *collection[E, P] {
	return &collection[E, P]{dir: dir, items: map[uuid.UUID]P{}}
}

// load reads every element file in the collection's dir, creating the dir
// if needed.
func (c *collection[E, P]) load() error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(c.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var v P = new(E)
		if err := json.Unmarshal(data, v); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		id := v.GetMeta().ID
		if id.String()+".json" != e.Name() {
			return fmt.Errorf("%s: file name does not match id %s", path, id)
		}
		c.items[id] = v
	}
	return nil
}

func (c *collection[E, P]) path(id uuid.UUID) string {
	return filepath.Join(c.dir, id.String()+".json")
}

// put writes v to disk, then to memory.
func (c *collection[E, P]) put(v P) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(c.path(v.GetMeta().ID), append(data, '\n')); err != nil {
		return err
	}
	c.items[v.GetMeta().ID] = v
	return nil
}

// remove deletes the element with id from disk and memory.
func (c *collection[E, P]) remove(id uuid.UUID) error {
	if err := os.Remove(c.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	delete(c.items, id)
	return nil
}

// get returns a copy of the element with id.
func (c *collection[E, P]) get(id uuid.UUID) (E, bool) {
	v, ok := c.items[id]
	if !ok {
		var zero E
		return zero, false
	}
	return *v, true
}

func (c *collection[E, P]) has(id uuid.UUID) bool {
	_, ok := c.items[id]
	return ok
}

// list returns copies of the elements matching keep (all when nil),
// oldest first.
func (c *collection[E, P]) list(keep func(P) bool) []E {
	out := []E{}
	for _, v := range c.items {
		if keep == nil || keep(v) {
			out = append(out, *v)
		}
	}
	slices.SortFunc(out, func(a, b E) int {
		ma, mb := P(&a).GetMeta(), P(&b).GetMeta()
		if c := ma.CreatedAt.Compare(mb.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(ma.ID.String(), mb.ID.String())
	})
	return out
}

// any reports whether an element matches pred.
func (c *collection[E, P]) any(pred func(P) bool) bool {
	for _, v := range c.items {
		if pred(v) {
			return true
		}
	}
	return false
}

// writeFileAtomic replaces path with data so that readers see either the
// old or the new content.
func writeFileAtomic(path string, data []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()
	if err = f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
