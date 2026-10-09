package runs

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/store"
)

// ErrInvalid reports a malformed request, such as a dry-run image that is
// not pinned by digest. The REST API answers it with 400.
var ErrInvalid = errors.New("invalid request")

// maxSamples bounds the samples of a dry-run report.
const maxSamples = 5

var pinnedImageRE = regexp.MustCompile(`^[^\s@]+@sha256:[0-9a-f]{64}$`)

// DryRunReport is the state of a dry run (spec §5.7): how many bound
// answers it ran, how many failed, how many proposals it would open in how
// many workspaces, and up to five samples.
type DryRunReport struct {
	State      State    `json:"state"` // running, succeeded or failed
	Runs       int      `json:"runs"`
	Failed     int      `json:"failed"`
	Proposals  int      `json:"proposals"`
	Workspaces int      `json:"workspaces"`
	Samples    []Sample `json:"samples"`
	Error      string   `json:"error,omitempty"` // why the dry run itself failed
}

// Sample is one proposal a dry run would open.
type Sample struct {
	Workspace string     `json:"workspace"`
	Task      string     `json:"task"`
	Items     []ItemJSON `json:"items"`
}

// dryRun is one in-flight (or finished) dry-run job. Unlike a real run, it
// is never backed by a Record: it does not go through the queue, the
// per-answer-path serialization of nextReady/executing, or the run-key
// bookkeeping of keys/addKey/removeKey/rekey, since none of that applies —
// a dry run writes nothing, so there is nothing for a later run to race
// against or overwrite. It only shares mu/cond/busy with the real queue,
// purely so Wait() can block on it too.
type dryRun struct {
	report     DryRunReport
	workspaces map[string]bool // workspace ids that would get a proposal
}

// DryRun starts running image instead of the registered image of
// processor name over every answer bound to name, in every workspace
// (bindings at each workspace's pin), and returns the job id. Nothing is
// written: outputs are matched against main and counted. The image must be
// pinned by digest (ErrInvalid); the processor must be registered on the
// catalog's main (store.ErrNotFound). Jobs live in memory only.
func (s *Service) DryRun(name, image string) (string, error) {
	if !pinnedImageRE.MatchString(image) {
		return "", fmt.Errorf("%w: image %q is not pinned by digest, as in registry.example.org/name@sha256:<64 hex digits>", ErrInvalid, image)
	}
	procs, err := s.Processors()
	if err != nil {
		return "", err
	}
	if !slices.ContainsFunc(procs, func(p ProcessorInfo) bool { return p.Name == name }) {
		return "", fmt.Errorf("processor %q: %w", name, store.ErrNotFound)
	}
	id := s.newID()
	job := &dryRun{report: DryRunReport{State: Running, Samples: []Sample{}}, workspaces: map[string]bool{}}
	s.mu.Lock()
	s.dry[id] = job
	s.busy++
	s.inFlight++
	ctx := s.ctx
	s.mu.Unlock()
	go func() {
		s.dryRun(ctx, job, name, image)
		s.mu.Lock()
		s.busy--
		s.inFlight--
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
	return id, nil
}

// DryRunStatus returns the report of dry run id; store.ErrNotFound when
// there is none (also after a restart, since dry runs live in memory
// only).
func (s *Service) DryRunStatus(id string) (DryRunReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.dry[id]
	if !ok {
		return DryRunReport{}, fmt.Errorf("dry run %s: %w", id, store.ErrNotFound)
	}
	r := job.report
	r.Samples = slices.Clone(r.Samples)
	return r, nil
}

// dryRun does the work of one dry-run job: resolve the candidate image
// once, then run it over every answer bound to name, in every workspace,
// one container at a time, using process (shared with real runs) against
// each workspace's own pinned bindings. It never calls enqueue, add,
// addKey or anything else that touches the real queue's bookkeeping,
// because a dry run creates no Record and writes no proposal.
func (s *Service) dryRun(ctx context.Context, job *dryRun, name, image string) {
	finish := func(state State, err error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		job.report.State = state
		if err != nil {
			job.report.Error = err.Error()
		}
	}
	ref, digest, err := s.rn.Resolve(ctx, image)
	if err != nil {
		finish(Failed, err)
		return
	}
	ids, err := s.st.WorkspaceIDs()
	if err != nil {
		finish(Failed, err)
		return
	}
	for _, wsID := range ids {
		snap, ok, err := s.load(wsID)
		if err != nil || !ok {
			continue // a workspace that cannot be read has no answers to try
		}
		for _, path := range slices.Sorted(maps.Keys(snap.w.Answers)) {
			if ctx.Err() != nil {
				finish(Failed, ctx.Err())
				return
			}
			a := snap.w.Answers[path]
			b, ok := bindingFor(snap.w, snap.c, a)
			if !ok || b.name != name {
				continue
			}
			ch, _, err := s.process(ctx, wsID, snap, a, b, ref, digest)
			s.mu.Lock()
			job.report.Runs++
			switch {
			case err != nil:
				job.report.Failed++
			case len(ch.Files) > 0:
				job.report.Proposals++
				job.workspaces[wsID] = true
				job.report.Workspaces = len(job.workspaces)
				if len(job.report.Samples) < maxSamples {
					job.report.Samples = append(job.report.Samples, Sample{Workspace: wsID, Task: a.Task, Items: itemsJSON(ch.Items)})
				}
			}
			s.mu.Unlock()
		}
	}
	finish(Succeeded, nil)
}

// ProcessorInfo is one entry of the registry on the catalog's main.
type ProcessorInfo struct {
	Name       string   `json:"name"`
	Image      string   `json:"image"`
	Digest     string   `json:"digest"`
	Timeout    string   `json:"timeout"`
	Network    bool     `json:"network"`
	Secrets    []string `json:"secrets"`
	BoundTasks []string `json:"bound_tasks"`
}

// Processors returns the registry on the catalog's main, sorted by name;
// empty while the catalog has no main.
func (s *Service) Processors() ([]ProcessorInfo, error) {
	cat := s.st.CatalogRepo()
	head, ok, err := cat.ResolveRef(mainRef)
	if err != nil {
		return nil, err
	}
	out := []ProcessorInfo{}
	if !ok {
		return out, nil
	}
	fsys, err := cat.TreeFS(head)
	if err != nil {
		return nil, err
	}
	c, _ := catalog.Load(fsys)
	for _, name := range slices.Sorted(maps.Keys(c.Registry.Processors)) {
		p := c.Registry.Processors[name]
		info := ProcessorInfo{Name: name, Image: p.Image, Timeout: p.Timeout, Network: p.Network,
			Secrets: slices.Clone(p.Secrets), BoundTasks: []string{}}
		if _, d, ok := cutDigest(p.Image); ok {
			info.Digest = d
		}
		if info.Timeout == "" {
			info.Timeout = "60s"
		}
		if info.Secrets == nil {
			info.Secrets = []string{}
		}
		for _, id := range slices.Sorted(maps.Keys(c.Registry.Bindings)) {
			if c.Registry.Bindings[id] == name {
				info.BoundTasks = append(info.BoundTasks, id)
			}
		}
		out = append(out, info)
	}
	return out, nil
}
