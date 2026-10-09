package runs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/attest/carabiner"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/proctest"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/runner"
)

// TestSignedDocumentIsVerified runs the sign image with its secret mounted
// from the secrets directory and checks that the proposal's document carries
// the verification result of the service's verifier: verified with the
// signing key trusted, failed without trusted keys (as serve does without
// --trusted-keys).
func TestSignedDocumentIsVerified(t *testing.T) {
	sign := proctest.Image(t, "sign")
	secrets, pub := proctest.SigningKey(t)
	reg := "processors:\n  signer:\n    image: " + sign + "\n    secrets: [" + proctest.SigningKeySecret + "]\n" +
		"bindings:\n  " + fixture.TaskB + ": signer\n"
	keys := t.TempDir()
	if err := os.WriteFile(filepath.Join(keys, "signer.pub"), pub, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, keysDir, want string
	}{
		{"trusted key", keys, attest.StatusVerified},
		{"no trusted keys", "", attest.StatusFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newStoppedEnv(t, reg, Config{})
			v, err := carabiner.New(c.keysDir)
			if err != nil {
				t.Fatal(err)
			}
			e.svc, err = New(e.st, e.bl, runner.New(runner.Config{SecretsDir: secrets}), v, Config{})
			if err != nil {
				t.Fatal(err)
			}
			e.st.OnMainMoved(e.svc.Scan)
			e.svc.Start(t.Context())
			e.commit(t, ws, map[string]string{answerB: markdownAnswer("doc slsa web-01\n")})
			wantRun(t, e.runs(t, ws)[0], Succeeded, Proposed, ReasonAnswer)
			p, err := proposal.Get(e.st, ws, fixture.TaskB)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Items) != 1 || p.Items[0].Kind != match.KindDocument || p.Items[0].Verification == nil ||
				p.Items[0].Verification.Status != c.want {
				t.Fatalf("items %+v, want one document with status %s", p.Items, c.want)
			}
			if c.want == attest.StatusVerified && len(p.Items[0].Verification.Signers) == 0 {
				t.Error("a verified document must name its signer")
			}
		})
	}
}
