// Package runs queues and executes processor runs (spec §4.3, §5): it finds
// the answers of bound tasks that have not been run yet, runs their
// processor through the runner, matches the output and writes it as a
// proposal branch. Run records are files in the data directory, not Git:
//
//	<data-dir>/runs/<workspace-id>/<run-id>.json   the record
//	<data-dir>/runs/<workspace-id>/<run-id>.log    the processor's stderr
package runs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emeland-io/custos/internal/task"
)

// State is where a run is in its life.
type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Succeeded State = "succeeded"
	Failed    State = "failed"
)

// Outcome is what a succeeded run produced.
type Outcome string

const (
	Proposed  Outcome = "proposed"  // a proposal branch was written
	Unchanged Outcome = "unchanged" // the output matches main; open proposals of the task were closed
	None      Outcome = ""          // queued, running or failed
)

// Reasons a run was queued.
const (
	ReasonAnswer  = "answer"   // a new or changed answer
	ReasonDigest  = "digest"   // the processor's image digest changed
	ReasonBinding = "binding"  // the task was bound to another processor
	ReasonRetry   = "retry"    // an engineer retried a failed run
	ReasonMerge   = "merge"    // a merge took main's side of generated output
	ReasonStartUp = "start-up" // found by the scan at start-up, with no earlier run of the answer
)

// Record is one run. Once a run starts, the answer, processor and digest
// fields show what it actually used.
type Record struct {
	ID           string    `json:"id"`
	Workspace    string    `json:"workspace"`
	Task         task.Ref  `json:"task"` // answered task id and the answer's task_version
	AnswerPath   string    `json:"answer_path"`
	AnswerBlob   string    `json:"answer_blob"` // git blob oid of the answer file
	AnswerCommit string    `json:"answer_commit"`
	Processor    string    `json:"processor"`
	Image        string    `json:"image"`
	Digest       string    `json:"digest"`
	Key          string    `json:"key"` // see runKey; a key with a record is not run again by Scan
	Reason       string    `json:"reason"`
	RetryOf      string    `json:"retry_of,omitempty"`
	State        State     `json:"state"`
	Outcome      Outcome   `json:"outcome,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	Error        string    `json:"error,omitempty"`
	Queued       time.Time `json:"queued"`
	Started      time.Time `json:"started,omitzero"`
	Finished     time.Time `json:"finished,omitzero"`
}

// Config tunes a Service.
type Config struct {
	Workers  int // runs executed at the same time; default 2
	MaxDepth int // maximum depth of generated tasks; default 8
}

// runKey identifies the work of a run: the hex SHA-256 of the workspace
// id, the answer path, the answer's blob oid and the processor digest,
// separated by newlines (none of them can contain one).
func runKey(workspaceID, answerPath, answerBlob, digest string) string {
	sum := sha256.Sum256([]byte(workspaceID + "\n" + answerPath + "\n" + answerBlob + "\n" + digest))
	return hex.EncodeToString(sum[:])
}

// files keeps records and logs below one directory.
type files struct{ dir string } // <data-dir>/runs

func (f files) path(r *Record, ext string) string {
	return filepath.Join(f.dir, r.Workspace, r.ID+ext)
}

// save writes the record atomically: a reader sees the old or the new
// file, never a partial one.
func (f files) save(r *Record) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(f.path(r, ".json"), append(data, '\n'))
}

// saveLog writes the run's log.
func (f files) saveLog(r *Record, log []byte) error {
	return writeAtomic(f.path(r, ".log"), log)
}

// log reads the run's log; a missing log is empty.
func (f files) log(r *Record) ([]byte, error) {
	data, err := os.ReadFile(f.path(r, ".log"))
	if errors.Is(err, fs.ErrNotExist) {
		return []byte{}, nil
	}
	return data, err
}

// load reads every record. Files that cannot be read as a record of the
// workspace directory they are in are skipped: one damaged file must not
// keep the server from starting, and the worst outcome is that its answer
// runs once more.
func (f files) load() ([]*Record, error) {
	wss, err := os.ReadDir(f.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Record
	for _, ws := range wss {
		if !ws.IsDir() || !task.ValidID(ws.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(f.dir, ws.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			id, ok := strings.CutSuffix(e.Name(), ".json")
			if !ok || !task.ValidID(id) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(f.dir, ws.Name(), e.Name()))
			if err != nil {
				return nil, err
			}
			var r Record
			if json.Unmarshal(data, &r) != nil || r.ID != id || r.Workspace != ws.Name() {
				continue
			}
			out = append(out, &r)
		}
	}
	return out, nil
}

// writeAtomic writes data to a temporary file next to path and renames it.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
