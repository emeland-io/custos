package runs

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/store"
)

// pathKey identifies one answer of one workspace, independent of its
// content or binding: the unit that execution is serialized on (see
// Service.executing).
type pathKey struct{ ws, path string }

// Service owns the run queue and the run records of one server.
type Service struct {
	st    *store.Store
	bl    *blobs.Store
	rn    *runner.Runner
	v     attest.Verifier
	cfg   Config
	files files
	newID func() string // run, dry-run, generated task and document ids
	now   func() time.Time

	// snapMu orders reading main for runs: a scan (or Rerun) holds it from
	// reading main until its runs are queued, a starting run from reading
	// main until its key is recorded. So a scan either sees the key of a
	// run that read the same main, or the run reads main after the scan.
	snapMu sync.Mutex

	mu      sync.Mutex
	cond    *sync.Cond      // signalled whenever the fields below change
	ctx     context.Context // from Start; Background before
	started bool
	records map[string]map[string]*Record // workspace id → run id → record

	// keys counts, for each run key, how many records currently carry it
	// in their Key field (see addKey/removeKey/hasKey). A key is only
	// added once a run has actually bound to it (at enqueue, with the
	// key intended at scan time, and rebound in prepare if main moved on
	// again before the run started) — never left behind under a key a
	// record no longer carries, so a later revert to that exact content
	// is not mistaken for already covered (see TestRevertAfterCoalescing).
	keys map[string]int

	queue     []*Record          // queued records, oldest first
	starting  map[*Record]bool   // taken from the queue, main not read yet
	executing map[pathKey]bool   // answers with a run actually executing (past prepare); see nextReady
	scans     map[string]bool    // pending scans: workspace id → only ScanAll asked for it
	busy      int                // scans, runs and dry runs in progress
	dry       map[string]*dryRun // dry-run jobs by id; never persisted (see dryrun.go)
}

// New loads the run records below <data-dir>/runs. Records that were
// queued or running when the previous process stopped are queued again,
// oldest first; they start once Start is called.
func New(st *store.Store, bl *blobs.Store, rn *runner.Runner, v attest.Verifier, cfg Config) (*Service, error) {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 8
	}
	s := &Service{
		st: st, bl: bl, rn: rn, v: v, cfg: cfg,
		files:     files{dir: filepath.Join(st.DataDir(), "runs")},
		newID:     uuid.NewString,
		now:       func() time.Time { return time.Now().UTC() },
		ctx:       context.Background(),
		records:   map[string]map[string]*Record{},
		keys:      map[string]int{},
		scans:     map[string]bool{},
		starting:  map[*Record]bool{},
		executing: map[pathKey]bool{},
		dry:       map[string]*dryRun{},
	}
	s.cond = sync.NewCond(&s.mu)
	rs, err := s.files.load()
	if err != nil {
		return nil, fmt.Errorf("loading run records: %w", err)
	}
	slices.SortFunc(rs, func(a, b *Record) int { return cmp.Or(a.Queued.Compare(b.Queued), cmp.Compare(a.ID, b.ID)) })
	for _, r := range rs {
		s.add(r)
		if r.State == Queued || r.State == Running {
			r.State, r.Started = Queued, time.Time{}
			if err := s.files.save(r); err != nil {
				return nil, err
			}
			s.queue = append(s.queue, r)
		}
	}
	return s, nil
}

// add indexes a record and registers the key it currently carries (its
// contribution to that key's refcount; see addKey). The caller holds mu
// (or owns s exclusively, as New does before starting).
func (s *Service) add(r *Record) {
	m := s.records[r.Workspace]
	if m == nil {
		m = map[string]*Record{}
		s.records[r.Workspace] = m
	}
	m[r.ID] = r
	s.addKey(r.Key)
}

// addKey registers one more record's claim on key (a no-op for ""). The
// caller holds mu.
func (s *Service) addKey(key string) {
	if key != "" {
		s.keys[key]++
	}
}

// removeKey releases one record's claim on key (a no-op for ""). The
// caller holds mu.
func (s *Service) removeKey(key string) {
	if key == "" {
		return
	}
	if s.keys[key] <= 1 {
		delete(s.keys, key)
	} else {
		s.keys[key]--
	}
}

