package status

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	ghtml "github.com/yuin/goldmark/renderer/html"

	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// md converts a Markdown-shaped answer body to an HTML fragment. GFM adds
// tables, strikethrough, autolinking and task lists; WithUnsafe lets a
// Markdown body embed raw HTML (accepted for this project's scale and trust
// model — see the plan's "Decisions beyond the architecture note").
var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(ghtml.WithUnsafe()),
)

const htmlStyle = `
body { font-family: -apple-system, Segoe UI, Roboto, sans-serif; max-width: 48rem; margin: 2rem auto; padding: 0 1rem; line-height: 1.5; color: #1a1a1a; }
h1, h2, h3, h4, h5, h6 { margin-top: 1.6em; }
code, pre { background: #f2f2f2; border-radius: 4px; }
pre { padding: 0.75em; overflow-x: auto; }
code { padding: 0.15em 0.35em; }
.state { color: #555; font-style: italic; }
ul.attachments { color: #555; }
table { border-collapse: collapse; }
th, td { border: 1px solid #ccc; padding: 0.3em 0.6em; }
`

// RenderHTML renders the same book as RenderMarkdown, as a complete,
// self-contained HTML document (inline <style>, no external requests).
func RenderHTML(s *Status) []byte {
	var b htmlBuilder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	fmt.Fprintf(&b, "<title>Workspace %s</title>\n", html.EscapeString(s.Workspace))
	fmt.Fprintf(&b, "<style>%s</style>\n</head>\n<body>\n", htmlStyle)
	fmt.Fprintf(&b, "<h1>Workspace %s</h1>\n", html.EscapeString(s.Workspace))
	fmt.Fprintf(&b, "<p>Catalog commit <code>%s</code>.</p>\n", html.EscapeString(s.Pin))
	for _, en := range s.Book {
		switch {
		case en.Group != nil:
			b.heading(en.Depth, en.Group.Title)
		case en.Task != nil:
			b.task(en.Depth, en.Task)
		}
	}
	b.WriteString("</body>\n</html>\n")
	return b.Bytes()
}

type htmlBuilder struct{ bytes.Buffer }

// level returns the heading tag for depth, the same clamp RenderMarkdown
// uses (document title at depth-equivalent 0 is <h1>, everything below
// clamps at <h6>; groups/tasks start one level below the title).
func level(depth int) int { return min(depth+2, 6) }

func (b *htmlBuilder) heading(depth int, title string) {
	fmt.Fprintf(b, "<h%d>%s</h%d>\n", level(depth), html.EscapeString(title), level(depth))
}

func (b *htmlBuilder) task(depth int, ts *TaskStatus) {
	b.heading(depth, ts.Title)
	switch ts.State {
	case Unanswered:
		b.WriteString("<p class=\"state\">Unanswered.</p>\n")
		return
	case PendingUpdate:
		if ts.Answer != nil {
			fmt.Fprintf(b, "<p class=\"state\">Pending update: answered for version %s; the current version is %s.</p>\n",
				html.EscapeString(ts.Answer.TaskVersion), html.EscapeString(ts.Current.Version))
		} else {
			fmt.Fprintf(b, "<p class=\"state\">Pending update: version %s is not answered yet; the answers of the tasks it merged stay in effect.</p>\n",
				html.EscapeString(ts.Current.Version))
		}
	}
	for _, a := range ts.InEffect {
		if a != ts.Answer {
			fmt.Fprintf(b, "<p class=\"state\">Answer to merged task %s, version %s:</p>\n", html.EscapeString(a.Task), html.EscapeString(a.TaskVersion))
		}
		b.answer(a)
	}
}

func (b *htmlBuilder) answer(a *workspace.Answer) {
	if a.Type == task.AnswerMarkdown {
		if strings.TrimSpace(a.Body) != "" {
			md.Convert([]byte(a.Body), b)
			b.WriteString("\n")
		}
	} else if a.Value != "" {
		fmt.Fprintf(b, "<p>%s</p>\n", html.EscapeString(a.Value))
	}
	if len(a.Attachments) == 0 {
		return
	}
	b.WriteString("<ul class=\"attachments\">\n")
	for _, at := range a.Attachments {
		fmt.Fprintf(b, "<li>Attachment %s (%s, sha256 %s)</li>\n",
			html.EscapeString(at.Name), html.EscapeString(at.MediaType), html.EscapeString(at.SHA256))
	}
	b.WriteString("</ul>\n")
}
