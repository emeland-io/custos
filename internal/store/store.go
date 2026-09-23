// Package store persists custos elements as one JSON file per element.
//
// Roots, Nodes and Leaves are kept below the root directory, Seeds, Shoots
// and Attestations below the work directory. All elements are held in
// memory; every change is written to disk before it becomes visible.
package store

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/model"
)

// Errors returned by the Store, wrapped with details.
var (
	ErrNotFound = errors.New("not found")
	// ErrConflict means the change contradicts other elements, for example
	// deleting a Node that still has children.
	ErrConflict = errors.New("conflict")
	// ErrInvalid means the input itself is not acceptable.
	ErrInvalid = errors.New("invalid")
)

// Store holds all elements. It is safe for concurrent use.
type Store struct {
	mu           sync.RWMutex
	now          func() time.Time
	roots        *collection[model.Root, *model.Root]
	nodes        *collection[model.Node, *model.Node]
	leaves       *collection[model.Leaf, *model.Leaf]
	seeds        *collection[model.Seed, *model.Seed]
	shoots       *collection[model.Shoot, *model.Shoot]
	attestations *collection[model.Attestation, *model.Attestation]
}

// Open loads the Store from rootDir and workDir, creating the directories
// as needed. References that cannot be resolved are logged, not rejected:
// the root dir may be edited outside of custos.
func Open(rootDir, workDir string, log *slog.Logger) (*Store, error) {
	s := &Store{
		now:          func() time.Time { return time.Now().UTC() },
		roots:        newCollection[model.Root](filepath.Join(rootDir, "roots")),
		nodes:        newCollection[model.Node](filepath.Join(rootDir, "nodes")),
		leaves:       newCollection[model.Leaf](filepath.Join(rootDir, "leaves")),
		seeds:        newCollection[model.Seed](filepath.Join(workDir, "seeds")),
		shoots:       newCollection[model.Shoot](filepath.Join(workDir, "shoots")),
		attestations: newCollection[model.Attestation](filepath.Join(workDir, "attestations")),
	}
	for _, c := range []interface{ load() error }{s.roots, s.nodes, s.leaves, s.seeds, s.shoots, s.attestations} {
		if err := c.load(); err != nil {
			return nil, fmt.Errorf("loading store: %w", err)
		}
	}
	for _, d := range s.Dangling() {
		log.Warn("dangling reference", "kind", d.Kind, "id", d.ID, "field", d.Field, "target", d.Target)
	}
	return s, nil
}

// Dangling is a reference to an element that does not exist.
type Dangling struct {
	Kind   string    `json:"kind"`
	ID     uuid.UUID `json:"id"`
	Field  string    `json:"field"`
	Target uuid.UUID `json:"target"`
}

// Dangling lists all references to missing elements.
func (s *Store) Dangling() []Dangling {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Dangling
	check := func(kind string, id uuid.UUID, field string, target uuid.UUID, ok bool) {
		if !ok {
			out = append(out, Dangling{Kind: kind, ID: id, Field: field, Target: target})
		}
	}
	for _, r := range s.roots.items {
		check("root", r.ID, "entryNodeId", r.EntryNodeID, s.nodes.has(r.EntryNodeID))
	}
	for _, n := range s.nodes.items {
		if n.ParentID != nil {
			check("node", n.ID, "parentId", *n.ParentID, s.nodes.has(*n.ParentID))
		}
	}
	for _, l := range s.leaves.items {
		check("leaf", l.ID, "parentId", l.ParentID, s.nodes.has(l.ParentID))
	}
	for _, sd := range s.seeds.items {
		check("seed", sd.ID, "rootId", sd.RootID, s.roots.has(sd.RootID))
	}
	for _, sh := range s.shoots.items {
		check("shoot", sh.ID, "seedId", sh.SeedID, s.seeds.has(sh.SeedID))
		check("shoot", sh.ID, "leafId", sh.LeafID, s.leaves.has(sh.LeafID))
	}
	for _, a := range s.attestations.items {
		check("attestation", a.ID, "seedId", a.SeedID, s.seeds.has(a.SeedID))
		check("attestation", a.ID, "nodeId", a.NodeID, s.nodes.has(a.NodeID))
	}
	return out
}

// newMeta returns the Meta of a new element.
func (s *Store) newMeta() model.Meta {
	now := s.now()
	return model.Meta{ID: uuid.New(), CreatedAt: now, UpdatedAt: now}
}

