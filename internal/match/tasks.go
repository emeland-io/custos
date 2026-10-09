package match

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/semver"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// firstVersion is the version of a new generated task.
const firstVersion = "1.0.0"

// generatedFile is the frontmatter of a generated task version as custos
// writes it. It lists the fields of workspace.GeneratedMeta flat, because
// frontmatter.Encode's quoting fallback cannot encode an inlined struct.
type generatedFile struct {
	ID         string               `yaml:"id"`
	Version    string               `yaml:"version"`
	Title      string               `yaml:"title"`
	AnswerType task.AnswerType      `yaml:"answer_type"`
	Choices    []string             `yaml:"choices,omitempty"`
	Previous   []task.Ref           `yaml:"previous,omitempty"`
	Origin     *workspace.Origin    `yaml:"origin,omitempty"`
	MatchKey   string               `yaml:"match_key"`
	Processor  string               `yaml:"processor,omitempty"`
	ProducedBy workspace.ProducedBy `yaml:"produced_by"`
}

// tasks matches the output tasks against the generated tasks the answered
// task produced earlier.
func (p *planner) tasks(out []contract.OutputTask) error {
	w := p.run.Workspace
	earlier, extra := earlierTasks(w, p.run.Task.ID)
	for _, t := range out {
		old, found := earlier[t.MatchKey]
		delete(earlier, t.MatchKey)
		body := normalizeBody(t.Body)
		switch {
		case !found:
			id := p.run.NewID()
			if err := p.writeTask(id, firstVersion, nil, t, body); err != nil {
				return err
			}
			p.items = append(p.items, Item{Kind: KindTask, MatchKey: t.MatchKey, Action: Added, ID: id, To: firstVersion, Title: t.Title})
		case sameTask(old, t, body):
			p.items = append(p.items, Item{Kind: KindTask, MatchKey: t.MatchKey, Action: Unchanged, ID: old.ID,
				From: old.Version, To: old.Version, Title: t.Title})
		default:
			step := t.Bump
			if step == "" {
				step = semver.Minor
			}
			next, err := semver.Bump(old.Version, step)
			if err != nil {
				return fmt.Errorf("task %q: %w", t.MatchKey, err)
			}
			if _, exists := w.Graph.Lookup(task.Ref{ID: old.ID, Version: next}); exists {
				return fmt.Errorf("task %q: version %s of generated task %s already exists; use a larger bump", t.MatchKey, next, old.ID)
			}
			if err := p.writeTask(old.ID, next, []task.Ref{old.Ref()}, t, body); err != nil {
				return err
			}
			p.items = append(p.items, Item{Kind: KindTask, MatchKey: t.MatchKey, Action: NewVersion, ID: old.ID,
				From: old.Version, To: next, Title: t.Title})
		}
	}
	gone := slices.Collect(maps.Values(earlier))
	for _, g := range append(gone, extra...) {
		p.removeTask(g)
	}
	return nil
}

// writeTask adds the file of version of generated task id.
func (p *planner) writeTask(id, version string, previous []task.Ref, t contract.OutputTask, body string) error {
	f := generatedFile{
		ID: id, Version: version, Title: t.Title, AnswerType: t.AnswerType, Choices: t.Choices, Previous: previous,
		Origin: origin(t.Origin), MatchKey: t.MatchKey, Processor: t.Processor, ProducedBy: p.produced,
	}
	data, err := frontmatter.Encode(f, body)
	if err != nil {
		return fmt.Errorf("task %q: %w", t.MatchKey, err)
	}
	p.write("generated/"+id+"/"+version+".md", data)
	return nil
}

// removeTask deletes every version file of generated task g and its answer
// (answers stay in Git history, §5.5).
func (p *planner) removeTask(g *workspace.Generated) {
	w := p.run.Workspace
	for _, v := range w.Graph.Versions(g.ID) {
		p.remove(v.Path)
	}
	if answer := "answers/" + g.ID + ".md"; w.Answers[answer] != nil {
		p.remove(answer)
	}
	p.items = append(p.items, Item{Kind: KindTask, MatchKey: g.MatchKey, Action: Removed, ID: g.ID, From: g.Version, Title: g.Title})
}

// sameTask reports whether output t has the content of generated task g:
// title, body, answer_type, choices, origin and processor (bump is not
// content). body is t.Body normalised.
func sameTask(g *workspace.Generated, t contract.OutputTask, body string) bool {
	return g.Title == t.Title && g.Body == body && g.AnswerType == t.AnswerType &&
		slices.Equal(g.Choices, t.Choices) && sameOrigin(g.Origin, origin(t.Origin)) && g.Processor == t.Processor
}

func origin(o *contract.Origin) *workspace.Origin {
	if o == nil {
		return nil
	}
	return &workspace.Origin{ID: o.ID, Version: o.Version}
}

func sameOrigin(a, b *workspace.Origin) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// normalizeBody returns body as a task file stores it: "\n" line endings and
// a final newline unless it is empty, which is what reading the file back
// yields (frontmatter.Split).
func normalizeBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body
}
