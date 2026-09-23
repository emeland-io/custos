// Package attest defines how custos inspects and verifies in-toto
// attestations. The implementation lives in a backend subpackage so it can
// be replaced without touching the rest of the server.
package attest

import (
	"errors"
	"fmt"
	"slices"

	"github.com/emeland-io/custos/internal/model"
)

// ErrNotAttestation is returned when the data is neither a DSSE envelope,
// a Sigstore bundle nor a bare in-toto statement.
var ErrNotAttestation = errors.New("data is not an in-toto attestation")

// Format names the wrapper an attestation was uploaded in.
const (
	FormatDSSE   = "dsse"
	FormatBundle = "sigstore-bundle"
	FormatBare   = "bare"
)

// Parsed is the content of an attestation, independent of its signatures.
type Parsed struct {
	Format        string
	PredicateType string
	Subjects      []model.Subject
}

// Key describes a trusted public key.
type Key struct {
	// File is the name of the key file in the keys directory.
	File string `json:"file"`
	// Identity is the identity spec a Node can require to match this key.
	Identity string `json:"identity"`
}

// Verifier inspects and verifies attestations.
type Verifier interface {
	// Inspect parses raw without verifying signatures.
	Inspect(raw []byte) (Parsed, error)
	// Verify checks the signatures of raw and whether it satisfies req.
	Verify(raw []byte, req model.RequiredAttestation) (Parsed, model.Verification, error)
}

// CheckRequirement returns the ways in which an attestation fails req.
// signed reports whether the identity spec signed the attestation.
func CheckRequirement(p Parsed, req model.RequiredAttestation, signed func(spec string) (bool, error)) []string {
	var errs []string
	if req.PredicateType != "" && p.PredicateType != req.PredicateType {
		errs = append(errs, fmt.Sprintf("predicate type is %q, required %q", p.PredicateType, req.PredicateType))
	}
	if req.SubjectName != "" && !slices.ContainsFunc(p.Subjects, func(s model.Subject) bool { return s.Name == req.SubjectName }) {
		errs = append(errs, fmt.Sprintf("no subject named %q", req.SubjectName))
	}
	for _, spec := range req.RequiredIdentities {
		ok, err := signed(spec)
		switch {
		case err != nil:
			errs = append(errs, fmt.Sprintf("invalid identity %q: %v", spec, err))
		case !ok:
			errs = append(errs, fmt.Sprintf("not signed by %q", spec))
		}
	}
	return errs
}