func notFound(kind string, id uuid.UUID) error {
	return fmt.Errorf("%w: %s %s", ErrNotFound, kind, id)
}

// Roots

func (s *Store) ListRoots() []model.Root {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.roots.list(nil)
}

func (s *Store) GetRoot(id uuid.UUID) (model.Root, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.roots.get(id)
	if !ok {
		return r, notFound("root", id)
	}
	return r, nil
}

func (s *Store) checkRoot(r *model.Root) error {
	if r.DisplayName == "" {
		return fmt.Errorf("%w: displayName is required", ErrInvalid)
	}
	return s.checkCurrentNode(r.EntryNodeID, "entryNodeId")
}

// CreateRoot stores a new Root built from the displayName, description and
// entryNodeId of in.
func (s *Store) CreateRoot(in model.Root) (model.Root, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := model.Root{Meta: s.newMeta(), DisplayName: in.DisplayName, Description: in.Description, EntryNodeID: in.EntryNodeID}
	if err := s.checkRoot(&r); err != nil {
		return r, err
	}
	return r, s.roots.put(&r)
}

// UpdateRoot replaces the displayName, description and entryNodeId of a
// Root.
func (s *Store) UpdateRoot(id uuid.UUID, in model.Root) (model.Root, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.roots.get(id)
	if !ok {
		return r, notFound("root", id)
	}
	r.DisplayName, r.Description, r.EntryNodeID = in.DisplayName, in.Description, in.EntryNodeID
	r.UpdatedAt = s.now()
	if err := s.checkRoot(&r); err != nil {
		return r, err
	}
	return r, s.roots.put(&r)
}

// DeleteRoot deletes a Root that no Seed refers to.
func (s *Store) DeleteRoot(id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.roots.has(id) {
		return notFound("root", id)
	}
	if s.seeds.any(func(sd *model.Seed) bool { return sd.RootID == id }) {
		return fmt.Errorf("%w: root %s is used by seeds", ErrConflict, id)
	}
	return s.roots.remove(id)
}

// Seeds

func (s *Store) ListSeeds() []model.Seed {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seeds.list(nil)
}

func (s *Store) GetSeed(id uuid.UUID) (model.Seed, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sd, ok := s.seeds.get(id)
	if !ok {
		return sd, notFound("seed", id)
	}
	return sd, nil
}

func (s *Store) checkSeed(sd *model.Seed) error {
	if sd.DisplayName == "" {
		return fmt.Errorf("%w: displayName is required", ErrInvalid)
	}
	if !s.roots.has(sd.RootID) {
		return fmt.Errorf("%w: rootId: %w", ErrInvalid, notFound("root", sd.RootID))
	}
	return nil
}

// CreateSeed stores a new Seed built from the displayName, description and
// rootId of in.
func (s *Store) CreateSeed(in model.Seed) (model.Seed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sd := model.Seed{Meta: s.newMeta(), DisplayName: in.DisplayName, Description: in.Description, RootID: in.RootID}
	if err := s.checkSeed(&sd); err != nil {
		return sd, err
	}
	return sd, s.seeds.put(&sd)
}

// UpdateSeed replaces the displayName and description of a Seed. Its Root
// cannot change.
func (s *Store) UpdateSeed(id uuid.UUID, in model.Seed) (model.Seed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sd, ok := s.seeds.get(id)
	if !ok {
		return sd, notFound("seed", id)
	}
	sd.DisplayName, sd.Description = in.DisplayName, in.Description
	sd.UpdatedAt = s.now()
	if sd.DisplayName == "" {
		return sd, fmt.Errorf("%w: displayName is required", ErrInvalid)
	}
	return sd, s.seeds.put(&sd)
}

// DeleteSeed deletes a Seed with its Shoots and Attestations.
func (s *Store) DeleteSeed(id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.seeds.has(id) {
		return notFound("seed", id)
	}
	for _, sh := range s.shoots.list(func(sh *model.Shoot) bool { return sh.SeedID == id }) {
		if err := s.shoots.remove(sh.ID); err != nil {
			return err
		}
	}
	for _, a := range s.attestations.list(func(a *model.Attestation) bool { return a.SeedID == id }) {
		if err := s.attestations.remove(a.ID); err != nil {
			return err
		}
	}
	return s.seeds.remove(id)
}

// Shoots

