package runs

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

const mainRef = "refs/heads/main"

// binding is the processor an answer is run with.
type binding struct {
	name    string            // processor name in the registry
	proc    catalog.Processor // its registry entry at the workspace's pin
	digest  string            // "sha256:<hex>" from proc.Image
	version *task.Version     // the task version the answer was written for
}

// bindingFor returns the processor that answer a is run with: for a catalog
// task the binding in the pinned catalog's registry, for a generated task
// the processor named by the answered version; ok is false when there is
// none, the name is not registered, or the answered version does not exist.
func bindingFor(w *workspace.Workspace, c *catalog.Catalog, a *workspace.Answer) (binding, bool) {
	ref := task.Ref{ID: a.Task, Version: a.TaskVersion}
	var name string
	var v *task.Version
	var ok bool
	switch {
	case c.Tasks.Has(a.Task):
		v, ok = c.Tasks.Lookup(ref)
		name = c.Registry.Bindings[a.Task]
	case w.Graph.Has(a.Task):
		v, ok = w.Graph.Lookup(ref)
		if ok {
			if g := w.Generated[v.Path]; g != nil {
				name = g.Processor
			}
		}
	}
	if !ok || name == "" {
		return binding{}, false
	}
	p, ok := c.Registry.Processors[name]
	if !ok {
		return binding{}, false
	}
	_, digest, ok := cutDigest(p.Image)
	if !ok {
		return binding{}, false
	}
	return binding{name: name, proc: p, digest: digest, version: v}, true
}

// cutDigest splits "<ref>@sha256:<hex>" into the reference and the digest
// "sha256:<hex>"; the catalog's validation guarantees that form on main.
func cutDigest(image string) (ref, digest string, ok bool) {
	ref, digest, ok = strings.Cut(image, "@")
	return ref, digest, ok && strings.HasPrefix(digest, "sha256:")
}

// snapshot is a workspace's main as one scan or run sees it.
type snapshot struct {
	commit string
	w      *workspace.Workspace
	c      *catalog.Catalog
	blobs  map[string]string // answer path → git blob oid
}

// load reads workspace wsID's main; ok is false when it has no main yet.
func (s *Service) load(wsID string) (*snapshot, bool, error) {
	repo, err := s.st.WorkspaceRepo(wsID)
	if err != nil {
		return nil, false, err
	}
	commit, ok, err := repo.ResolveRef(mainRef)
	if err != nil || !ok {
		return nil, false, err
	}
	w, c, err := s.st.Load(wsID, commit)
	if err != nil {
		return nil, false, err
	}
	ids, err := repo.BlobIDs(commit, "answers")
	if err != nil {
		return nil, false, err
	}
	return &snapshot{commit: commit, w: w, c: c, blobs: ids}, true, nil
}

// scan queues a run for every answer of a bound task on wsID's main whose
// key has no record and that has no queued run already (a queued run reads
// main when it starts, so it covers the newer answer). startUp sets the
// reason of answers never run before to "start-up".
func (s *Service) scan(wsID string, startUp bool) error {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	snap, ok, err := s.load(wsID)
	if err != nil || !ok {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range slices.Sorted(maps.Keys(snap.w.Answers)) {
		a := snap.w.Answers[path]
		b, ok := bindingFor(snap.w, snap.c, a)
		if !ok {
			continue
		}
		key := runKey(wsID, path, snap.blobs[path], b.digest)
		if s.hasKey(key) || s.queuedFor(wsID, path) != nil {
			continue
		}
		r := s.newRecord(wsID, snap, a, b)
		r.Key, r.Reason = key, s.reason(wsID, path, snap.blobs[path], b, startUp)
		if err := s.enqueue(r); err != nil {
			return err
		}
	}
	return nil
}

// reason tells why an answer needs a run, from its newest earlier record.
// The caller holds mu.
func (s *Service) reason(wsID, path, blob string, b binding, startUp bool) string {
	prev := s.latestFor(wsID, path)
	switch {
	case prev == nil && startUp:
		return ReasonStartUp
	case prev == nil || prev.AnswerBlob != blob:
		return ReasonAnswer
	case prev.Processor != b.name:
		return ReasonBinding
	case prev.Digest != b.digest:
		return ReasonDigest
	}
	return ReasonAnswer
}

// newRecord fills the answer and processor fields of a new record.
func (s *Service) newRecord(wsID string, snap *snapshot, a *workspace.Answer, b binding) *Record {
	return &Record{
		Workspace: wsID, Task: task.Ref{ID: a.Task, Version: a.TaskVersion},
		AnswerPath: a.Path, AnswerBlob: snap.blobs[a.Path], AnswerCommit: snap.commit,
		Processor: b.name, Image: b.proc.Image, Digest: b.digest,
	}
}

// Rerun queues a run for the answer of each task in taskIDs on wsID's
// main, whether or not its key has a record (merge reruns, §4.4). Tasks
// without an answer or without a processor are skipped. An answer that
// already has a queued run gets no second one; that record is returned
// instead. The result is sorted by task id.
func (s *Service) Rerun(wsID string, taskIDs []string, reason string) ([]Record, error) {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	snap, ok, err := s.load(wsID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("workspace %s has no main branch: %w", wsID, store.ErrNotFound)
	}
	ids := slices.Clone(taskIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Record{}
	for _, id := range ids {
		a := snap.w.Answers["answers/"+id+".md"]
		if a == nil {
			continue
		}
		b, ok := bindingFor(snap.w, snap.c, a)
		if !ok {
			continue
		}
		if q := s.queuedFor(wsID, a.Path); q != nil {
			out = append(out, *q)
			continue
		}
		r := s.newRecord(wsID, snap, a, b)
		r.Key, r.Reason = runKey(wsID, a.Path, snap.blobs[a.Path], b.digest), reason
		if err := s.enqueue(r); err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, nil
}
