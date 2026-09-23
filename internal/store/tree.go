package store

import (
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/model"
)

// DefaultVersion is the version of a new Node or Leaf when none is given.
const DefaultVersion = "1"

// ancestry returns nodeID followed by its ancestors, nearest first. It
// stops at a missing Node or a cycle.
func (s *Store) ancestry(nodeID uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for id := &nodeID; id != nil; {
		n, ok := s.nodes.get(*id)
		if !ok || slices.Contains(out, *id) {
			break
		}
		out = append(out, *id)
		id = n.ParentID
	}
	return out
}

func (s *Store) checkCurrentNode(id uuid.UUID, field string) error {
	n, ok := s.nodes.get(id)
	if !ok {
		return fmt.Errorf("%w: %s: %w", ErrInvalid, field, notFound("node", id))
	}
	if n.Superseded {
		return fmt.Errorf("%w: %s: node %s is superseded", ErrInvalid, field, id)
	}
	return nil
}

// Nodes

// ListNodes returns the current Nodes, and the superseded ones when all
// is set.
func (s *Store) ListNodes(all bool) []model.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nodes.list(func(n *model.Node) bool { return all || !n.Superseded })
}

func (s *Store) GetNode(id uuid.UUID) (model.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.nodes.get(id)
	if !ok {
		return n, notFound("node", id)
	}
	return n, nil
}

// setNodeFields copies the editable fields of in to n.
func setNodeFields(n *model.Node, in model.Node) {
	n.DisplayName = in.DisplayName
	n.ParentID = in.ParentID
	n.Description = in.Description
	n.RequiredAttestation = in.RequiredAttestation
	n.RequiredAttestation.RequiredIdentities = slices.Clone(in.RequiredAttestation.RequiredIdentities)
	n.MissingReason = in.MissingReason
}

func (s *Store) checkNode(n *model.Node) error {
	if n.DisplayName == "" {
		return fmt.Errorf("%w: displayName is required", ErrInvalid)
	}
	if n.ParentID == nil {
		return nil
	}
	if err := s.checkCurrentNode(*n.ParentID, "parentId"); err != nil {
		return err
	}
	if slices.Contains(s.ancestry(*n.ParentID), n.ID) {
		return fmt.Errorf("%w: parentId: node %s would be its own ancestor", ErrInvalid, n.ID)
	}
	return nil
}

// CreateNode stores a new Node built from the editable fields and the
// version of in.
func (s *Store) CreateNode(in model.Node) (model.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := model.Node{Meta: s.newMeta(), Versioned: model.Versioned{Version: in.Version, PreviousVersions: []uuid.UUID{}}}
	if n.Version == "" {
		n.Version = DefaultVersion
	}
	setNodeFields(&n, in)
	if err := s.checkNode(&n); err != nil {
		return n, err
	}
	return n, s.nodes.put(&n)
}

// UpdateNode changes the editable fields of a current Node in place.
func (s *Store) UpdateNode(id uuid.UUID, in model.Node) (model.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.currentNode(id)
	if err != nil {
		return n, err
	}
	setNodeFields(&n, in)
	n.UpdatedAt = s.now()
	if err := s.checkNode(&n); err != nil {
		return n, err
	}
	return n, s.nodes.put(&n)
}

func (s *Store) currentNode(id uuid.UUID) (model.Node, error) {
	n, ok := s.nodes.get(id)
	if !ok {
		return n, notFound("node", id)
	}
	if n.Superseded {
		return n, fmt.Errorf("%w: node %s is superseded", ErrConflict, id)
	}
	return n, nil
}

// NewNodeVersion replaces a current Node with a new version carrying the
// editable fields and version of in. Child Nodes, Leaves and Roots are
// re-pointed to the new version; Attestations keep the version they
// attest.
func (s *Store) NewNodeVersion(id uuid.UUID, in model.Node) (model.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.currentNode(id)
	if err != nil {
		return old, err
	}
	n := model.Node{Meta: s.newMeta()}
	if n.Versioned, err = nextVersion(old.Versioned, old.ID, in.Version); err != nil {
		return n, err
	}
	setNodeFields(&n, in)
	if err := s.checkNode(&n); err != nil {
		return n, err
	}
	// The children of old become children of n.
	if n.ParentID != nil && slices.Contains(s.ancestry(*n.ParentID), old.ID) {
		return n, fmt.Errorf("%w: parentId: node %s would be its own ancestor", ErrInvalid, old.ID)
	}
	if err := s.nodes.put(&n); err != nil {
		return n, err
	}
	for _, c := range s.nodes.list(func(c *model.Node) bool { return !c.Superseded && c.ParentID != nil && *c.ParentID == id }) {
		c.ParentID = &n.ID
		c.UpdatedAt = n.CreatedAt
		if err := s.nodes.put(&c); err != nil {
			return n, err
		}
	}
	for _, l := range s.leaves.list(func(l *model.Leaf) bool { return !l.Superseded && l.ParentID == id }) {
		l.ParentID = n.ID
		l.UpdatedAt = n.CreatedAt
		if err := s.leaves.put(&l); err != nil {
			return n, err
		}
	}
	for _, r := range s.roots.list(func(r *model.Root) bool { return r.EntryNodeID == id }) {
		r.EntryNodeID = n.ID
		r.UpdatedAt = n.CreatedAt
		if err := s.roots.put(&r); err != nil {
			return n, err
		}
	}
	old.Superseded = true
	old.UpdatedAt = n.CreatedAt
	return n, s.nodes.put(&old)
}

