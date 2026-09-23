// Package seed computes what is still missing in a Seed.
package seed

import (
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/emeland-io/custos/internal/model"
	"github.com/emeland-io/custos/internal/store"
)

// PathEntry names a Node on the way from the top of the tree.
type PathEntry struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"displayName"`
}

// MissingShoot is a current Leaf without a Shoot in the Seed.
type MissingShoot struct {
	Leaf model.Leaf  `json:"leaf"`
	Path []PathEntry `json:"path"`
	// Stale are Shoots answering previous versions of the Leaf.
	Stale []model.Shoot `json:"stale"`
}

// AttestationSummary is an Attestation without its raw content.
type AttestationSummary struct {
	ID             uuid.UUID                `json:"id"`
	NodeID         uuid.UUID                `json:"nodeId"`
	PredicateType  string                   `json:"predicateType"`
	Status         model.VerificationStatus `json:"status"`
	RequirementMet bool                     `json:"requirementMet"`
	CreatedAt      time.Time                `json:"createdAt"`
}

func summarize(a model.Attestation) AttestationSummary {
	return AttestationSummary{
		ID: a.ID, NodeID: a.NodeID, PredicateType: a.PredicateType,
		Status: a.Verification.Status, RequirementMet: a.Verification.RequirementMet, CreatedAt: a.CreatedAt,
	}
}

// MissingAttestation is a current Node without an Attestation in the Seed
// that is verified and meets its requirement.
type MissingAttestation struct {
	Node model.Node  `json:"node"`
	Path []PathEntry `json:"path"`
	// Stale are Attestations meeting the requirement of a previous
	// version of the Node.
	Stale []AttestationSummary `json:"stale"`
	// Rejected are Attestations for this version that are not verified
	// or do not meet the requirement.
	Rejected []AttestationSummary `json:"rejected"`
}

// Status is the progress of a Seed.
type Status struct {
	Seed                model.Seed           `json:"seed"`
	MissingShoots       []MissingShoot       `json:"missingShoots"`
	MissingAttestations []MissingAttestation `json:"missingAttestations"`
	// Orphaned are references from this Seed to elements that no longer
	// exist.
	Orphaned []store.Dangling `json:"orphaned"`
	// TreeError is set when the tree of the Root cannot be read.
	TreeError      string `json:"treeError,omitempty"`
	Leaves         int    `json:"leaves"`
	AnsweredLeaves int    `json:"answeredLeaves"`
	Nodes          int    `json:"nodes"`
	AttestedNodes  int    `json:"attestedNodes"`
	// Percent is the share of answered Leaves and attested Nodes.
	Percent int `json:"percent"`
}

// Compute returns the Status of the Seed seedID.
func Compute(s *store.Store, seedID uuid.UUID) (Status, error) {
	sd, err := s.GetSeed(seedID)
	if err != nil {
		return Status{}, err
	}
	st := Status{
		Seed:                sd,
		MissingShoots:       []MissingShoot{},
		MissingAttestations: []MissingAttestation{},
		Orphaned:            []store.Dangling{},
	}
	shoots := s.ListShoots(seedID)
	atts := s.ListAttestations(seedID)
	for _, d := range s.Dangling() {
		if slices.ContainsFunc(shoots, func(sh model.Shoot) bool { return sh.ID == d.ID }) ||
			slices.ContainsFunc(atts, func(a model.Attestation) bool { return a.ID == d.ID }) ||
			d.ID == seedID {
			st.Orphaned = append(st.Orphaned, d)
		}
	}

	tree, err := s.Tree(sd.RootID)
	if err != nil {
		st.TreeError = err.Error()
		return st, nil
	}
	st.walk(tree, nil, shoots, atts)
	if total := st.Leaves + st.Nodes; total > 0 {
		st.Percent = (st.AnsweredLeaves + st.AttestedNodes) * 100 / total
	}
	return st, nil
}

func (st *Status) walk(t store.TreeNode, path []PathEntry, shoots []model.Shoot, atts []model.Attestation) {
	path = append(slices.Clip(path), PathEntry{ID: t.ID, DisplayName: t.DisplayName})

	st.Nodes++
	var rejected, stale []AttestationSummary
	attested := false
	for _, a := range atts {
		met := a.Verification.RequirementMet
		switch {
		case a.NodeID == t.ID && met:
			attested = true
		case a.NodeID == t.ID:
			rejected = append(rejected, summarize(a))
		case met && slices.Contains(t.PreviousVersions, a.NodeID):
			stale = append(stale, summarize(a))
		}
	}
	if attested {
		st.AttestedNodes++
	} else {
		st.MissingAttestations = append(st.MissingAttestations, MissingAttestation{
			Node: t.Node, Path: path, Stale: nonNil(stale), Rejected: nonNil(rejected),
		})
	}

	for _, l := range t.Leaves {
		st.Leaves++
		var staleShoots []model.Shoot
		answered := false
		for _, sh := range shoots {
			switch {
			case sh.LeafID == l.ID:
				answered = true
			case slices.Contains(l.PreviousVersions, sh.LeafID):
				staleShoots = append(staleShoots, sh)
			}
		}
		if answered {
			st.AnsweredLeaves++
		} else {
			st.MissingShoots = append(st.MissingShoots, MissingShoot{Leaf: l, Path: path, Stale: nonNil(staleShoots)})
		}
	}

	for _, c := range t.Children {
		st.walk(c, path, shoots, atts)
	}
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
