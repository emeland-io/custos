package api

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

type answerRequest struct {
	TaskVersion string           `json:"task_version"` // default: the current version
	Value       string           `json:"value"`
	Body        string           `json:"body"`
	Attachments []attachmentJSON `json:"attachments"`
}

// answerResponse is the answer as written, and the commit main points to.
type answerResponse struct {
	answerJSON
	Commit string `json:"commit"`
}

// answerEdit computes the answer to write from the task's current version,
// the graph that holds the task (catalog or generated tasks), and the
// existing answer file (nil when there is none).
type answerEdit func(current *task.Version, graph *task.Graph, old *workspace.Answer) (*workspace.Answer, error)

// putAnswer writes an answer as one commit on main (spec §4.2). Value,
// type, attachment and catalog rules are not checked here: the store
// validates main like a push and reports problems as *store.RejectedError.
func (a *API) putAnswer(w http.ResponseWriter, r *http.Request) {
	author, err := Author(r)
	if err != nil {
		WriteError(w, err)
		return
	}
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
	var req answerRequest
	if err := decodeJSON(w, r, &req, false); err != nil {
		WriteError(w, err)
		return
	}
	if err := a.checkBlobs(tid, req.Attachments); err != nil {
		WriteError(w, err)
		return
	}
	ans, commit, err := a.updateAnswer(id, tid, author, "Answer task "+tid,
		func(current *task.Version, g *task.Graph, _ *workspace.Answer) (*workspace.Answer, error) {
			v := current
			if req.TaskVersion != "" {
				var ok bool
				if v, ok = g.Lookup(task.Ref{ID: tid, Version: req.TaskVersion}); !ok {
					return nil, rejected(tid, "task %s has no version %q", tid, req.TaskVersion)
				}
			}
			ans := &workspace.Answer{Task: tid, TaskVersion: v.Version, Type: v.AnswerType, Value: req.Value, Body: normalizeBody(req.Body)}
			for _, at := range req.Attachments {
				ans.Attachments = append(ans.Attachments, workspace.Attachment{Name: at.Name, SHA256: at.SHA256, MediaType: at.MediaType})
			}
			return ans, nil
		})
	if err != nil {
		WriteError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, answerResponse{answerJSON: a.answerToJSON(ans), Commit: commit})
}

// updateAnswer writes the answer edit returns to main of workspace id, under
// the workspace's lock: the tree and the pinned catalog are read inside the
// store's edit callback, so the current version cannot change underneath.
// Writing the same content again makes no commit and returns main.
func (a *API) updateAnswer(id, tid string, author gitrepo.Signature, message string, edit answerEdit) (*workspace.Answer, string, error) {
	var written *workspace.Answer
	var editErr error // the callback's own error, whatever the store wraps it in
	commit, err := a.st.UpdateWorkspace(id, mainRef, author, message, func(tree fs.FS) ([]gitrepo.Change, error) {
		var changes []gitrepo.Change
		written, changes, editErr = a.answerChange(id, tid, tree, edit)
		return changes, editErr
	})
	if editErr != nil {
		return nil, "", editErr
	}
	if err != nil {
		return nil, "", err
	}
	return written, commit, nil
}

func (a *API) answerChange(id, tid string, tree fs.FS, edit answerEdit) (*workspace.Answer, []gitrepo.Change, error) {
	ws, _ := workspace.Load(tree) // main is valid; the store re-validates the result
	if ws.Config.Catalog.Commit == "" {
		return nil, nil, errorf(http.StatusConflict, "workspace %s has no main yet", id)
	}
	c, err := a.catalogAt(ws.Config.Catalog.Commit)
	if err != nil {
		return nil, nil, err
	}
	g := taskGraph(ws, c, tid)
	if g == nil {
		return nil, nil, errorf(http.StatusNotFound,
			"task %s is neither in the pinned catalog nor a generated task of workspace %s", tid, id)
	}
	current, ok := g.Current(tid)
	if !ok {
		if g.Superseded(tid) {
			return nil, nil, rejected(tid, "task %s was merged into another task; answer that task instead", tid)
		}
		return nil, nil, rejected(tid, "task %s has no single current version", tid)
	}
	path := answerPath(tid)
	ans, err := edit(current, g, ws.Answers[path])
	if err != nil {
		return nil, nil, err
	}
	ans.Path = path
	data, err := frontmatter.Encode(ans, ans.Body)
	if err != nil {
		return nil, nil, err
	}
	if old, err := fs.ReadFile(tree, path); err == nil && bytes.Equal(old, data) {
		return ans, nil, nil
	}
	return ans, []gitrepo.Change{{Path: path, Data: data}}, nil
}

// taskGraph returns the graph that holds task id: the pinned catalog's, or
// the workspace's generated tasks; nil when neither knows the task.
func taskGraph(ws *workspace.Workspace, c *catalog.Catalog, id string) *task.Graph {
	if c.Tasks.Has(id) {
		return c.Tasks
	}
	if ws.Graph != nil && ws.Graph.Has(id) {
		return ws.Graph
	}
	return nil
}

// checkBlobs refuses attachments whose blob is not on the server, so that
// the API never creates a dangling reference. Malformed hashes are left to
// the workspace rules, which the store applies.
func (a *API) checkBlobs(tid string, ats []attachmentJSON) error {
	var ps problem.List
	for _, at := range ats {
		if blobs.ValidSHA(at.SHA256) && !a.bl.Has(at.SHA256) {
			ps.Add(answerPath(tid), problem.RuleAnswer,
				"attachment %q: blob %s is not on the server; upload it with POST /api/blobs first", at.Name, at.SHA256)
		}
	}
	if len(ps) > 0 {
		return &store.RejectedError{Problems: ps}
	}
	return nil
}

// rejected is a validation problem of the answer to task tid.
func rejected(tid, format string, args ...any) error {
	return &store.RejectedError{Problems: []problem.Problem{{
		Path: answerPath(tid), Rule: problem.RuleAnswer, Message: fmt.Sprintf(format, args...),
	}}}
}

// normalizeBody returns the body as reading the file back yields it: "\n"
// line endings and a final newline (frontmatter.Split). The final newline
// is added before folding "\r\n" to "\n", so a body ending in a lone "\r"
// (no trailing "\n" at all) comes out as "...\n", not "...\r\n" — matching
// what Split would read back from the file, where the "\r\n" it just
// became is itself folded. Without this, saving the same answer twice
// would make a second commit.
func normalizeBody(s string) string {
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}
