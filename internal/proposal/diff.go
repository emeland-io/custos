package proposal

import (
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"slices"

	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/workspace"
)

// side is one tree: the workspace's main or a proposal branch's tip.
type side struct {
	ws   *workspace.Workspace
	fsys fs.FS
}

func loadSide(fsys fs.FS) side {
	w, _ := workspace.Load(fsys)
	return side{ws: w, fsys: fsys}
}

// entry is one recomputed item and the changes that apply it to main.
type entry struct {
	item    match.Item
	changes []gitrepo.Change
}

// selector returns the name Accept selects an item by.
func selector(it match.Item) string { return it.Kind + ":" + it.MatchKey }

// diff recomputes the items of a proposal from its tip against main.
//
// The scope of an ordinary proposal of task taskID is every generated task
// whose current version names taskID in produced_by.task.id, and every
// document that does, on either side. The scope of a cascade of removed
// task taskID is match.Below(taskID) on either side, and the documents
// produced by taskID or by one of those tasks, on either side.
//
// Within the scope, generated tasks are compared by id and documents by
// file path:
//   - only on the tip: added (task: all its version files are written;
//     document: its file is written);
//   - on both: unchanged when main has the tip's current version (task) or
//     the same bytes (document), else new-version (task: the tip's version
//     files main lacks are written; document: the tip's file is written);
//   - only on main: removed (task: all its version files and its answer are
//     deleted; document: its file is deleted).
func diff(main, tip side, taskID string, cascade bool) ([]entry, error) {
	mt, md := scope(main.ws, taskID, cascade)
	tt, td := scope(tip.ws, taskID, cascade)
	var out []entry
	for _, id := range union(mt, tt) {
		e, err := diffTask(main, tip, id)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	for _, path := range union(md, td) {
		e, err := diffDocument(main, tip, path)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	slices.SortStableFunc(out, func(a, b entry) int { return match.CompareItems(a.item, b.item) })
	return out, nil
}

// scope returns the generated task ids and document paths of w that belong
// to the proposal of taskID (see diff), sorted.
func scope(w *workspace.Workspace, taskID string, cascade bool) (tasks, docs []string) {
	producers := map[string]bool{taskID: true}
	if cascade {
		tasks = match.Below(w, taskID)
		for _, id := range tasks {
			producers[id] = true
		}
	} else {
		for _, id := range w.Graph.Tasks() {
			if g := match.Current(w, id); g != nil && g.ProducedBy.Task.ID == taskID {
				tasks = append(tasks, id)
			}
		}
	}
	for _, path := range slices.Sorted(maps.Keys(w.Documents)) {
		if producers[w.Documents[path].ProducedBy.Task.ID] {
			docs = append(docs, path)
		}
	}
	return tasks, docs
}

func union(a, b []string) []string {
	return slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(a), b...))))
}

func diffTask(main, tip side, id string) (entry, error) {
	m, t := match.Current(main.ws, id), match.Current(tip.ws, id)
	switch {
	case t == nil: // only on main
		e := entry{item: match.Item{Kind: match.KindTask, MatchKey: m.MatchKey, Action: match.Removed, ID: id, From: m.Version, Title: m.Title}}
		for _, v := range main.ws.Graph.Versions(id) {
			e.changes = append(e.changes, gitrepo.Change{Path: v.Path, Delete: true})
		}
		if answer := "answers/" + id + ".md"; main.ws.Answers[answer] != nil {
			e.changes = append(e.changes, gitrepo.Change{Path: answer, Delete: true})
		}
		return e, nil
	case m != nil:
		if _, ok := main.ws.Graph.Lookup(t.Ref()); ok {
			return entry{item: match.Item{Kind: match.KindTask, MatchKey: t.MatchKey, Action: match.Unchanged, ID: id,
				From: t.Version, To: t.Version, Title: t.Title}}, nil
		}
	}
	e := entry{item: match.Item{Kind: match.KindTask, MatchKey: t.MatchKey, Action: match.Added, ID: id, To: t.Version, Title: t.Title}}
	if m != nil {
		e.item.Action, e.item.From = match.NewVersion, m.Version
	}
	for _, v := range tip.ws.Graph.Versions(id) {
		if _, ok := main.ws.Graph.Lookup(v.Ref()); ok {
			continue
		}
		data, err := fs.ReadFile(tip.fsys, v.Path)
		if err != nil {
			return entry{}, err
		}
		e.changes = append(e.changes, gitrepo.Change{Path: v.Path, Data: data})
	}
	return e, nil
}

func diffDocument(main, tip side, path string) (entry, error) {
	m, t := main.ws.Documents[path], tip.ws.Documents[path]
	id := documentID(path)
	if t == nil {
		return entry{
			item:    match.Item{Kind: match.KindDocument, MatchKey: m.MatchKey, Action: match.Removed, ID: id, Title: m.Name},
			changes: []gitrepo.Change{{Path: path, Delete: true}},
		}, nil
	}
	data, err := fs.ReadFile(tip.fsys, path)
	if err != nil {
		return entry{}, err
	}
	e := entry{item: match.Item{Kind: match.KindDocument, MatchKey: t.MatchKey, Action: match.Added, ID: id,
		Title: t.Name, Verification: t.Verification}}
	if m != nil {
		cur, err := fs.ReadFile(main.fsys, path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return entry{}, err
		}
		if bytes.Equal(cur, data) {
			e.item.Action = match.Unchanged
			return e, nil
		}
		e.item.Action = match.NewVersion
	}
	e.changes = []gitrepo.Change{{Path: path, Data: data}}
	return e, nil
}

// documentID returns the uuid in documents/<uuid>.json.
func documentID(path string) string {
	return path[len("documents/") : len(path)-len(".json")]
}
