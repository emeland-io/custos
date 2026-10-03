package api

import (
	"net/http"

	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// stillValid confirms an existing answer for the task's current version: it
// writes the same value, body and attachments with the new task_version
// (spec §4.2). The store's validation catches a value the new version no
// longer allows, such as a removed choice.
func (a *API) stillValid(w http.ResponseWriter, r *http.Request) {
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
	ans, commit, err := a.updateAnswer(id, tid, author, "Confirm the answer to task "+tid+" as still valid",
		func(current *task.Version, _ *task.Graph, old *workspace.Answer) (*workspace.Answer, error) {
			if old == nil {
				return nil, errorf(http.StatusNotFound, "workspace %s has no answer to task %s", id, tid)
			}
			if old.Type != current.AnswerType {
				return nil, rejected(tid, "version %s of task %s asks for a %s answer, but the answer is %s; write a new answer instead",
					current.Version, tid, current.AnswerType, old.Type)
			}
			return &workspace.Answer{
				Task: tid, TaskVersion: current.Version, Type: current.AnswerType,
				Value: old.Value, Attachments: old.Attachments, Body: old.Body,
			}, nil
		})
	if err != nil {
		WriteError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, answerResponse{answerJSON: a.answerToJSON(ans), Commit: commit})
}
