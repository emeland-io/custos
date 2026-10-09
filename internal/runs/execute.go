package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// execute performs one queued run: it reads main as it is now, runs the
// processor bound to the answer, matches the output and writes the
// proposal. Every failure ends the run as Failed with the error and the
// log kept. When ctx is cancelled (server shutdown) the record is left
// "running", so the next process queues it again.
func (s *Service) execute(ctx context.Context, r *Record) {
	s.update(r, func(r *Record) { r.State, r.Started = Running, s.now() })
	outcome, branch, log, err := s.perform(ctx, r)
	if ctx.Err() != nil {
		return
	}
	if log == nil {
		log = []byte{}
	}
	s.files.saveLog(r, log)
	s.update(r, func(r *Record) {
		r.Finished = s.now()
		if err != nil {
			r.State, r.Error = Failed, err.Error()
			return
		}
		r.State, r.Outcome, r.Branch = Succeeded, outcome, branch
	})
}

// perform runs r against main and returns the outcome, the proposal branch
// (when proposed) and the processor's log.
func (s *Service) perform(ctx context.Context, r *Record) (Outcome, string, []byte, error) {
	snap, a, b, existing, err := s.prepare(r)
	if err != nil {
		return None, "", nil, err
	}
	// The key this run bound to already has a terminal result from
	// another record, and this run was not explicitly asked to redo the
	// work (retry/merge are) — redundant, so report that result instead
	// of running the processor again (Important finding I1).
	if existing != nil {
		if existing.State == Failed {
			return None, "", nil, fmt.Errorf("skipped: run %s already failed with the exact same answer and processor: %s",
				existing.ID, existing.Error)
		}
		return Unchanged, "", nil, nil
	}
	ref, digest, err := s.rn.Resolve(ctx, b.proc.Image)
	if err != nil {
		return None, "", nil, err
	}
	ch, log, err := s.process(ctx, r.Workspace, snap, a, b, ref, digest)
	if err != nil {
		return None, "", log, err
	}
	branch, err := proposal.Write(s.st, r.Workspace, a.Task, digest, ch,
		fmt.Sprintf("Propose output of %s for task %s", b.name, a.Task))
	if err != nil {
		return None, "", log, fmt.Errorf("writing the proposal: %w", err)
	}
	if branch == "" {
		return Unchanged, "", log, nil
	}
	return Proposed, branch, log, nil
}

// prepare reads main for run r and records what the run uses, which may
// be newer than what was queued: answer, processor, digest and key. Its
// claim on the key it was queued with (added when it was enqueued) moves
// to the key it actually binds to here, so a key is never left registered
// under a record that no longer carries it (Critical finding C1) and a
// revert to content this exact answer path held earlier, after being
// coalesced away before it ever ran, is not mistaken for already covered.
//
// existing is a terminal (Succeeded or Failed) record other than r that
// already carries the exact key r bound to, when r's reason does not
// itself call for redoing the work (retry and merge do); the caller must
// not run the processor again in that case (Important finding I1).
func (s *Service) prepare(r *Record) (*snapshot, *workspace.Answer, binding, *Record, error) {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.starting, r)
		s.mu.Unlock()
	}()
	snap, ok, err := s.load(r.Workspace)
	if err != nil {
		s.releaseKey(r)
		return nil, nil, binding{}, nil, err
	}
	if !ok {
		s.releaseKey(r)
		return nil, nil, binding{}, nil, fmt.Errorf("workspace %s has no main branch", r.Workspace)
	}
	a := snap.w.Answers[r.AnswerPath]
	if a == nil {
		s.releaseKey(r)
		return nil, nil, binding{}, nil, fmt.Errorf("%s is no longer on main", r.AnswerPath)
	}
	b, ok := bindingFor(snap.w, snap.c, a)
	if !ok {
		s.releaseKey(r)
		return nil, nil, binding{}, nil, fmt.Errorf("task %s is no longer bound to a registered processor", a.Task)
	}
	oldKey := r.Key
	var existing *Record
	err = s.update(r, func(r *Record) {
		r.Task = task.Ref{ID: a.Task, Version: a.TaskVersion}
		r.AnswerBlob, r.AnswerCommit = snap.blobs[a.Path], snap.commit
		r.Processor, r.Image, r.Digest = b.name, b.proc.Image, b.digest
		r.Key = runKey(r.Workspace, a.Path, r.AnswerBlob, b.digest)
		s.rekey(oldKey, r.Key)
		if r.Reason != ReasonRetry && r.Reason != ReasonMerge {
			existing = s.terminalFor(r.Workspace, r.Key, r.ID)
		}
	})
	return snap, a, b, existing, err
}

// releaseKey clears r's claim on the key it currently holds (its intended,
// queue-time key, since this is only called before prepare ever reaches a
// successful rekey to a confirmed one) and r.Key itself, so a run that
// fails before it can bind to a real key — the answer is gone, or the task
// is no longer bound to a registered processor — never leaves that key
// registered under a record that no longer meaningfully carries it (same
// bug class as Critical finding C1, found in review round 2). execute's
// subsequent update to Failed persists the cleared field; this only needs
// to update the in-memory bookkeeping under mu, since r is exclusively
// owned by the goroutine executing it until that later save happens.
func (s *Service) releaseKey(r *Record) {
	s.mu.Lock()
	s.rekey(r.Key, "")
	r.Key = ""
	s.mu.Unlock()
}

// process runs image ref (digest "sha256:<hex>") with the timeout, network
// and secrets of b.proc on answer a and matches the output against snap;
// runs and dry runs (which pass another image) share it. It returns the
// processor's log also when it fails.
func (s *Service) process(ctx context.Context, wsID string, snap *snapshot, a *workspace.Answer, b binding,
	ref, digest string) (*match.Changes, []byte, error) {
	proc := b.proc
	input, err := json.Marshal(contract.NewInput(wsID, b.version, a))
	if err != nil {
		return nil, nil, err
	}
	job := runner.Job{Image: ref, Network: proc.Network, Secrets: proc.Secrets, Input: input, Blobs: map[string]string{}}
	if proc.Timeout != "" {
		if job.Timeout, err = time.ParseDuration(proc.Timeout); err != nil {
			return nil, nil, fmt.Errorf("processor %s: timeout %q: %w", b.name, proc.Timeout, err)
		}
	}
	for _, at := range a.Attachments {
		path, ok := s.bl.Path(at.SHA256)
		if !ok {
			return nil, nil, fmt.Errorf("attachment %q (sha256 %s) is not in the blob store", at.Name, at.SHA256)
		}
		job.Blobs[at.SHA256] = path
	}
	res, err := s.rn.Run(ctx, job)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case res.TimedOut:
		return nil, res.Log, fmt.Errorf("processor %s timed out after %s", b.name, res.Duration.Round(time.Second))
	case res.ExitCode != 0:
		return nil, res.Log, fmt.Errorf("processor %s exited with code %d", b.name, res.ExitCode)
	}
	out, err := contract.ParseOutput(res.Stdout)
	if err != nil {
		return nil, res.Log, fmt.Errorf("processor %s wrote invalid output: %w", b.name, err)
	}
	ch, err := match.Plan(match.Run{
		Workspace: snap.w, Catalog: snap.c,
		Task:      task.Ref{ID: a.Task, Version: a.TaskVersion},
		Processor: b.name, Digest: digest, AnswerCommit: snap.commit,
		MaxDepth: s.cfg.MaxDepth, Verifier: s.v, NewID: s.newID,
	}, out)
	if err != nil {
		return nil, res.Log, err
	}
	return ch, res.Log, nil
}