// hasKey reports whether any record currently carries key in its Key
// field. The caller holds mu.
func (s *Service) hasKey(key string) bool { return s.keys[key] > 0 }

// rekey moves one record's claim from oldKey to newKey (same key: a
// no-op net of the refcount, used so prepare can call it unconditionally).
// The caller holds mu.
func (s *Service) rekey(oldKey, newKey string) {
	s.removeKey(oldKey)
	s.addKey(newKey)
}

// Start starts the scanner and cfg.Workers workers and returns at once.
// They stop when ctx is cancelled; a run interrupted that way stays
// "running" on disk and is queued again by the next New.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started, s.ctx = true, ctx
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
	go s.scanner(ctx)
	for range s.cfg.Workers {
		go s.worker(ctx)
	}
}

// Scan asks for workspace wsID to be scanned for answers that need a run
// (see scan). It does not block; requests for the same workspace that
// arrive before the scan starts are served by one scan.
func (s *Service) Scan(wsID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scans[wsID] = false
	s.cond.Broadcast()
}

// ScanAll asks for every workspace to be scanned. Runs it finds for
// answers that were never run before get the reason "start-up", since
// serve calls it once at start to catch up with changes made while it was
// down.
func (s *Service) ScanAll() {
	ids, err := s.st.WorkspaceIDs()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if _, ok := s.scans[id]; !ok {
			s.scans[id] = true
		}
	}
	s.cond.Broadcast()
}

// Wait blocks until no scan is pending or running, the queue is empty and
// no run or dry run is active. Tests use it after Start.
func (s *Service) Wait() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.scans) > 0 || len(s.queue) > 0 || s.busy > 0 {
		s.cond.Wait()
	}
}

// Runs returns the records of workspace wsID, newest first.
func (s *Service) Runs(wsID string) ([]Record, error) {
	if _, err := s.st.WorkspaceRepo(wsID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, 0, len(s.records[wsID]))
	for _, r := range s.records[wsID] {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b Record) int { return cmp.Or(b.Queued.Compare(a.Queued), cmp.Compare(b.ID, a.ID)) })
	return out, nil
}

// Get returns one record and its log; store.ErrNotFound when the
// workspace has no such run.
func (s *Service) Get(wsID, runID string) (*Record, []byte, error) {
	s.mu.Lock()
	r, ok := s.records[wsID][runID]
	var rec Record
	if ok {
		rec = *r
	}
	s.mu.Unlock()
	if !ok {
		return nil, nil, fmt.Errorf("run %s of workspace %s: %w", runID, wsID, store.ErrNotFound)
	}
	log, err := s.files.log(&rec)
	if err != nil {
		return nil, nil, err
	}
	return &rec, log, nil
}

// Retry queues a new run of the answer of failed run runID, with reason
// "retry" and retry_of set. store.ErrNotFound for an unknown run,
// store.ErrConflict when the run did not fail.
func (s *Service) Retry(wsID, runID string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.records[wsID][runID]
	if !ok {
		return nil, fmt.Errorf("run %s of workspace %s: %w", runID, wsID, store.ErrNotFound)
	}
	if old.State != Failed {
		return nil, fmt.Errorf("%w: run %s is %s; only failed runs can be retried", store.ErrConflict, runID, old.State)
	}
	r := &Record{
		Workspace: wsID, Task: old.Task, AnswerPath: old.AnswerPath, AnswerBlob: old.AnswerBlob,
		AnswerCommit: old.AnswerCommit, Processor: old.Processor, Image: old.Image, Digest: old.Digest,
		Key: old.Key, Reason: ReasonRetry, RetryOf: old.ID,
	}
	if err := s.enqueue(r); err != nil {
		return nil, err
	}
	out := *r
	return &out, nil
}

// enqueue gives r an id, the state Queued and the queue time, saves it
// and appends it to the queue. The caller holds mu.
func (s *Service) enqueue(r *Record) error {
	r.ID, r.State, r.Queued = s.newID(), Queued, s.now()
	if err := s.files.save(r); err != nil {
		return err
	}
	s.add(r)
	s.queue = append(s.queue, r)
	s.cond.Broadcast()
	return nil
}