// ListShoots returns the Shoots of a Seed, or all Shoots when seedID is
// uuid.Nil.
func (s *Store) ListShoots(seedID uuid.UUID) []model.Shoot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.shoots.list(func(sh *model.Shoot) bool { return seedID == uuid.Nil || sh.SeedID == seedID })
}

func (s *Store) GetShoot(id uuid.UUID) (model.Shoot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sh, ok := s.shoots.get(id)
	if !ok {
		return sh, notFound("shoot", id)
	}
	return sh, nil
}

// CreateShoot stores a new Shoot for a current Leaf within the tree of
// the Seed's Root.
func (s *Store) CreateShoot(in model.Shoot) (model.Shoot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh := model.Shoot{Meta: s.newMeta(), SeedID: in.SeedID, LeafID: in.LeafID, Content: in.Content, Author: in.Author}
	sd, ok := s.seeds.get(sh.SeedID)
	if !ok {
		return sh, fmt.Errorf("%w: seedId: %w", ErrInvalid, notFound("seed", sh.SeedID))
	}
	l, ok := s.leaves.get(sh.LeafID)
	if !ok {
		return sh, fmt.Errorf("%w: leafId: %w", ErrInvalid, notFound("leaf", sh.LeafID))
	}
	if l.Superseded {
		return sh, fmt.Errorf("%w: leaf %s is superseded", ErrInvalid, l.ID)
	}
	if !s.inTree(sd.RootID, l.ParentID) {
		return sh, fmt.Errorf("%w: leaf %s is not in the tree of root %s", ErrInvalid, l.ID, sd.RootID)
	}
	return sh, s.shoots.put(&sh)
}

// UpdateShoot replaces the content and author of a Shoot.
func (s *Store) UpdateShoot(id uuid.UUID, in model.Shoot) (model.Shoot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.shoots.get(id)
	if !ok {
		return sh, notFound("shoot", id)
	}
	sh.Content, sh.Author = in.Content, in.Author
	sh.UpdatedAt = s.now()
	return sh, s.shoots.put(&sh)
}

func (s *Store) DeleteShoot(id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.shoots.has(id) {
		return notFound("shoot", id)
	}
	return s.shoots.remove(id)
}

// Attestations

// ListAttestations returns the Attestations of a Seed, or all when seedID
// is uuid.Nil.
func (s *Store) ListAttestations(seedID uuid.UUID) []model.Attestation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.attestations.list(func(a *model.Attestation) bool { return seedID == uuid.Nil || a.SeedID == seedID })
}

func (s *Store) GetAttestation(id uuid.UUID) (model.Attestation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.attestations.get(id)
	if !ok {
		return a, notFound("attestation", id)
	}
	return a, nil
}

// CreateAttestation stores a new Attestation for a current Node within
// the tree of the Seed's Root. The caller fills in the parsed and verified
// fields.
func (s *Store) CreateAttestation(in model.Attestation) (model.Attestation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := in
	a.Meta = s.newMeta()
	sd, ok := s.seeds.get(a.SeedID)
	if !ok {
		return a, fmt.Errorf("%w: seedId: %w", ErrInvalid, notFound("seed", a.SeedID))
	}
	n, ok := s.nodes.get(a.NodeID)
	if !ok {
		return a, fmt.Errorf("%w: nodeId: %w", ErrInvalid, notFound("node", a.NodeID))
	}
	if n.Superseded {
		return a, fmt.Errorf("%w: node %s is superseded", ErrInvalid, n.ID)
	}
	if !s.inTree(sd.RootID, n.ID) {
		return a, fmt.Errorf("%w: node %s is not in the tree of root %s", ErrInvalid, n.ID, sd.RootID)
	}
	return a, s.attestations.put(&a)
}

// SetVerification replaces the verification result of an Attestation.
func (s *Store) SetVerification(id uuid.UUID, v model.Verification) (model.Attestation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.attestations.get(id)
	if !ok {
		return a, notFound("attestation", id)
	}
	a.Verification = v
	a.UpdatedAt = s.now()
	return a, s.attestations.put(&a)
}

func (s *Store) DeleteAttestation(id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.attestations.has(id) {
		return notFound("attestation", id)
	}
	return s.attestations.remove(id)
}

// inTree reports whether nodeID is the entry Node of the Root or below it.
func (s *Store) inTree(rootID, nodeID uuid.UUID) bool {
	r, ok := s.roots.get(rootID)
	if !ok {
		return false
	}
	return slices.Contains(s.ancestry(nodeID), r.EntryNodeID)
}
