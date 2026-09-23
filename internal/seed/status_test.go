package seed

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/emeland-io/custos/internal/model"
	"github.com/emeland-io/custos/internal/store"
)

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func TestCompute(t *testing.T) {
	rootDir, workDir := t.TempDir(), t.TempDir()
	open := func() *store.Store {
		return must[*store.Store](t)(store.Open(rootDir, workDir, slog.New(slog.NewTextHandler(io.Discard, nil))))
	}
	s := open()

	entry := must[model.Node](t)(s.CreateNode(model.Node{DisplayName: "entry"}))
	child := must[model.Node](t)(s.CreateNode(model.Node{DisplayName: "child", ParentID: &entry.ID}))
	leaf1 := must[model.Leaf](t)(s.CreateLeaf(model.Leaf{ParentID: entry.ID}))
	leaf2 := must[model.Leaf](t)(s.CreateLeaf(model.Leaf{ParentID: child.ID}))
	root := must[model.Root](t)(s.CreateRoot(model.Root{DisplayName: "root", EntryNodeID: entry.ID}))
	sd := must[model.Seed](t)(s.CreateSeed(model.Seed{DisplayName: "run", RootID: root.ID}))

	st := must[Status](t)(Compute(s, sd.ID))
	if len(st.MissingShoots) != 2 || len(st.MissingAttestations) != 2 || st.Percent != 0 {
		t.Fatalf("empty seed: %+v", st)
	}
	if p := st.MissingShoots[1].Path; len(p) != 2 || p[1].ID != child.ID {
		t.Errorf("path of leaf2: %+v", p)
	}

	// Answer leaf1, attest child with a met and entry with a failed attestation.
	must[model.Shoot](t)(s.CreateShoot(model.Shoot{SeedID: sd.ID, LeafID: leaf1.ID}))
	must[model.Attestation](t)(s.CreateAttestation(model.Attestation{SeedID: sd.ID, NodeID: child.ID,
		Verification: model.Verification{Status: model.StatusVerified, RequirementMet: true}}))
	must[model.Attestation](t)(s.CreateAttestation(model.Attestation{SeedID: sd.ID, NodeID: entry.ID,
		Verification: model.Verification{Status: model.StatusFailed}}))

	st = must[Status](t)(Compute(s, sd.ID))
	if st.AnsweredLeaves != 1 || st.AttestedNodes != 1 || st.Percent != 50 {
		t.Errorf("counts: %+v", st)
	}
	if len(st.MissingAttestations) != 1 || st.MissingAttestations[0].Node.ID != entry.ID || len(st.MissingAttestations[0].Rejected) != 1 {
		t.Errorf("missing attestations: %+v", st.MissingAttestations)
	}
	if len(st.MissingShoots) != 1 || st.MissingShoots[0].Leaf.ID != leaf2.ID {
		t.Errorf("missing shoots: %+v", st.MissingShoots)
	}

	// New versions make the answer and the attestation stale.
	leaf1v2 := must[model.Leaf](t)(s.NewLeafVersion(leaf1.ID, model.Leaf{ParentID: entry.ID, Versioned: model.Versioned{Version: "2"}}))
	childV2 := must[model.Node](t)(s.NewNodeVersion(child.ID, model.Node{DisplayName: "child", ParentID: &entry.ID, Versioned: model.Versioned{Version: "2"}}))
	st = must[Status](t)(Compute(s, sd.ID))
	if st.AnsweredLeaves != 0 || st.AttestedNodes != 0 {
		t.Errorf("counts after new versions: %+v", st)
	}
	for _, m := range st.MissingShoots {
		if m.Leaf.ID == leaf1v2.ID && len(m.Stale) != 1 {
			t.Errorf("leaf1 v2 stale shoots: %+v", m.Stale)
		}
	}
	for _, m := range st.MissingAttestations {
		if m.Node.ID == childV2.ID && len(m.Stale) != 1 {
			t.Errorf("child v2 stale attestations: %+v", m.Stale)
		}
	}

	// Removing the answered leaf version from disk orphans the shoot.
	if err := os.Remove(filepath.Join(rootDir, "leaves", leaf1.ID.String()+".json")); err != nil {
		t.Fatal(err)
	}
	st = must[Status](t)(Compute(open(), sd.ID))
	if len(st.Orphaned) != 1 || st.Orphaned[0].Kind != "shoot" {
		t.Errorf("orphaned: %+v", st.Orphaned)
	}
}
