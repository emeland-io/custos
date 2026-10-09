// Package carabiner implements attest.Verifier with the carabiner-dev
// signer library (ADR 0001). It is the only package of custos that imports
// carabiner-dev modules.
package carabiner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/carabiner-dev/signer"
	api "github.com/carabiner-dev/signer/api/v1"
	"github.com/carabiner-dev/signer/key"
	"github.com/carabiner-dev/signer/options"

	"github.com/emeland-io/custos/internal/attest"
)

// sharedSigner is created once per process: constructing a signer
// verifier resolves the Sigstore trust root, which tries a TUF refresh
// (about 1.5 s offline) when the embedded copy is older than 30 days
// (ADR 0001 finding 4).
var sharedSigner = sync.OnceValue(func() *signer.Verifier { return signer.NewVerifier() })

// Verifier verifies documents against the trusted public keys and, for
// Sigstore bundles, the embedded Sigstore trust root. It is safe for
// concurrent use.
type Verifier struct {
	keys []key.PublicKeyProvider
}

var _ attest.Verifier = (*Verifier)(nil)

// New loads every *.pem / *.pub public key in keysDir ("" = none) and
// returns a Verifier. Sigstore bundles verify offline with the embedded
// trust root (ADR 0001 findings 2 and 4); one signer.Verifier per process.
//
// Other files and subdirectories of keysDir are ignored. A keysDir that
// does not exist, or a key file that cannot be read or parsed, is an error
// naming the file.
func New(keysDir string) (*Verifier, error) {
	v := &Verifier{}
	if keysDir == "" {
		return v, nil
	}
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		return nil, fmt.Errorf("reading trusted keys: %w", err)
	}
	parser := key.NewParser()
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if ext != ".pem" && ext != ".pub" {
			continue
		}
		path := filepath.Join(keysDir, e.Name())
		// Stat follows symbolic links, as in mounted Kubernetes secrets.
		if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading trusted key: %w", err)
		}
		p, err := parser.ParsePublicKeyProvider(data)
		if err != nil {
			return nil, fmt.Errorf("trusted key %s: %w", path, err)
		}
		if _, err := p.PublicKey(); err != nil {
			return nil, fmt.Errorf("trusted key %s: %w", path, err)
		}
		v.keys = append(v.keys, p)
	}
	return v, nil
}

// Verify implements attest.Verifier. Bare content and envelopes without
// signatures are unsigned; an envelope or bundle whose signatures cannot
// be verified for any reason (no matching trusted key, no trusted keys at
// all, a modified payload, a malformed envelope, a payload that is not
// JSON) is failed.
//
// A verified Result's Payload is taken from the very artifact the signer
// library parsed and checked the signatures of (signer.ParseArtifact,
// then VerifyStatement on its result), never from a second parse of
// content: the library routes and decodes JSON its own way (an
// encoding/json probe that matches keys case-insensitively, the last
// duplicate winning; protojson, which also accepts proto field names such
// as dsse_envelope), and any second parser can be made to disagree with
// it about which payload a document carries. Payload is still always
// attest.Unwrap(content).Payload, as for attest.Unverified: a document on
// which Unwrap and the library disagree is failed, whatever the
// signatures say.
func (v *Verifier) Verify(content []byte) (result attest.Result) {
	// The carabiner-dev library, sigstore-go and protojson are not known to
	// panic on malformed input, but nothing guarantees it either. Verify
	// runs in worker goroutines (plan 3d), where an unrecovered panic would
	// kill the whole serve process rather than fail one request, so guard
	// against it here instead of relying solely on the dependencies' own
	// care.
	defer func() {
		if recover() != nil {
			result = attest.Result{Status: attest.StatusFailed, Payload: attest.Unwrap(content).Payload}
		}
	}()
	u := attest.Unwrap(content)
	res := attest.Result{Status: attest.StatusUnsigned, Payload: u.Payload}
	// What Unwrap sees decides only between unsigned and failed when the
	// library cannot conclude, or concludes UNSIGNED; neither carries a
	// trust claim.
	notVerified := attest.StatusUnsigned
	if u.Kind != attest.KindBare && u.Signatures > 0 {
		notVerified = attest.StatusFailed
	}

	art, err := signer.ParseArtifact(content)
	if err != nil {
		// Not a DSSE envelope or Sigstore bundle as the library reads it.
		res.Status = notVerified
		return res
	}
	opts := []options.VerificationOptFunc{
		options.WithRekorVerification(false),
		// custos records who signed; it does not require particular
		// signers, so the bundle verifier must not insist on one.
		options.WithSkipIdentityCheck(true),
	}
	if len(v.keys) > 0 {
		opts = append(opts, options.WithPublicKeys(v.keys...))
	}
	ver, err := sharedSigner().VerifyStatement(art, opts...)
	if err != nil {
		res.Status = notVerified
		return res
	}
	sig := ver.GetSignature()
	switch sig.GetStatus() {
	case api.VerificationStatus_VERIFIED:
		payload, err := attest.Canonical(signedPayload(art))
		ids := signers(sig.GetIdentities())
		// A signed payload that is not JSON is not a statement; one that
		// differs from what Unwrap extracts means content is ambiguous.
		if err != nil || !bytes.Equal(payload, u.Payload) || len(ids) == 0 {
			res.Status = attest.StatusFailed
			return res
		}
		res.Status = attest.StatusVerified
		res.Signers = ids
		res.Payload = payload
	case api.VerificationStatus_UNSIGNED:
		res.Status = notVerified
	default:
		res.Status = attest.StatusFailed
	}
	return res
}

// signedPayload returns the payload of the DSSE envelope of art: the
// bytes whose signatures VerifyStatement checked (the key and SPIFFE
// verifiers check signatures over the PAE of this envelope's payload,
// sigstore-go re-encodes the bundle's envelope from these bytes). Nil
// when art carries no DSSE envelope.
func signedPayload(art signer.SignedArtifact) []byte {
	switch a := art.(type) {
	case *signer.EnvelopeArtifact:
		return a.Envelope.GetPayload()
	case *signer.BundleArtifact:
		if a.Bundle != nil {
			return a.Bundle.GetDsseEnvelope().GetPayload()
		}
	}
	return nil
}

// signers returns the identity specs of ids, sorted and without
// duplicates.
func signers(ids []*api.Identity) []string {
	var out []string
	for _, id := range ids {
		if s := strings.TrimSpace(id.Spec()); s != "" {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
