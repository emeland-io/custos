package match_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/attest/carabiner"
	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

// TestPlanVerifiesSignedDocumentFromRealProcessor runs the sign test image
// (plan 3a) in Docker, verifies its DSSE envelope with the matching public
// key through the carabiner verifier (plan 3b), and expects the document
// to be stored as verified. With another key it must be failed.
func TestPlanVerifiesSignedDocumentFromRealProcessor(t *testing.T) {
	secrets, pub := proctest.SigningKey(t)
	_, otherPub := proctest.SigningKey(t)
	image := proctest.Image(t, "sign")

	w, _ := workspace.Load(fixture.MapFS(map[string]string{"custos.yaml": fixture.Config(fixture.WorkspaceID, fixture.Commit)}))
	c, ps := catalog.Load(fixture.MapFS(fixture.Catalog()))
	fixture.WantNone(t, ps)
	v, ok := c.Tasks.Lookup(task.Ref{ID: fixture.TaskB, Version: "1.0.0"})
	if !ok {
		t.Fatal("fixture catalog lacks TaskB 1.0.0")
	}
	answer := &workspace.Answer{Task: fixture.TaskB, TaskVersion: "1.0.0", Type: task.AnswerText,
		Value: "doc provenance web-01", Path: "answers/" + fixture.TaskB + ".md"}
	input, err := json.Marshal(contract.NewInput(fixture.WorkspaceID, v, answer))
	if err != nil {
		t.Fatal(err)
	}

	rn := runner.New(runner.Config{SecretsDir: secrets})
	ctx := context.Background()
	ref, digest, err := rn.Resolve(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	res, err := rn.Run(ctx, runner.Job{Image: ref, Secrets: []string{"test-signing-key"}, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.TimedOut {
		t.Fatalf("sign exited %d (timed out %v):\n%s", res.ExitCode, res.TimedOut, res.Log)
	}
	out, err := contract.ParseOutput(res.Stdout)
	if err != nil {
		t.Fatal(err)
	}

	for _, c2 := range []struct {
		name string
		key  []byte
		want string
	}{
		{"trusted key", pub, attest.StatusVerified},
		{"other key", otherPub, attest.StatusFailed},
	} {
		t.Run(c2.name, func(t *testing.T) {
			keys := t.TempDir()
			if err := os.WriteFile(filepath.Join(keys, "test.pub"), c2.key, 0o600); err != nil {
				t.Fatal(err)
			}
			verifier, err := carabiner.New(keys)
			if err != nil {
				t.Fatal(err)
			}
			ch, err := match.Plan(match.Run{
				Workspace: w, Catalog: c, Task: task.Ref{ID: fixture.TaskB, Version: "1.0.0"},
				Processor: "host-scanner", Digest: digest, AnswerCommit: fixture.Commit, Verifier: verifier,
			}, out)
			if err != nil {
				t.Fatal(err)
			}
			if len(ch.Items) != 1 || ch.Items[0].Kind != match.KindDocument || ch.Items[0].Verification == nil {
				t.Fatalf("items %+v", ch.Items)
			}
			if got := ch.Items[0].Verification.Status; got != c2.want {
				t.Errorf("status %q, want %q", got, c2.want)
			}
			after, _ := workspace.Load(fixture.MapFS(map[string]string{
				"custos.yaml":    fixture.Config(fixture.WorkspaceID, fixture.Commit),
				ch.Files[0].Path: string(ch.Files[0].Data),
			}))
			for _, d := range after.Documents {
				if d.Verification == nil || d.Verification.Status != c2.want {
					t.Errorf("stored verification %+v, want %s", d.Verification, c2.want)
				}
			}
		})
	}
}
