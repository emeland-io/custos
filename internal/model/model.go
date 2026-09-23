// Package model defines the persisted element types of custos.
//
// Roots, Nodes and Leaves form the definition tree and live in the root
// directory. Seeds, Shoots and Attestations are the data of a run over
// that tree and live in the work directory.
package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Meta holds the fields common to every persisted element.
type Meta struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// GetMeta gives generic code access to the common fields.
func (m *Meta) GetMeta() *Meta { return m }

// Versioned holds the version history of Nodes and Leaves. A new version
// is a copy under a new ID; the IDs it replaced are kept in
// PreviousVersions (oldest first) and the replaced elements are marked
// Superseded.
type Versioned struct {
	Version          string      `json:"version"`
	PreviousVersions []uuid.UUID `json:"previousVersions"`
	Superseded       bool        `json:"superseded"`
}

// GetVersioned gives generic code access to the version fields.
func (v *Versioned) GetVersioned() *Versioned { return v }

// Root is the entry point to a tree of Nodes.
type Root struct {
	Meta
	DisplayName string    `json:"displayName"`
	Description string    `json:"description"`
	EntryNodeID uuid.UUID `json:"entryNodeId"`
}

// RequiredAttestation describes the in-toto attestation a Node requires.
type RequiredAttestation struct {
	// PredicateType the attestation's statement must carry.
	PredicateType string `json:"predicateType"`
	// SubjectName, when set, must match the name of one of the subjects.
	SubjectName string `json:"subjectName,omitempty"`
	// RequiredIdentities are signer identity specs (for example
	// "key::ed25519::<id>" or "sigstore::<issuer>::<identity>"), all of
	// which must have signed the attestation.
	RequiredIdentities []string `json:"requiredIdentities,omitempty"`
}

// Node is an inner element of the tree requiring an attestation.
type Node struct {
	Meta
	Versioned
	DisplayName         string              `json:"displayName"`
	ParentID            *uuid.UUID          `json:"parentId,omitempty"`
	Description         string              `json:"description"`
	RequiredAttestation RequiredAttestation `json:"requiredAttestation"`
	MissingReason       string              `json:"missingReason"`
}

// Leaf describes a task below a Node.
type Leaf struct {
	Meta
	Versioned
	ParentID    uuid.UUID `json:"parentId"`
	Description string    `json:"description"`
}

// Seed is one run over the tree of a Root.
type Seed struct {
	Meta
	DisplayName string    `json:"displayName"`
	Description string    `json:"description"`
	RootID      uuid.UUID `json:"rootId"`
}

// Shoot is the result of, or reply to, the task of a Leaf within a Seed.
type Shoot struct {
	Meta
	SeedID  uuid.UUID `json:"seedId"`
	LeafID  uuid.UUID `json:"leafId"`
	Content string    `json:"content"`
	Author  string    `json:"author"`
}

// VerificationStatus is the outcome of a signature verification.
type VerificationStatus string

const (
	StatusVerified     VerificationStatus = "VERIFIED"
	StatusFailed       VerificationStatus = "FAILED"
	StatusUnsigned     VerificationStatus = "UNSIGNED"
	StatusUnverifiable VerificationStatus = "UNVERIFIABLE"
)

// Subject is a subject of an in-toto statement.
type Subject struct {
	Name   string            `json:"name,omitempty"`
	URI    string            `json:"uri,omitempty"`
	Digest map[string]string `json:"digest,omitempty"`
}

// Verification records the result of verifying an attestation.
type Verification struct {
	Status VerificationStatus `json:"status"`
	// Identities are the specs of the identities that signed.
	Identities []string  `json:"identities,omitempty"`
	Error      string    `json:"error,omitempty"`
	Date       time.Time `json:"date"`
	// RequirementMet is true when the attestation is verified and
	// satisfies the RequiredAttestation of its Node.
	RequirementMet    bool     `json:"requirementMet"`
	RequirementErrors []string `json:"requirementErrors,omitempty"`
}

// Attestation is an in-toto attestation uploaded for a Node within a Seed.
type Attestation struct {
	Meta
	SeedID        uuid.UUID       `json:"seedId"`
	NodeID        uuid.UUID       `json:"nodeId"`
	Raw           json.RawMessage `json:"raw,omitempty"`
	Format        string          `json:"format"`
	PredicateType string          `json:"predicateType"`
	Subjects      []Subject       `json:"subjects"`
	Verification  Verification    `json:"verification"`
}