// queuedFor returns a record of an answer that will still read main: a
// queued one, or one taken by a worker that has not read main yet; nil if
// there is none. The caller holds mu.
func (s *Service) queuedFor(wsID, path string) *Record {
	for _, r := range s.queue {
		if r.Workspace == wsID && r.AnswerPath == path {
			return r
		}
	}
	for r := range s.starting {
		if r.Workspace == wsID && r.AnswerPath == path {
			return r
		}
	}
	return nil
}

// latestFor returns the newest record of an answer, or nil. The caller
// holds mu.
func (s *Service) latestFor(wsID, path string) *Record {
	var latest *Record
	for _, r := range s.records[wsID] {
		if r.AnswerPath == path && (latest == nil || r.Queued.After(latest.Queued)) {
			latest = r
		}
	}
	return latest
}

// terminalFor returns a copy of a terminal (Succeeded or Failed) record of
// workspace wsID whose Key is key, other than excludeID, or nil if there is
// none. Used by prepare to avoid running a processor again for a key that
// already has a result (see Important finding I1); the caller holds mu.
func (s *Service) terminalFor(wsID, key, excludeID string) *Record {
	for _, r := range s.records[wsID] {
		if r.ID != excludeID && r.Key == key && (r.State == Succeeded || r.State == Failed) {
			out := *r
			return &out
		}
	}
	return nil
}

// scanner serves Scan and ScanAll requests one workspace at a time.
func (s *Service) scanner(ctx context.Context) {
	for {
		s.mu.Lock()
		for len(s.scans) == 0 && ctx.Err() == nil {
			s.cond.Wait()
		}
		if ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		var id string
		var startUp bool
		for k, v := range s.scans {
			id, startUp = k, v
			break
		}
		delete(s.scans, id)
		s.busy++
		s.mu.Unlock()

		s.scan(id, startUp) // errors mean nothing can be run now; the next scan retries

		s.mu.Lock()
		s.busy--
		s.cond.Broadcast()
		s.mu.Unlock()
	}
}

// worker executes queued runs until ctx is cancelled. It only ever starts
// executing one run per answer path at a time (see nextReady), so an older
// run's late-finishing proposal write can never land after, and overwrite,
// a newer run's — the newer run simply cannot start until the older one
// has fully finished (Critical finding C2).
func (s *Service) worker(ctx context.Context) {
	for {
		s.mu.Lock()
		var r *Record
		for {
			// ctx is checked before every call to nextReady, never
			// after: nextReady mutates s.queue/s.executing, and checking
			// ctx only afterward could pop and mark a record executing
			// and then discard it on this same iteration without ever
			// reaching the cleanup below, leaking that mark forever.
			if ctx.Err() != nil {
				s.mu.Unlock()
				return
			}
			if r = s.nextReady(); r != nil {
				break
			}
			s.cond.Wait()
		}
		s.starting[r] = true
		s.busy++
		s.mu.Unlock()

		s.execute(ctx, r)

		s.mu.Lock()
		delete(s.executing, pathKey{r.Workspace, r.AnswerPath})
		s.busy--
		s.cond.Broadcast()
		s.mu.Unlock()
	}
}

// nextReady removes and returns the first queued record whose answer path
// has no run currently executing, and marks that path executing; nil when
// every queued record's path is already covered by a run in progress (or
// the queue is empty) — such a record is left in the queue, where
// queuedFor still reports it as pending. The caller holds mu.
func (s *Service) nextReady() *Record {
	for i, r := range s.queue {
		k := pathKey{r.Workspace, r.AnswerPath}
		if !s.executing[k] {
			s.queue = append(s.queue[:i:i], s.queue[i+1:]...)
			s.executing[k] = true
			return r
		}
	}
	return nil
}

// update changes r under mu and saves it. change is responsible for any
// key bookkeeping its edit requires (see prepare, the only caller that
// rewrites r.Key).
func (s *Service) update(r *Record, change func(r *Record)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(r)
	return s.files.save(r)
}
