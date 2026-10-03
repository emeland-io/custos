package status

import (
	"fmt"
	"strings"

	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// RenderMarkdown writes the book of s as one Markdown document: a heading
// per group and task, nested by depth, and the answers in effect below each
// task.
func RenderMarkdown(s *Status) []byte {
	var md markdown
	md.block("# Workspace " + s.Workspace)
	md.block("Catalog commit `" + s.Pin + "`.")
	for _, en := range s.Book {
		switch {
		case en.Group != nil:
			md.heading(en.Depth, en.Group.Title)
		case en.Task != nil:
			md.task(en.Depth, en.Task)
		}
	}
	return []byte(strings.TrimRight(md.String(), "\n") + "\n")
}

type markdown struct{ strings.Builder }

// block writes one paragraph, list or heading, followed by a blank line.
func (md *markdown) block(s string) {
	md.WriteString(strings.TrimRight(s, "\n"))
	md.WriteString("\n\n")
}

// heading writes a heading for depth: groups and tasks at depth 0 get "##",
// the document title being "#"; Markdown has no level beyond 6.
func (md *markdown) heading(depth int, title string) {
	md.block(strings.Repeat("#", min(depth+2, 6)) + " " + title)
}

func (md *markdown) task(depth int, ts *TaskStatus) {
	md.heading(depth, ts.Title)
	switch ts.State {
	case Unanswered:
		md.block("*Unanswered.*")
		return
	case PendingUpdate:
		if ts.Answer != nil {
			md.block(fmt.Sprintf("*Pending update: answered for version %s; the current version is %s.*", ts.Answer.TaskVersion, ts.Current.Version))
		} else {
			md.block(fmt.Sprintf("*Pending update: version %s is not answered yet; the answers of the tasks it merged stay in effect.*", ts.Current.Version))
		}
	}
	for _, a := range ts.InEffect {
		if a != ts.Answer {
			md.block(fmt.Sprintf("*Answer to merged task %s, version %s:*", a.Task, a.TaskVersion))
		}
		md.answer(a)
	}
}

func (md *markdown) answer(a *workspace.Answer) {
	if a.Type == task.AnswerMarkdown {
		if strings.TrimSpace(a.Body) != "" {
			md.block(a.Body)
		}
	} else {
		md.block(a.Value)
	}
	if len(a.Attachments) == 0 {
		return
	}
	var list strings.Builder
	for _, at := range a.Attachments {
		fmt.Fprintf(&list, "- Attachment %s (%s, sha256 %s)\n", at.Name, at.MediaType, at.SHA256)
	}
	md.block(list.String())
}