// nextVersion returns the version fields of the successor of the element
// id with fields v.
func nextVersion(v model.Versioned, id uuid.UUID, version string) (model.Versioned, error) {
	if version == "" {
		return v, fmt.Errorf("%w: version is required", ErrInvalid)
	}
	if version == v.Version {
		return v, fmt.Errorf("%w: version %q is the current version", ErrInvalid, version)
	}
	return model.Versioned{
		Version:          version,
		PreviousVersions: append(slices.Clone(v.PreviousVersions), id),
	}, nil
}

// DeleteNode deletes a current Node without children, together with its
// previous versions. It fails while a Root starts at it or an Attestation
// refers to any of its versions.
func (s *Store) DeleteNode(id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.currentNode(id)
	if err != nil {
		return err
	}
	if s.nodes.any(func(c *model.Node) bool { return !c.Superseded && c.ParentID != nil && *c.ParentID == id }) ||
		s.leaves.any(func(l *model.Leaf) bool { return !l.Superseded && l.ParentID == id }) {
		return fmt.Errorf("%w: node %s has children", ErrConflict, id)
	}
	if s.roots.any(func(r *model.Root) bool { return r.EntryNodeID == id }) {
		return fmt.Errorf("%w: node %s is the entry of a root", ErrConflict, id)
	}
	versions := append(slices.Clone(n.PreviousVersions), id)
	if s.attestations.any(func(a *model.Attestation) bool { return slices.Contains(versions, a.NodeID) }) {
		return fmt.Errorf("%w: node %s has attestations", ErrConflict, id)
	}
	for _, v := range versions {
		if err := s.nodes.remove(v); err != nil {
			return err
		}
	}
	return nil
}

// NodeHistory returns all versions of the Node id belongs to, oldest
// first.
func (s *Store) NodeHistory(id uuid.UUID) ([]model.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return history(s.nodes, id, "node")
}

// history finds the latest version of the element id and returns its
// versions, oldest first. Versions deleted from disk are skipped.
func history[E any, P interface {
	element[E]
	GetVersioned() *model.Versioned
}](c *collection[E, P], id uuid.UUID, kind string) ([]E, error) {
	latest, ok := c.items[id]
	if !ok {
		return nil, notFound(kind, id)
	}
	for _, v := range c.items {
		prev := v.GetVersioned().PreviousVersions
		if slices.Contains(prev, id) && len(prev) > len(latest.GetVersioned().PreviousVersions) {
			latest = v
		}
	}
	var out []E
	for _, prev := range latest.GetVersioned().PreviousVersions {
		if e, ok := c.get(prev); ok {
			out = append(out, e)
		}
	}
	return append(out, *latest), nil
}

// Leaves

// ListLeaves returns the current Leaves, and the superseded ones when all
// is set.
func (s *Store) ListLeaves(all bool) []model.Leaf {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.leaves.list(func(l *model.Leaf) bool { return all || !l.Superseded })
}

func (s *Store) GetLeaf(id uuid.UUID) (model.Leaf, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.leaves.get(id)
	if !ok {
		return l, notFound("leaf", id)
	}
	return l, nil
}

func (s *Store) currentLeaf(id uuid.UUID) (model.Leaf, error) {
	l, ok := s.leaves.get(id)
	if !ok {
		return l, notFound("leaf", id)
	}
	if l.Superseded {
		return l, fmt.Errorf("%w: leaf %s is superseded", ErrConflict, id)
	}
	return l, nil
}

