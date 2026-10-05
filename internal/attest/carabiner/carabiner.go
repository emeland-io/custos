// Package carabiner implements attest.Verifier with the carabiner-dev
// signer library (ADR 0001). It is the only package of custos that imports
// carabiner-dev modules.
package carabiner

import (
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
// all, a modified payload, a malformed envelope) is failed.
func (v *Verifier) Verify(content []byte) attest.Result {
	u := attest.Unwrap(content)
	res := attest.Result{Status: attest.StatusUnsigned, Payload: u.Payload}
	if u.Kind == attest.KindBare || u.Signatures == 0 {
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
	ver, err := sharedSigner().VerifyStatementBytes(content, opts...)
	if err != nil {
		res.Status = attest.StatusFailed
		return res
	}
	sig := ver.GetSignature()
	switch sig.GetStatus() {
	case api.VerificationStatus_VERIFIED:
		res.Status = attest.StatusVerified
		res.Signers = signers(sig.GetIdentities())
		if len(res.Signers) == 0 {
			res.Status = attest.StatusFailed
		}
	case api.VerificationStatus_UNSIGNED:
		res.Status = attest.StatusUnsigned
	default:
		res.Status = attest.StatusFailed
	}
	return res
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
