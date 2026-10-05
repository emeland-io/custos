// Package attest defines how custos verifies the documents processors
// output (in-toto statements, DSSE envelopes, Sigstore bundles) and how it
// compares them. The signature checks live in the backend subpackage
// carabiner, behind the Verifier interface (ADR 0001); everything here uses
// the standard library only.
package attest

// Verification states, as stored in a document's verification.status.
const (
	StatusVerified = "verified" // signed by a trusted key or a valid Sigstore bundle
	StatusFailed   = "failed"   // signatures present but none verifies, payload tampered, or the content is ambiguous or malformed enough that a verifier cannot trust what it verified (e.g. duplicate or case-variant JSON keys, a parse the verifying library and Unwrap disagree about)
	StatusUnsigned = "unsigned" // bare statement, envelope without signatures, or not an attestation
)

// Result is the outcome of verifying one document's content.
type Result struct {
	Status string
	// Signers holds the identities of verified signers, sorted; empty
	// unless Status is StatusVerified.
	Signers []string
	// Payload is the canonical JSON of the statement (or of the content
	// when it is not an attestation); documents compare on it (§5.4).
	//
	// Payload is authenticated — safe to treat as what a trusted signer
	// actually signed — only when Status is StatusVerified. For
	// StatusUnsigned, Payload is just the content's own unverified claim
	// about itself: nothing signed it. For StatusFailed, Payload is still
	// only an unverified claim, and may not even be the bytes any
	// signature covers — a Verifier can reach StatusFailed from content
	// designed to make its own parse disagree with what was actually
	// verified (or not verified at all), so an attacker may have chosen
	// Payload's contents freely. Callers must never make a trust decision
	// from Payload without first checking Status == StatusVerified.
	Payload []byte
}

// Verifier checks the signatures of a document's content.
type Verifier interface {
	Verify(content []byte) Result // never fails: unreadable input is StatusUnsigned with Payload = canonical JSON of content
}

// Unverified is a Verifier that never checks signatures: Status is
// "unsigned" for everything, Payload as above. processor test uses it when no
// trusted keys are given.
var Unverified Verifier = unverified{}

type unverified struct{}

func (unverified) Verify(content []byte) Result {
	return Result{Status: StatusUnsigned, Payload: Unwrap(content).Payload}
}
