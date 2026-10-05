package match

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// ErrTooDeep fails a run whose output tasks would lie deeper than the
// maximum depth of generated tasks (spec §5.5).
var ErrTooDeep = errors.New("output exceeds the maximum depth of generated tasks")

// ErrInvalidOutput fails a run whose output names an unknown processor or
// origin, or repeats a match key. Plan wraps it with all such problems.
var ErrInvalidOutput = errors.New("invalid processor output")

// DefaultMaxDepth is the depth limit used when Run.MaxDepth is not positive.
const DefaultMaxDepth = 8

// Run describes the processor run whose output Plan matches.
type Run struct {
	Workspace    *workspace.Workspace // main at AnswerCommit
	Catalog      *catalog.Catalog     // at the workspace's pin
	Task         task.Ref             // the answered task (catalog or generated) and the answer's task_version
	Processor    string
	Digest       string // "sha256:<hex>"
	AnswerCommit string
	MaxDepth     int
	Verifier     attest.Verifier
	NewID        func() string // UUID v4 source; tests make it deterministic
}

// Action says what a proposal does with one item.
type Action string

const (
	Added      Action = "added"
	NewVersion Action = "new-version"
	Unchanged  Action = "unchanged"
	Removed    Action = "removed"
)

// Item is one generated task or document of a proposal.
type Item struct {
	Kind         string // "task" | "document"
	MatchKey     string
	Action       Action
	ID           string                  // generated task id or document file uuid
	From, To     string                  // task versions (From empty when added, To empty when removed)
	Title        string                  // task title or document name
	Verification *workspace.Verification // documents only, nil when removed
}

// Item kinds.
const (
	KindTask     = "task"
	KindDocument = "document"
)

// Changes is the result of Plan.
type Changes struct {
	Items []Item           // sorted by Kind, then MatchKey; includes Unchanged
	Files []gitrepo.Change // what to write on a branch based on main (empty when nothing changed)
}

// CompareItems orders items by Kind, then MatchKey, then ID, the order of
// Changes.Items.
func CompareItems(a, b Item) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.MatchKey, b.MatchKey), cmp.Compare(a.ID, b.ID))
}

// Plan matches out against what the answer's task produced earlier (§5.4):
// checks that every named processor is registered in the catalog, that
// origin ids name a catalog task or a generated task of the workspace, and
// the depth limit (ErrTooDeep wrapped); verifies documents with
// Run.Verifier and stores the result in the document's verification field.
func Plan(run Run, out *contract.Output) (*Changes, error) {
	if out == nil {
		out = &contract.Output{}
	}
	if run.NewID == nil {
		run.NewID = uuid.NewString
	}
	if run.Verifier == nil {
		run.Verifier = attest.Unverified
	}
	if err := check(run, out); err != nil {
		return nil, err
	}
	if err := checkDepth(run, out); err != nil {
		return nil, err
	}
	p := &planner{run: run, produced: workspace.ProducedBy{
		Task:         run.Task,
		Processor:    run.Processor,
		Digest:       run.Digest,
		AnswerCommit: run.AnswerCommit,
	}}
	if err := p.tasks(out.Tasks); err != nil {
		return nil, err
	}
	slices.SortFunc(p.items, CompareItems)
	slices.SortStableFunc(p.files, func(a, b gitrepo.Change) int { return cmp.Compare(a.Path, b.Path) })
	return &Changes{Items: p.items, Files: p.files}, nil
}

// planner collects the items and files of one Plan call.
type planner struct {
	run      Run
	produced workspace.ProducedBy
	items    []Item
	files    []gitrepo.Change
}

func (p *planner) write(path string, data []byte) {
	p.files = append(p.files, gitrepo.Change{Path: path, Data: data})
}

func (p *planner) remove(path string) {
	p.files = append(p.files, gitrepo.Change{Path: path, Delete: true})
}

// check reports unknown processors and origins and repeated match keys, all
// in one error wrapping ErrInvalidOutput.
func check(run Run, out *contract.Output) error {
	var msgs []string
	seen := map[string]bool{}
	for _, t := range out.Tasks {
		if seen[t.MatchKey] {
			msgs = append(msgs, fmt.Sprintf("task %q: match_key appears more than once", t.MatchKey))
		}
		seen[t.MatchKey] = true
		if t.Processor != "" {
			if _, ok := run.Catalog.Registry.Processors[t.Processor]; !ok {
				msgs = append(msgs, fmt.Sprintf("task %q: processor %q is not registered in the catalog", t.MatchKey, t.Processor))
			}
		}
		if t.Origin != nil && !run.Catalog.Tasks.Has(t.Origin.ID) && !run.Workspace.Graph.Has(t.Origin.ID) {
			msgs = append(msgs, fmt.Sprintf("task %q: origin %s is neither a catalog task nor a generated task of the workspace", t.MatchKey, t.Origin.ID))
		}
	}
	seen = map[string]bool{}
	for _, d := range out.Documents {
		if seen[d.MatchKey] {
			msgs = append(msgs, fmt.Sprintf("document %q: match_key appears more than once", d.MatchKey))
		}
		seen[d.MatchKey] = true
	}
	if len(msgs) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidOutput, strings.Join(msgs, "; "))
	}
	return nil
}

// checkDepth fails output with tasks when the answered task already lies
// at the maximum depth, and a run on a task that is unknown.
func checkDepth(run Run, out *contract.Output) error {
	depth := 0
	if !run.Catalog.Tasks.Has(run.Task.ID) {
		if depth = Depth(run.Workspace, run.Task.ID); depth < 0 {
			return fmt.Errorf("task %s is neither a catalog task nor a generated task of the workspace", run.Task.ID)
		}
	}
	limit := run.MaxDepth
	if limit <= 0 {
		limit = DefaultMaxDepth
	}
	if len(out.Tasks) > 0 && depth >= limit {
		if depth == math.MaxInt {
			return fmt.Errorf("%w: task %s lies on a cycle of generated tasks", ErrTooDeep, run.Task.ID)
		}
		return fmt.Errorf("%w: task %s is at depth %d, its output tasks would be at depth %d, the maximum is %d",
			ErrTooDeep, run.Task.ID, depth, depth+1, limit)
	}
	return nil
}

// earlierTasks returns the current versions of the generated tasks the
// answered task produced earlier, by match key. When two share a key (only
// possible after hand edits), the one with the smallest id is matched and
// the others are returned in extra, to be removed.
func earlierTasks(w *workspace.Workspace, taskID string) (byKey map[string]*workspace.Generated, extra []*workspace.Generated) {
	byKey = map[string]*workspace.Generated{}
	for _, id := range w.Graph.Tasks() {
		g := Current(w, id)
		if g == nil || g.ProducedBy.Task.ID != taskID {
			continue
		}
		if _, dup := byKey[g.MatchKey]; dup {
			extra = append(extra, g)
			continue
		}
		byKey[g.MatchKey] = g
	}
	return byKey, extra
}