// CreateLeaf stores a new Leaf built from the parentId, description and
// version of in.
func (s *Store) CreateLeaf(in model.Leaf) (model.Leaf, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := model.Leaf{
		Meta:        s.newMeta(),
		Versioned:   model.Versioned{Version: in.Version, PreviousVersions: []uuid.UUID{}},
		ParentID:    in.ParentID,
		Description: in.Description,
	}
	if l.Version == "" {
		l.Version = DefaultVersion
	}
	if err := s.checkCurrentNode(l.ParentID, "parentId"); err != nil {
		return l, err
	}
	return l, s.leaves.put(&l)
}

// UpdateLeaf changes the parentId and description of a current Leaf in
// place.
func (s *Store) UpdateLeaf(id uuid.UUID, in model.Leaf) (model.Leaf, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.currentLeaf(id)
	if err != nil {
		return l, err
	}
	l.ParentID, l.Description = in.ParentID, in.Description
	l.UpdatedAt = s.now()
	if err := s.checkCurrentNode(l.ParentID, "parentId"); err != nil {
		return l, err
	}
	return l, s.leaves.put(&l)
}

// NewLeafVersion replaces a current Leaf with a new version carrying the
// parentId, description and version of in. Shoots keep the version they
// answer.
func (s *Store) NewLeafVersion(id uuid.UUID, in model.Leaf) (model.Leaf, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.currentLeaf(id)
	if err != nil {
		return old, err
	}
	l := model.Leaf{Meta: s.newMeta(), ParentID: in.ParentID, Description: in.Description}
	if l.Versioned, err = nextVersion(old.Versioned, old.ID, in.Version); err != nil {
		return l, err
	}
	if err := s.checkCurrentNode(l.ParentID, "parentId"); err != nil {
		return l, err
	}
	if err := s.leaves.put(&l); err != nil {
		return l, err
	}
	old.Superseded = true
	old.UpdatedAt = l.CreatedAt
	return l, s.leaves.put(&old)
}

// DeleteLeaf deletes a current Leaf with its previous versions. It fails
// while a Shoot refers to any of its versions.
func (s *Store) DeleteLeaf(id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.currentLeaf(id)
	if err != nil {
		return err
	}
	versions := append(slices.Clone(l.PreviousVersions), id)
	if s.shoots.any(func(sh *model.Shoot) bool { return slices.Contains(versions, sh.LeafID) }) {
		return fmt.Errorf("%w: leaf %s has shoots", ErrConflict, id)
	}
	for _, v := range versions {
		if err := s.leaves.remove(v); err != nil {
			return err
		}
	}
	return nil
}

// LeafHistory returns all versions of the Leaf id belongs to, oldest
// first.
func (s *Store) LeafHistory(id uuid.UUID) ([]model.Leaf, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return history(s.leaves, id, "leaf")
}

// Tree queries

// TreeNode is a Node with its current children.
type TreeNode struct {
	model.Node
	Children []TreeNode   `json:"children"`
	Leaves   []model.Leaf `json:"leaves"`
}

// Tree returns the current tree below the entry Node of a Root.
func (s *Store) Tree(rootID uuid.UUID) (TreeNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.roots.get(rootID)
	if !ok {
		return TreeNode{}, notFound("root", rootID)
	}
	return s.subtree(r.EntryNodeID, map[uuid.UUID]bool{})
}

// Subtree returns the current tree below a Node.
func (s *Store) Subtree(nodeID uuid.UUID) (TreeNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.subtree(nodeID, map[uuid.UUID]bool{})
}

func (s *Store) subtree(id uuid.UUID, seen map[uuid.UUID]bool) (TreeNode, error) {
	n, ok := s.nodes.get(id)
	if !ok {
		return TreeNode{}, notFound("node", id)
	}
	seen[id] = true
	t := TreeNode{
		Node:   n,
		Leaves: s.leaves.list(func(l *model.Leaf) bool { return !l.Superseded && l.ParentID == id }),
	}
	t.Children = []TreeNode{}
	for _, c := range s.nodes.list(func(c *model.Node) bool { return !c.Superseded && c.ParentID != nil && *c.ParentID == id }) {
		if seen[c.ID] {
			continue
		}
		ct, err := s.subtree(c.ID, seen)
		if err != nil {
			return t, err
		}
		t.Children = append(t.Children, ct)
	}
	return t, nil
}

// Path returns the Nodes from the top of the tree down to nodeID.
func (s *Store) Path(nodeID uuid.UUID) ([]model.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.nodes.has(nodeID) {
		return nil, notFound("node", nodeID)
	}
	ids := s.ancestry(nodeID)
	out := make([]model.Node, 0, len(ids))
	for _, id := range slices.Backward(ids) {
		n, _ := s.nodes.get(id)
		out = append(out, n)
	}
	return out, nil
}
