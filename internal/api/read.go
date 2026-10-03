package api

import (
	"net/http"

	"github.com/emeland-io/custos/internal/status"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// attachmentJSON is one attachment of an answer. Available tells whether
// the blob is on the server (spec §7: "attachment unavailable"); it is
// ignored in requests.
type attachmentJSON struct {
	Name      string `json:"name"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	Available bool   `json:"available"`
}

type answerJSON struct {
	Task        string           `json:"task"`
	TaskVersion string           `json:"task_version"`
	Type        task.AnswerType  `json:"type"`
	Value       string           `json:"value"`
	Body        string           `json:"body"`
	Attachments []attachmentJSON `json:"attachments"`
}

type taskStatusJSON struct {
	ID         string          `json:"id"`
	Version    string          `json:"version"` // current version
	Title      string          `json:"title"`
	AnswerType task.AnswerType `json:"answer_type"`
	Generated  bool            `json:"generated"`
	State      status.State    `json:"state"`
	Answer     *answerJSON     `json:"answer"`    // the task's own answer file, any version
	Merged     []answerJSON    `json:"merged"`    // answers of tasks merged into this one, for reference
	InEffect   []answerJSON    `json:"in_effect"` // what the book shows
}

type bookGroupJSON struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// bookEntryJSON is one line of the book: a group heading or a task id.
type bookEntryJSON struct {
	Depth int            `json:"depth"`
	Group *bookGroupJSON `json:"group,omitempty"`
	Task  string         `json:"task,omitempty"`
}

type statusJSON struct {
	Workspace string           `json:"workspace"`
	Pin       string           `json:"pin"`
	Tasks     []taskStatusJSON `json:"tasks"` // book order
	Book      []bookEntryJSON  `json:"book"`
}

func (a *API) answerToJSON(ans *workspace.Answer) answerJSON {
	out := answerJSON{
		Task: ans.Task, TaskVersion: ans.TaskVersion, Type: ans.Type, Value: ans.Value, Body: ans.Body,
		Attachments: []attachmentJSON{},
	}
	for _, at := range ans.Attachments {
		out.Attachments = append(out.Attachments, attachmentJSON{
			Name: at.Name, SHA256: at.SHA256, MediaType: at.MediaType, Available: a.bl.Has(at.SHA256),
		})
	}
	return out
}

func (a *API) answersToJSON(as []*workspace.Answer) []answerJSON {
	out := []answerJSON{}
	for _, ans := range as {
		out = append(out, a.answerToJSON(ans))
	}
	return out
}

func (a *API) statusToJSON(s *status.Status) statusJSON {
	out := statusJSON{Workspace: s.Workspace, Pin: s.Pin, Tasks: []taskStatusJSON{}, Book: []bookEntryJSON{}}
	for _, ts := range s.Tasks {
		tj := taskStatusJSON{
			ID: ts.ID, Version: ts.Current.Version, Title: ts.Title, AnswerType: ts.AnswerType,
			Generated: ts.Generated, State: ts.State,
			Merged: a.answersToJSON(ts.Merged), InEffect: a.answersToJSON(ts.InEffect),
		}
		if ts.Answer != nil {
			aj := a.answerToJSON(ts.Answer)
			tj.Answer = &aj
		}
		out.Tasks = append(out.Tasks, tj)
	}
	for _, entry := range s.Book {
		be := bookEntryJSON{Depth: entry.Depth}
		if entry.Group != nil {
			be.Group = &bookGroupJSON{Slug: entry.Group.Slug, Title: entry.Group.Title}
		}
		if entry.Task != nil {
			be.Task = entry.Task.ID
		}
		out.Book = append(out.Book, be)
	}
	return out
}

// workspaceStatus computes the status of the workspace named in the path.
func (a *API) workspaceStatus(r *http.Request) (*status.Status, error) {
	id, err := workspaceID(r)
	if err != nil {
		return nil, err
	}
	return a.st.Status(id)
}

func (a *API) getStatus(w http.ResponseWriter, r *http.Request) {
	s, err := a.workspaceStatus(r)
	if err != nil {
		WriteError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, a.statusToJSON(s))
}

// getBook serves the book as Markdown (ruling 2.11).
func (a *API) getBook(w http.ResponseWriter, r *http.Request) {
	s, err := a.workspaceStatus(r)
	if err != nil {
		WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(status.RenderMarkdown(s))
}

func (a *API) getAnswer(w http.ResponseWriter, r *http.Request) {
	id, err := workspaceID(r)
	if err != nil {
		WriteError(w, err)
		return
	}
	tid, err := taskID(r)
	if err != nil {
		WriteError(w, err)
		return
	}
	ws, _, err := a.st.Load(id, "")
	if err != nil {
		WriteError(w, err)
		return
	}
	ans, ok := ws.Answers[answerPath(tid)]
	if !ok {
		WriteError(w, errorf(http.StatusNotFound, "workspace %s has no answer to task %s", id, tid))
		return
	}
	WriteJSON(w, http.StatusOK, a.answerToJSON(ans))
}
