package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/model"
)

type fixture struct {
	t                *testing.T
	rootDir, workDir string
	s                *Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, rootDir: filepath.Join(t.TempDir(), "root"), workDir: filepath.Join(t.TempDir(), "work")}
	f.reopen()
	return f
}

func (f *fixture) reopen() {
	f.t.Helper()
	s, err := Open(f.rootDir, f.workDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		f.t.Fatal(err)
	}
	f.s = s
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

// tree builds entry -> {child -> leaf2, leaf1} with a root at entry.
type tree struct {
	entry, child model.Node
	leaf1, leaf2 model.Leaf
	root         model.Root
}

func (f *fixture) tree() tree {
	f.t.Helper()
	var tr tree
	tr.entry = must[model.Node](f.t)(f.s.CreateNode(model.Node{DisplayName: "entry", RequiredAttestation: model.RequiredAttestation{PredicateType: "p"}}))
	tr.child = must[model.Node](f.t)(f.s.CreateNode(model.Node{DisplayName: "child", ParentID: &tr.entry.ID}))
	tr.leaf1 = must[model.Leaf](f.t)(f.s.CreateLeaf(model.Leaf{ParentID: tr.entry.ID, Description: "# task 1"}))
	tr.leaf2 = must[model.Leaf](f.t)(f.s.CreateLeaf(model.Leaf{ParentID: tr.child.ID, Description: "task 2"}))
	tr.root = must[model.Root](f.t)(f.s.CreateRoot(model.Root{DisplayName: "root", EntryNodeID: tr.entry.ID}))
	return tr
}

func TestPersistenceAcrossDirs(t *testing.T) {
	f := newFixture(t)
	tr := f.tree()
	seed := must[model.Seed](t)(f.s.CreateSeed(model.Seed{DisplayName: "run", RootID: tr.root.ID}))
	shoot := must[model.Shoot](t)(f.s.CreateShoot(model.Shoot{SeedID: seed.ID, LeafID: tr.leaf1.ID, Content: "done"}))
	att := must[model.Attestation](t)(f.s.CreateAttestation(model.Attestation{SeedID: seed.ID, NodeID: tr.child.ID, Raw: []byte(`{"a":1}`)}))

	for dir, ids := range map[string][]uuid.UUID{
		filepath.Join(f.rootDir, "roots"):        {tr.root.ID},
		filepath.Join(f.rootDir, "nodes"):        {tr.entry.ID, tr.child.ID},
		filepath.Join(f.rootDir, "leaves"):       {tr.leaf1.ID, tr.leaf2.ID},
		filepath.Join(f.workDir, "seeds"):        {seed.ID},
		filepath.Join(f.workDir, "shoots"):       {shoot.ID},
		filepath.Join(f.workDir, "attestations"): {att.ID},
	} {
		for _, id := range ids {
			if _, err := os.Stat(filepath.Join(dir, id.String()+".json")); err != nil {
				t.Error(err)
			}
		}
	}

	f.reopen()
	got := must[model.Leaf](t)(f.s.GetLeaf(tr.leaf1.ID))
	if got.Description != "# task 1" || got.Version != DefaultVersion {
		t.Errorf("reloaded leaf: %+v", got)
	}
	// Raw is kept as JSON, but not byte for byte.
	var raw bytes.Buffer
	if err := json.Compact(&raw, must[model.Attestation](t)(f.s.GetAttestation(att.ID)).Raw); err != nil || raw.String() != `{"a":1}` {
		t.Errorf("reloaded raw: %s %v", raw.String(), err)
	}
	if n := len(f.s.ListShoots(seed.ID)); n != 1 {
		t.Errorf("%d shoots after reload", n)
	}
}

func TestValidation(t *testing.T) {
	f := newFixture(t)
	tr := f.tree()
	missing := uuid.New()

	_, err := f.s.CreateNode(model.Node{DisplayName: "x", ParentID: &missing})
	wantErr(t, err, ErrInvalid)
	_, err = f.s.CreateNode(model.Node{})
	wantErr(t, err, ErrInvalid)
	_, err = f.s.CreateLeaf(model.Leaf{ParentID: missing})
	wantErr(t, err, ErrInvalid)
	_, err = f.s.CreateRoot(model.Root{DisplayName: "r", EntryNodeID: missing})
	wantErr(t, err, ErrInvalid)
	_, err = f.s.CreateSeed(model.Seed{DisplayName: "s", RootID: missing})
	wantErr(t, err, ErrInvalid)

	// A node cannot move below itself.
	_, err = f.s.UpdateNode(tr.entry.ID, model.Node{DisplayName: "entry", ParentID: &tr.child.ID})
	wantErr(t, err, ErrInvalid)

	// Shoots and attestations only for elements in the seed's tree.
	other := must[model.Node](t)(f.s.CreateNode(model.Node{DisplayName: "other"}))
	otherLeaf := must[model.Leaf](t)(f.s.CreateLeaf(model.Leaf{ParentID: other.ID}))
	seed := must[model.Seed](t)(f.s.CreateSeed(model.Seed{DisplayName: "run", RootID: tr.root.ID}))
	_, err = f.s.CreateShoot(model.Shoot{SeedID: seed.ID, LeafID: otherLeaf.ID})
	wantErr(t, err, ErrInvalid)
	_, err = f.s.CreateAttestation(model.Attestation{SeedID: seed.ID, NodeID: other.ID})
	wantErr(t, err, ErrInvalid)

	_, err = f.s.GetNode(missing)
	wantErr(t, err, ErrNotFound)
}

func TestDeleteRules(t *testing.T) {
	f := newFixture(t)
	tr := f.tree()
	seed := must[model.Seed](t)(f.s.CreateSeed(model.Seed{DisplayName: "run", RootID: tr.root.ID}))
	must[model.Shoot](t)(f.s.CreateShoot(model.Shoot{SeedID: seed.ID, LeafID: tr.leaf2.ID}))

	wantErr(t, f.s.DeleteNode(tr.child.ID), ErrConflict) // has leaf2
	wantErr(t, f.s.DeleteLeaf(tr.leaf2.ID), ErrConflict) // has a shoot
	wantErr(t, f.s.DeleteRoot(tr.root.ID), ErrConflict)  // has a seed
	wantErr(t, f.s.DeleteNode(tr.entry.ID), ErrConflict)

	if err := f.s.DeleteSeed(seed.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(f.s.ListShoots(uuid.Nil)); n != 0 {
		t.Errorf("%d shoots left after deleting their seed", n)
	}
	for _, err := range []error{f.s.DeleteLeaf(tr.leaf2.ID), f.s.DeleteNode(tr.child.ID), f.s.DeleteRoot(tr.root.ID)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	f.reopen()
	if _, err := f.s.GetNode(tr.child.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted node still there: %v", err)
	}
}

func TestNodeVersion(t *testing.T) {
	f := newFixture(t)
	tr := f.tree()

	v2 := must[model.Node](t)(f.s.NewNodeVersion(tr.entry.ID, model.Node{DisplayName: "entry v2", Versioned: model.Versioned{Version: "2"}}))
	if v2.ID == tr.entry.ID || v2.Version != "2" || len(v2.PreviousVersions) != 1 || v2.PreviousVersions[0] != tr.entry.ID {
		t.Fatalf("new version: %+v", v2.Versioned)
	}
	f.reopen()

	if old := must[model.Node](t)(f.s.GetNode(tr.entry.ID)); !old.Superseded {
		t.Error("old version not superseded")
	}
	if r := must[model.Root](t)(f.s.GetRoot(tr.root.ID)); r.EntryNodeID != v2.ID {
		t.Error("root not re-pointed")
	}
	if c := must[model.Node](t)(f.s.GetNode(tr.child.ID)); *c.ParentID != v2.ID {
		t.Error("child node not re-pointed")
	}
	if l := must[model.Leaf](t)(f.s.GetLeaf(tr.leaf1.ID)); l.ParentID != v2.ID {
		t.Error("leaf not re-pointed")
	}
	tree := must[TreeNode](t)(f.s.Tree(tr.root.ID))
	if tree.ID != v2.ID || len(tree.Children) != 1 || len(tree.Leaves) != 1 || len(tree.Children[0].Leaves) != 1 {
		t.Errorf("tree after versioning: %+v", tree)
	}

	v3 := must[model.Node](t)(f.s.NewNodeVersion(v2.ID, model.Node{DisplayName: "entry v3", Versioned: model.Versioned{Version: "3"}}))
	for _, id := range []uuid.UUID{tr.entry.ID, v2.ID, v3.ID} {
		h := must[[]model.Node](t)(f.s.NodeHistory(id))
		if len(h) != 3 || h[0].Version != "1" || h[2].ID != v3.ID {
			t.Errorf("history of %s: %d versions", id, len(h))
		}
	}

	_, err := f.s.NewNodeVersion(tr.entry.ID, model.Node{DisplayName: "x", Versioned: model.Versioned{Version: "9"}})
	wantErr(t, err, ErrConflict) // superseded
	_, err = f.s.UpdateNode(tr.entry.ID, model.Node{DisplayName: "x"})
	wantErr(t, err, ErrConflict)
	_, err = f.s.NewNodeVersion(v3.ID, model.Node{DisplayName: "x", Versioned: model.Versioned{Version: "3"}})
	wantErr(t, err, ErrInvalid) // same version
	_, err = f.s.NewNodeVersion(v3.ID, model.Node{DisplayName: "x"})
	wantErr(t, err, ErrInvalid) // no version
	_, err = f.s.NewNodeVersion(v3.ID, model.Node{DisplayName: "x", Versioned: model.Versioned{Version: "4"}, ParentID: &tr.child.ID})
	wantErr(t, err, ErrInvalid) // below itself
	_, err = f.s.CreateLeaf(model.Leaf{ParentID: tr.entry.ID})
	wantErr(t, err, ErrInvalid) // superseded parent
}

func TestLeafVersion(t *testing.T) {
	f := newFixture(t)
	tr := f.tree()
	seed := must[model.Seed](t)(f.s.CreateSeed(model.Seed{DisplayName: "run", RootID: tr.root.ID}))
	shoot := must[model.Shoot](t)(f.s.CreateShoot(model.Shoot{SeedID: seed.ID, LeafID: tr.leaf1.ID}))

	v2 := must[model.Leaf](t)(f.s.NewLeafVersion(tr.leaf1.ID, model.Leaf{ParentID: tr.entry.ID, Description: "task 1, revised", Versioned: model.Versioned{Version: "1.1"}}))
	f.reopen()
	if old := must[model.Leaf](t)(f.s.GetLeaf(tr.leaf1.ID)); !old.Superseded {
		t.Error("old leaf not superseded")
	}
	if sh := must[model.Shoot](t)(f.s.GetShoot(shoot.ID)); sh.LeafID != tr.leaf1.ID {
		t.Error("shoot was moved to the new version")
	}
	if h := must[[]model.Leaf](t)(f.s.LeafHistory(tr.leaf1.ID)); len(h) != 2 || h[1].ID != v2.ID {
		t.Errorf("history: %+v", h)
	}
	leaves := must[TreeNode](t)(f.s.Tree(tr.root.ID)).Leaves
	if len(leaves) != 1 || leaves[0].ID != v2.ID {
		t.Errorf("tree leaves: %+v", leaves)
	}
	_, err := f.s.CreateShoot(model.Shoot{SeedID: seed.ID, LeafID: tr.leaf1.ID})
	wantErr(t, err, ErrInvalid)
	wantErr(t, f.s.DeleteLeaf(v2.ID), ErrConflict) // the shoot answers v1
}

func TestPath(t *testing.T) {
	f := newFixture(t)
	tr := f.tree()
	p := must[[]model.Node](t)(f.s.Path(tr.child.ID))
	if len(p) != 2 || p[0].ID != tr.entry.ID || p[1].ID != tr.child.ID {
		t.Errorf("path: %+v", p)
	}
}

func TestDanglingReferencesDoNotFailLoad(t *testing.T) {
	f := newFixture(t)
	tr := f.tree()
	seed := must[model.Seed](t)(f.s.CreateSeed(model.Seed{DisplayName: "run", RootID: tr.root.ID}))
	must[model.Shoot](t)(f.s.CreateShoot(model.Shoot{SeedID: seed.ID, LeafID: tr.leaf2.ID}))

	// Someone removes a leaf from the root dir behind our back.
	if err := os.Remove(filepath.Join(f.rootDir, "leaves", tr.leaf2.ID.String()+".json")); err != nil {
		t.Fatal(err)
	}
	f.reopen()
	d := f.s.Dangling()
	if len(d) != 1 || d[0].Kind != "shoot" || d[0].Field != "leafId" || d[0].Target != tr.leaf2.ID {
		t.Errorf("dangling: %+v", d)
	}
}

func TestLoadRejectsCorruptFiles(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.rootDir, "nodes", uuid.NewString()+".json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(f.rootDir, f.workDir, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Error("corrupt file accepted")
	}
}
